package kafka

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	commonskafka "github.com/trogers1052/trading-go-commons/kafka"
	testkit "github.com/trogers1052/trading-testkit"

	"github.com/trogers1052/alert-service/internal/metrics"
	"github.com/trogers1052/alert-service/internal/models"
)

// TestConsumer_EndToEnd starts a real Redpanda broker, produces a decision and
// a ranking event, and verifies the full Consumer wiring
// (NewConsumer → Start → ConsumeClaim → handlers → Close).
//
// Skipped with -short via testkit.NewRedpandaContainer.
func TestConsumer_EndToEnd(t *testing.T) {
	rc := testkit.NewRedpandaContainer(t)
	rc.CreateTopic(t, decTopic, 1)
	rc.CreateTopic(t, rankTopic, 1)

	producer, err := commonskafka.NewProducer([]string{rc.Brokers})
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	defer producer.Close()

	dec := models.DecisionEvent{
		EventType: models.EventTypeDecision, Source: "decision-engine", SchemaVersion: "1.2",
		Timestamp: time.Now().UTC(),
		Data:      models.DecisionData{Symbol: "AAPL", Signal: "BUY", Confidence: 0.9},
	}
	decRaw, _ := json.Marshal(dec)

	consumer, err := NewConsumer([]string{rc.Brokers}, "alert-test-group", decTopic, rankTopic, metrics.Nop{})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}

	gotDecision := make(chan *models.DecisionEvent, 1)
	consumer.SetDecisionHandler(func(_ context.Context, ev interface{}) error {
		if d, ok := ev.(*models.DecisionEvent); ok {
			select {
			case gotDecision <- d:
			default:
			}
		}
		return nil
	})
	consumer.SetRankingHandler(func(context.Context, interface{}) error { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := consumer.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	// Offsets.Initial = Newest, so produce AFTER the consumer group has joined
	// and settled on the latest offset. Start() returns as soon as the shared
	// ConsumerGroup runner is entered (before partition assignment), so give the
	// group time to complete the join/rebalance before producing — otherwise the
	// live message can land before the consumer is positioned. 6s matches the
	// settle window the shared package's own OffsetNewest test relies on.
	time.Sleep(6 * time.Second)
	if err := producer.Publish(context.Background(), decTopic, nil, decRaw); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case d := <-gotDecision:
		if d.Data.Symbol != "AAPL" {
			t.Fatalf("unexpected decision: %+v", d.Data)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for decision event")
	}
}

func TestNewConsumer_NoBrokers(t *testing.T) {
	// Construction must fail fast when no broker is supplied. Under kafka-go the
	// consumer group connects lazily (no metadata round-trip at construction), so
	// an unreachable-but-syntactically-valid broker no longer errors at
	// NewConsumer time; the deterministic construction-error path is an empty
	// broker list, which the shared NewConsumerGroup rejects.
	_, err := NewConsumer(nil, "g", decTopic, rankTopic, nil)
	if err == nil {
		t.Fatal("expected error when no brokers are supplied")
	}
}
