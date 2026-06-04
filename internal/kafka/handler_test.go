package kafka

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	commonskafka "github.com/trogers1052/trading-go-commons/kafka"

	"github.com/trogers1052/alert-service/internal/models"
)

// ---------------------------------------------------------------------------
// These tests drive the consumer's message Handler (handleMessage) directly.
// Offset commits, reconnect and shutdown are now owned by the shared
// trading-go-commons ConsumerGroup, so the unit tests verify the alert-service
// decode/validate/retry/dead-letter discipline and the "always commit" contract
// (handleMessage always returns nil → the shared runner marks the message).
// ---------------------------------------------------------------------------

const (
	decTopic  = "trading.decisions"
	rankTopic = "trading.rankings"
)

func newTestConsumer() *Consumer {
	return &Consumer{
		decisionTopic: decTopic,
		rankingTopic:  rankTopic,
		ready:         make(chan struct{}),
		metrics:       nopMetrics{},
	}
}

// nopMetrics satisfies metrics.Recorder without importing the metrics package
// indirection in every test (the real metrics.Nop also works, but this keeps
// the consumer's metric calls observable if needed later).
type nopMetrics struct{}

func (nopMetrics) IncAlertSent(string, string)     {}
func (nopMetrics) IncKafkaConsumed(string)         {}
func (nopMetrics) IncCooldownSkipped()             {}
func (nopMetrics) IncQuietHoursSkipped()           {}
func (nopMetrics) ObserveTelegramDuration(float64) {}
func (nopMetrics) IncTelegramErrors()              {}
func (nopMetrics) IncFeedbackReceived(string)      {}
func (nopMetrics) IncFeedbackPostErrors()          {}
func (nopMetrics) SetCooldownEntries(float64)      {}
func (nopMetrics) IncDeadLetters()                 {}

