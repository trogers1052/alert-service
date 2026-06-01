package kafka

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"

	"github.com/trogers1052/alert-service/internal/models"
)

// ---------------------------------------------------------------------------
// Fake sarama session + claim
// ---------------------------------------------------------------------------

type fakeSession struct {
	ctx    context.Context
	marked int
}

func (f *fakeSession) Claims() map[string][]int32                   { return nil }
func (f *fakeSession) MemberID() string                             { return "m" }
func (f *fakeSession) GenerationID() int32                          { return 1 }
func (f *fakeSession) MarkOffset(string, int32, int64, string)      {}
func (f *fakeSession) Commit()                                      {}
func (f *fakeSession) ResetOffset(string, int32, int64, string)     {}
func (f *fakeSession) MarkMessage(*sarama.ConsumerMessage, string)  { f.marked++ }
func (f *fakeSession) Context() context.Context                     { return f.ctx }

type fakeClaim struct {
	ch chan *sarama.ConsumerMessage
}

func (f *fakeClaim) Topic() string                            { return "" }
func (f *fakeClaim) Partition() int32                         { return 0 }
func (f *fakeClaim) InitialOffset() int64                     { return 0 }
func (f *fakeClaim) HighWaterMarkOffset() int64               { return 0 }
func (f *fakeClaim) Messages() <-chan *sarama.ConsumerMessage { return f.ch }

const (
	decTopic  = "trading.decisions"
	rankTopic = "trading.rankings"
)

func newTestConsumer() *Consumer {
	return &Consumer{
		decisionTopic: decTopic,
		rankingTopic:  rankTopic,
		ready:         make(chan bool),
		metrics:       nopMetrics{},
	}
}

// nopMetrics satisfies metrics.Recorder without importing the metrics package
// indirection in every test (the real metrics.Nop also works, but this keeps
// the consumer's metric calls observable if needed later).
type nopMetrics struct{}

func (nopMetrics) IncAlertSent(string, string)    {}
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
		EventType:     "DECISION",
		Source:        "decision-engine",
		SchemaVersion: "1.0",
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

func runClaim(t *testing.T, h *consumerGroupHandler, msgs []*sarama.ConsumerMessage) *fakeSession {
	t.Helper()
	sess := &fakeSession{ctx: context.Background()}
	claim := &fakeClaim{ch: make(chan *sarama.ConsumerMessage, len(msgs)+1)}
	for _, m := range msgs {
		claim.ch <- m
	}
	close(claim.ch)
	if err := h.ConsumeClaim(sess, claim); err != nil {
		t.Fatalf("ConsumeClaim: %v", err)
	}
	return sess
}

func TestSetHandlers(t *testing.T) {
	c := newTestConsumer()
	c.SetDecisionHandler(func(context.Context, interface{}) error { return nil })
	c.SetRankingHandler(func(context.Context, interface{}) error { return nil })
	if c.decisionHandler == nil || c.rankingHandler == nil {
		t.Fatal("handlers not set")
	}
}

func TestConsumeClaim_ValidDecision(t *testing.T) {
	c := newTestConsumer()
	var handled int
	c.SetDecisionHandler(func(context.Context, interface{}) error { handled++; return nil })
	h := &consumerGroupHandler{consumer: c, ready: make(chan bool)}

	sess := runClaim(t, h, []*sarama.ConsumerMessage{
		{Topic: decTopic, Value: validDecisionRaw(t)},
	})
	if handled != 1 {
		t.Fatalf("decision handler called %d times, want 1", handled)
	}
	if sess.marked != 1 {
		t.Fatalf("marked %d, want 1", sess.marked)
	}
}

func TestConsumeClaim_MalformedDecision(t *testing.T) {
	c := newTestConsumer()
	var handled int
	c.SetDecisionHandler(func(context.Context, interface{}) error { handled++; return nil })
	h := &consumerGroupHandler{consumer: c, ready: make(chan bool)}

	sess := runClaim(t, h, []*sarama.ConsumerMessage{
		{Topic: decTopic, Value: []byte("{bad json")},
	})
	if handled != 0 {
		t.Fatalf("handler should not run on malformed json, got %d", handled)
	}
	if sess.marked != 1 {
		t.Fatalf("malformed message should be marked, got %d", sess.marked)
	}
}

func TestConsumeClaim_InvalidDecision(t *testing.T) {
	c := newTestConsumer()
	var handled int
	c.SetDecisionHandler(func(context.Context, interface{}) error { handled++; return nil })
	h := &consumerGroupHandler{consumer: c, ready: make(chan bool)}

	// Missing symbol → Validate fails.
	bad := models.DecisionEvent{
		EventType: "DECISION",
		Data:      models.DecisionData{Signal: "BUY", Confidence: 0.5},
	}
	raw, _ := json.Marshal(bad)
	sess := runClaim(t, h, []*sarama.ConsumerMessage{{Topic: decTopic, Value: raw}})
	if handled != 0 {
		t.Fatalf("handler should not run on invalid event, got %d", handled)
	}
	if sess.marked != 1 {
		t.Fatalf("invalid message should be marked, got %d", sess.marked)
	}
}

