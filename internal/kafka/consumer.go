package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/trogers1052/alert-service/internal/models"
)

const (
	// handlerMaxRetries is the number of times to retry a failed handler before
	// giving up and committing the offset.  Trading alerts are high-value so we
	// retry a few times to ride out transient Telegram failures.
	handlerMaxRetries = 3
	handlerRetryDelay = 2 * time.Second

	// deadLetterPath is the file where failed decision events are persisted
	// so they can be recovered and replayed.  This prevents silent alert loss
	// when Telegram/stock-service is down for extended periods.
	deadLetterPath = "/var/lib/alert-service/dead_letters.jsonl"
)

// MessageHandler is called when a message is received
type MessageHandler func(ctx context.Context, event interface{}) error

// Consumer wraps Sarama consumer group for Kafka consumption
type Consumer struct {
	client           sarama.ConsumerGroup
	decisionTopic    string
	rankingTopic     string
	decisionHandler  MessageHandler
	rankingHandler   MessageHandler
	ready            chan bool
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

// NewConsumer creates a new Kafka consumer
func NewConsumer(brokers []string, groupID, decisionTopic, rankingTopic string) (*Consumer, error) {
	config := sarama.NewConfig()
	config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	config.Consumer.Offsets.Initial = sarama.OffsetNewest
	config.Version = sarama.V2_8_0_0

	client, err := sarama.NewConsumerGroup(brokers, groupID, config)
	if err != nil {
		return nil, err
	}

	return &Consumer{
		client:        client,
		decisionTopic: decisionTopic,
		rankingTopic:  rankingTopic,
		ready:         make(chan bool),
	}, nil
}

// SetDecisionHandler sets the handler for decision events
func (c *Consumer) SetDecisionHandler(handler MessageHandler) {
	c.decisionHandler = handler
}

// SetRankingHandler sets the handler for ranking events
func (c *Consumer) SetRankingHandler(handler MessageHandler) {
	c.rankingHandler = handler
}

// Start begins consuming messages from both topics
func (c *Consumer) Start(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)

	topics := []string{c.decisionTopic, c.rankingTopic}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			handler := &consumerGroupHandler{
				consumer: c,
				ready:    c.ready,
			}

			if err := c.client.Consume(ctx, topics, handler); err != nil {
				log.Printf("Error from consumer: %v", err)
			}

			if ctx.Err() != nil {
				return
			}

			c.ready = make(chan bool)
		}
	}()

	<-c.ready
	log.Println("Kafka consumer started and ready")
	return nil
}

// Close stops the consumer gracefully
func (c *Consumer) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	return c.client.Close()
}

// consumerGroupHandler implements sarama.ConsumerGroupHandler
type consumerGroupHandler struct {
	consumer *Consumer
	ready    chan bool
}

func (h *consumerGroupHandler) Setup(sarama.ConsumerGroupSession) error {
	close(h.ready)
	return nil
}

func (h *consumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

func (h *consumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case message, ok := <-claim.Messages():
			if !ok {
				return nil
			}

			ctx := session.Context()

			// Determine message type based on topic
			switch message.Topic {
			case h.consumer.decisionTopic:
				if h.consumer.decisionHandler != nil {
					var event models.DecisionEvent
					if err := json.Unmarshal(message.Value, &event); err != nil {
						log.Printf("WARNING: Failed to unmarshal decision event (offset %d): %v", message.Offset, err)
						session.MarkMessage(message, "")
						continue
					}

					if err := event.Validate(); err != nil {
						log.Printf("WARNING: Rejecting malformed decision event (offset %d): %v", message.Offset, err)
						session.MarkMessage(message, "")
						continue
					}

					log.Printf("Received decision: %s %s (confidence=%.2f, offset=%d)",
						event.Data.Symbol, event.Data.Signal, event.Data.Confidence, message.Offset)

					// Retry handler failures — trading alerts are high-value
					// and transient Telegram errors should not silently drop them.
					var handlerErr error
					for attempt := 1; attempt <= handlerMaxRetries; attempt++ {
						handlerErr = h.consumer.decisionHandler(ctx, &event)
						if handlerErr == nil {
							break
						}
						if attempt < handlerMaxRetries {
							log.Printf("Decision handler attempt %d/%d failed for %s: %v — retrying in %s",
								attempt, handlerMaxRetries, event.Data.Symbol, handlerErr, handlerRetryDelay)
							time.Sleep(handlerRetryDelay)
						}
					}
					if handlerErr != nil {
						log.Printf("CRITICAL: Decision handler FAILED after %d attempts for %s (offset %d): %v — writing to dead letter file",
							handlerMaxRetries, event.Data.Symbol, message.Offset, handlerErr)
						writeDeadLetter(message.Value, event.Data.Symbol, message.Offset, handlerErr)
					}
				}

			case h.consumer.rankingTopic:
				if h.consumer.rankingHandler != nil {
					var event models.RankingEvent
					if err := json.Unmarshal(message.Value, &event); err != nil {
						log.Printf("Failed to unmarshal ranking event: %v", err)
						session.MarkMessage(message, "")
						continue
					}

					log.Printf("Received ranking: %s (%d symbols, offset=%d)",
						event.Data.SignalType, len(event.Data.Rankings), message.Offset)

					if err := h.consumer.rankingHandler(ctx, &event); err != nil {
						log.Printf("Failed to handle ranking event: %v", err)
						// Rankings are lower priority — log and move on
					}
				}
			}

			session.MarkMessage(message, "")

		case <-session.Context().Done():
			return nil
		}
	}
}

// writeDeadLetter appends a failed decision event to a local JSONL file so it
// can be recovered and replayed later.  This prevents silent alert loss when
// downstream services (Telegram, stock-service) are unavailable.
func writeDeadLetter(raw []byte, symbol string, offset int64, handlerErr error) {
	entry := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"symbol":    symbol,
		"offset":    offset,
		"error":     handlerErr.Error(),
		"event":     json.RawMessage(raw),
	}

	line, err := json.Marshal(entry)
	if err != nil {
		log.Printf("ERROR: failed to marshal dead letter entry: %v", err)
		return
	}

	path := os.Getenv("DEAD_LETTER_PATH")
	if path == "" {
		path = deadLetterPath
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("ERROR: failed to open dead letter file %s: %v", path, err)
		// Last resort: log the full event so it's at least in stdout/journald
		log.Printf("DEAD_LETTER: %s", string(line))
		return
	}
	defer f.Close()

	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		log.Printf("ERROR: failed to write dead letter: %v", err)
		log.Printf("DEAD_LETTER: %s", string(line))
	}
}
