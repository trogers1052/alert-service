package kafka

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/IBM/sarama"
	"github.com/trogers1052/alert-service/internal/models"
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
						log.Printf("Failed to unmarshal decision event: %v", err)
						session.MarkMessage(message, "")
						continue
					}

					if err := h.consumer.decisionHandler(ctx, &event); err != nil {
						log.Printf("Failed to handle decision event: %v", err)
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

					if err := h.consumer.rankingHandler(ctx, &event); err != nil {
						log.Printf("Failed to handle ranking event: %v", err)
					}
				}
			}

			session.MarkMessage(message, "")

		case <-session.Context().Done():
			return nil
		}
	}
}