func validDecisionRaw(t *testing.T) []byte {
	t.Helper()
	ev := models.DecisionEvent{
		EventType:     models.EventTypeDecision,
		Source:        "decision-engine",
		SchemaVersion: "1.2",
		Timestamp:     time.Now().UTC(),
		Data: models.DecisionData{
			Symbol:     "AAPL",
			Signal:     "BUY",
			Confidence: 0.8,
		},
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// handle drives a single message through the consumer's Handler and asserts the
// "always commit" contract: handleMessage must return nil so the shared runner
// marks the offset (mirroring the old ConsumeClaim fall-through to MarkMessage).
func handle(t *testing.T, c *Consumer, msg *commonskafka.Message) {
	t.Helper()
	if err := c.handleMessage(context.Background(), msg); err != nil {
		t.Fatalf("handleMessage returned err %v, want nil (message must always be committed)", err)
	}
}

func TestSetHandlers(t *testing.T) {
	c := newTestConsumer()
	c.SetDecisionHandler(func(context.Context, interface{}) error { return nil })
	c.SetRankingHandler(func(context.Context, interface{}) error { return nil })
	if c.decisionHandler == nil || c.rankingHandler == nil {
		t.Fatal("handlers not set")
	}
}

func TestHandleMessage_ValidDecision(t *testing.T) {
	c := newTestConsumer()
	var handled int
	c.SetDecisionHandler(func(context.Context, interface{}) error { handled++; return nil })

	handle(t, c, &commonskafka.Message{Topic: decTopic, Value: validDecisionRaw(t)})
	if handled != 1 {
		t.Fatalf("decision handler called %d times, want 1", handled)
	}
}

func TestHandleMessage_MalformedDecision(t *testing.T) {
	c := newTestConsumer()
	var handled int
	c.SetDecisionHandler(func(context.Context, interface{}) error { handled++; return nil })

	// Malformed JSON must be committed (return nil) without invoking the handler.
	handle(t, c, &commonskafka.Message{Topic: decTopic, Value: []byte("{bad json")})
	if handled != 0 {
		t.Fatalf("handler should not run on malformed json, got %d", handled)
	}
}

func TestHandleMessage_InvalidDecision(t *testing.T) {
	c := newTestConsumer()
	var handled int
	c.SetDecisionHandler(func(context.Context, interface{}) error { handled++; return nil })

	// Missing symbol → Validate fails. Must be committed without invoking handler.
	bad := models.DecisionEvent{
		EventType: "DECISION",
		Data:      models.DecisionData{Signal: "BUY", Confidence: 0.5},
	}
	raw, _ := json.Marshal(bad)
	handle(t, c, &commonskafka.Message{Topic: decTopic, Value: raw})
	if handled != 0 {
		t.Fatalf("handler should not run on invalid event, got %d", handled)
	}
}

func TestHandleMessage_DecisionHandlerFails_WritesDeadLetter(t *testing.T) {
	// Redirect dead-letter file to a temp path. A handler that always fails
	// exhausts handlerMaxRetries=3 with a 2s delay (~4s); acceptable for a
	// single test.
	dir := t.TempDir()
	dlPath := filepath.Join(dir, "dead_letters.jsonl")
	t.Setenv("DEAD_LETTER_PATH", dlPath)

	c := newTestConsumer()
	var deadLetters int
	c.metrics = countingMetrics{dl: &deadLetters}
	c.SetDecisionHandler(func(context.Context, interface{}) error {
		return context.DeadlineExceeded
	})

	// Message is still committed (handleMessage returns nil) after exhausting
	// retries and dead-lettering.
	handle(t, c, &commonskafka.Message{Topic: decTopic, Value: validDecisionRaw(t), Offset: 7})

	if deadLetters != 1 {
		t.Fatalf("expected 1 dead-letter metric, got %d", deadLetters)
	}
	data, err := os.ReadFile(dlPath)
	if err != nil {
		t.Fatalf("read dead letter file: %v", err)
	}
	if !strings.Contains(string(data), "AAPL") {
		t.Fatalf("dead letter should contain symbol, got: %s", data)
	}
}

type countingMetrics struct {
	nopMetrics
	dl *int
}

func (c countingMetrics) IncDeadLetters() { *c.dl++ }

func TestHandleMessage_ValidRanking(t *testing.T) {
	c := newTestConsumer()
	var handled int
	c.SetRankingHandler(func(context.Context, interface{}) error { handled++; return nil })

	ev := models.RankingEvent{
		EventType: "RANKING",
		Data:      models.RankingData{SignalType: "BUY", Rankings: []models.SymbolRanking{{Symbol: "AAPL"}}},
	}
	raw, _ := json.Marshal(ev)
	handle(t, c, &commonskafka.Message{Topic: rankTopic, Value: raw})
	if handled != 1 {
		t.Fatalf("ranking handler called %d times, want 1", handled)
	}
}

func TestHandleMessage_MalformedRanking(t *testing.T) {
	c := newTestConsumer()
	var handled int
	c.SetRankingHandler(func(context.Context, interface{}) error { handled++; return nil })

	handle(t, c, &commonskafka.Message{Topic: rankTopic, Value: []byte("nope")})
	if handled != 0 {
		t.Fatalf("handler should not run on malformed ranking, got %d", handled)
	}
}

func TestHandleMessage_RankingHandlerError(t *testing.T) {
	c := newTestConsumer()
	c.SetRankingHandler(func(context.Context, interface{}) error { return context.Canceled })

	ev := models.RankingEvent{EventType: "RANKING", Data: models.RankingData{SignalType: "BUY"}}
	raw, _ := json.Marshal(ev)
	// Ranking handler errors are logged but the message is still committed.
	handle(t, c, &commonskafka.Message{Topic: rankTopic, Value: raw})
}

func TestHandleMessage_UnknownTopic(t *testing.T) {
	c := newTestConsumer()
	// Unknown-topic message must be committed (return nil) without dispatch.
	handle(t, c, &commonskafka.Message{Topic: "other.topic", Value: []byte("{}")})
}

func TestHandleMessage_NilHandlers(t *testing.T) {
	c := newTestConsumer() // no handlers
	handle(t, c, &commonskafka.Message{Topic: decTopic, Value: validDecisionRaw(t)})
	handle(t, c, &commonskafka.Message{Topic: rankTopic, Value: []byte(`{"event_type":"RANKING"}`)})
}

func TestWriteDeadLetter_OpenError(t *testing.T) {
	// Point at an unwritable path (a directory) so OpenFile fails and the
	// fallback log branch is exercised. Should not panic.
	t.Setenv("DEAD_LETTER_PATH", t.TempDir()) // a directory, not a file
	writeDeadLetter([]byte(`{"x":1}`), "SYM", 1, context.DeadlineExceeded)
}
