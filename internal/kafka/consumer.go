package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	commonskafka "github.com/trogers1052/trading-go-commons/kafka"

	"github.com/trogers1052/alert-service/internal/metrics"
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

// Consumer consumes the decision and ranking topics via the shared
// trading-go-commons consumer-group runner. It owns the alert-service-specific
// decode/validate/retry/dead-letter discipline inside its message Handler;
// offset commits and reconnect/rebalance/shutdown are handled by the shared
// ConsumerGroup.
//
// Commit discipline (preserved from the previous hand-rolled consumer): every
// message is ALWAYS marked. Decode/validate failures are logged and marked.
// Decision-handler failures are retried (handlerMaxRetries) then dead-lettered
// and marked. Ranking-handler failures are logged and marked. The Handler
// therefore always returns nil; WithOnError(MarkAndContinue) is configured as a
// belt-and-braces safety net so any unexpected error path still marks rather
// than wedging the consumer in a reprocess loop.
type Consumer struct {
	group         *commonskafka.ConsumerGroup
	decisionTopic string
	rankingTopic  string

	decisionHandler MessageHandler
	rankingHandler  MessageHandler

	cancel context.CancelFunc
	wg     sync.WaitGroup
	ready  chan struct{}

	metrics metrics.Recorder
}

// NewConsumer creates a new Kafka consumer.
// The metrics.Recorder is used to record Kafka consumption and dead letter metrics.
// Pass metrics.Nop{} when metrics collection is not needed.
func NewConsumer(brokers []string, groupID, decisionTopic, rankingTopic string, m metrics.Recorder) (*Consumer, error) {
	if m == nil {
		m = metrics.Nop{}
	}

	c := &Consumer{
		decisionTopic: decisionTopic,
		rankingTopic:  rankingTopic,
		ready:         make(chan struct{}),
		metrics:       m,
	}

	group, err := commonskafka.NewConsumerGroup(
		brokers,
		groupID,
		[]string{decisionTopic, rankingTopic},
		c.handleMessage,
		// Preserve the previous behaviour: a brand-new group starts at the
		// newest offset (only fresh alerts, never a backlog replay).
		commonskafka.WithInitialOffset(commonskafka.OffsetNewest),
		// Always mark on handler error (the Handler already dead-letters and
		// returns nil; this is the safety net for any unexpected error path).
		commonskafka.WithOnError(commonskafka.MarkAndContinue),
		commonskafka.WithConsumerClientID(groupID),
	)
	if err != nil {
		return nil, err
	}
	c.group = group

	return c, nil
}

// SetDecisionHandler sets the handler for decision events
func (c *Consumer) SetDecisionHandler(handler MessageHandler) {
	c.decisionHandler = handler
}

// SetRankingHandler sets the handler for ranking events
func (c *Consumer) SetRankingHandler(handler MessageHandler) {
	c.rankingHandler = handler
}

// Start begins consuming messages from both topics. It launches the shared
// ConsumerGroup runner in the background and returns once consumption has been
// started, matching the non-blocking contract of the previous implementation.
func (c *Consumer) Start(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		// The runner is consuming as soon as Run is entered; signal ready.
		close(c.ready)
		if err := c.group.Run(ctx); err != nil {
			log.Printf("Error from consumer: %v", err)
		}
	}()

	<-c.ready
	log.Println("Kafka consumer started and ready")
	return nil
}

// Close stops the consumer gracefully: it cancels the run context, waits for
// the runner goroutine to exit, then releases the underlying consumer group.
func (c *Consumer) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	if c.group != nil {
		return c.group.Close()
	}
	return nil
}

// handleMessage is the shared-ConsumerGroup Handler. It dispatches by topic into
// the decision/ranking processing, preserving the exact decode/validate/retry/
// dead-letter/commit semantics of the previous hand-rolled ConsumeClaim. It
// always returns nil so every message is marked (committed), matching the prior
// "always MarkMessage" fall-through behaviour.
func (c *Consumer) handleMessage(ctx context.Context, msg *commonskafka.Message) error {
	c.metrics.IncKafkaConsumed(msg.Topic)

	switch msg.Topic {
	case c.decisionTopic:
		c.handleDecision(ctx, msg)
	case c.rankingTopic:
		c.handleRanking(ctx, msg)
	}

	// Always mark — return nil regardless of downstream handler outcome.
	return nil
}

// handleDecision decodes, validates, and dispatches a decision event, retrying
// transient handler failures and dead-lettering on permanent failure.
func (c *Consumer) handleDecision(ctx context.Context, msg *commonskafka.Message) {
	if c.decisionHandler == nil {
		return
	}

	var event models.DecisionEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		log.Printf("WARNING: Failed to unmarshal decision event (offset %d): %v", msg.Offset, err)
		return
	}

	if err := event.Validate(); err != nil {
		log.Printf("WARNING: Rejecting malformed decision event (offset %d): %v", msg.Offset, err)
		return
	}

	log.Printf("Received decision: %s %s (confidence=%.2f, offset=%d)",
		event.Data.Symbol, event.Data.Signal, event.Data.Confidence, msg.Offset)

	// Retry handler failures — trading alerts are high-value and transient
	// Telegram errors should not silently drop them.
	var handlerErr error
	for attempt := 1; attempt <= handlerMaxRetries; attempt++ {
		handlerErr = c.decisionHandler(ctx, &event)
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
			handlerMaxRetries, event.Data.Symbol, msg.Offset, handlerErr)
		writeDeadLetter(msg.Value, event.Data.Symbol, msg.Offset, handlerErr)
		c.metrics.IncDeadLetters()
	}
}

// handleRanking decodes and dispatches a ranking event. Rankings are lower
// priority: decode and handler errors are logged and the message is committed.
func (c *Consumer) handleRanking(ctx context.Context, msg *commonskafka.Message) {
	if c.rankingHandler == nil {
		return
	}

	var event models.RankingEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		log.Printf("Failed to unmarshal ranking event: %v", err)
		return
	}

	log.Printf("Received ranking: %s (%d symbols, offset=%d)",
		event.Data.SignalType, len(event.Data.Rankings), msg.Offset)

	if err := c.rankingHandler(ctx, &event); err != nil {
		log.Printf("Failed to handle ranking event: %v", err)
		// Rankings are lower priority — log and move on
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