func TestConsumeClaim_DecisionHandlerFails_WritesDeadLetter(t *testing.T) {
	// Redirect dead-letter file to a temp path and shorten retry behaviour by
	// using a handler that always fails. handlerMaxRetries=3 with a 2s delay
	// means ~4s; acceptable for a single test.
	dir := t.TempDir()
	dlPath := filepath.Join(dir, "dead_letters.jsonl")
	t.Setenv("DEAD_LETTER_PATH", dlPath)

	c := newTestConsumer()
	var deadLetters int
	c.metrics = countingMetrics{dl: &deadLetters}
	c.SetDecisionHandler(func(context.Context, interface{}) error {
		return context.DeadlineExceeded
	})
	h := &consumerGroupHandler{consumer: c, ready: make(chan bool)}

	sess := runClaim(t, h, []*sarama.ConsumerMessage{
		{Topic: decTopic, Value: validDecisionRaw(t), Offset: 7},
	})

	// Message is still marked after exhausting retries.
	if sess.marked != 1 {
		t.Fatalf("message should be marked after dead-lettering, got %d", sess.marked)
	}
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

func TestConsumeClaim_ValidRanking(t *testing.T) {
	c := newTestConsumer()
	var handled int
	c.SetRankingHandler(func(context.Context, interface{}) error { handled++; return nil })
	h := &consumerGroupHandler{consumer: c, ready: make(chan bool)}

	ev := models.RankingEvent{
		EventType: "RANKING",
		Data:      models.RankingData{SignalType: "BUY", Rankings: []models.SymbolRanking{{Symbol: "AAPL"}}},
	}
	raw, _ := json.Marshal(ev)
	sess := runClaim(t, h, []*sarama.ConsumerMessage{{Topic: rankTopic, Value: raw}})
	if handled != 1 {
		t.Fatalf("ranking handler called %d times, want 1", handled)
	}
	if sess.marked != 1 {
		t.Fatalf("marked %d, want 1", sess.marked)
	}
}

func TestConsumeClaim_MalformedRanking(t *testing.T) {
	c := newTestConsumer()
	var handled int
	c.SetRankingHandler(func(context.Context, interface{}) error { handled++; return nil })
	h := &consumerGroupHandler{consumer: c, ready: make(chan bool)}

	sess := runClaim(t, h, []*sarama.ConsumerMessage{{Topic: rankTopic, Value: []byte("nope")}})
	if handled != 0 {
		t.Fatalf("handler should not run on malformed ranking, got %d", handled)
	}
	if sess.marked != 1 {
		t.Fatalf("malformed ranking should be marked, got %d", sess.marked)
	}
}

func TestConsumeClaim_RankingHandlerError(t *testing.T) {
	c := newTestConsumer()
	c.SetRankingHandler(func(context.Context, interface{}) error { return context.Canceled })
	h := &consumerGroupHandler{consumer: c, ready: make(chan bool)}

	ev := models.RankingEvent{EventType: "RANKING", Data: models.RankingData{SignalType: "BUY"}}
	raw, _ := json.Marshal(ev)
	// Ranking handler errors are logged but message is still marked.
	sess := runClaim(t, h, []*sarama.ConsumerMessage{{Topic: rankTopic, Value: raw}})
	if sess.marked != 1 {
		t.Fatalf("ranking message should be marked even on handler error, got %d", sess.marked)
	}
}

func TestConsumeClaim_UnknownTopic(t *testing.T) {
	c := newTestConsumer()
	h := &consumerGroupHandler{consumer: c, ready: make(chan bool)}
	sess := runClaim(t, h, []*sarama.ConsumerMessage{{Topic: "other.topic", Value: []byte("{}")}})
	if sess.marked != 1 {
		t.Fatalf("unknown-topic message should be marked, got %d", sess.marked)
	}
}

func TestConsumeClaim_NilHandlers(t *testing.T) {
	c := newTestConsumer() // no handlers
	h := &consumerGroupHandler{consumer: c, ready: make(chan bool)}
	sess := runClaim(t, h, []*sarama.ConsumerMessage{
		{Topic: decTopic, Value: validDecisionRaw(t)},
		{Topic: rankTopic, Value: []byte(`{"event_type":"RANKING"}`)},
	})
	if sess.marked != 2 {
		t.Fatalf("both messages should be marked with nil handlers, got %d", sess.marked)
	}
}

func TestConsumeClaim_ContextCancelled(t *testing.T) {
	c := newTestConsumer()
	h := &consumerGroupHandler{consumer: c, ready: make(chan bool)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sess := &fakeSession{ctx: ctx}
	claim := &fakeClaim{ch: make(chan *sarama.ConsumerMessage)} // never closed
	if err := h.ConsumeClaim(sess, claim); err != nil {
		t.Fatalf("ConsumeClaim cancelled ctx: %v", err)
	}
}

func TestSetupCleanup(t *testing.T) {
	ready := make(chan bool)
	h := &consumerGroupHandler{consumer: newTestConsumer(), ready: ready}
	if err := h.Setup(nil); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	select {
	case <-ready:
	default:
		t.Fatal("Setup should close ready")
	}
	if err := h.Cleanup(nil); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
}

func TestWriteDeadLetter_OpenError(t *testing.T) {
	// Point at an unwritable path (a directory) so OpenFile fails and the
	// fallback log branch is exercised. Should not panic.
	t.Setenv("DEAD_LETTER_PATH", t.TempDir()) // a directory, not a file
	writeDeadLetter([]byte(`{"x":1}`), "SYM", 1, context.DeadlineExceeded)
}
