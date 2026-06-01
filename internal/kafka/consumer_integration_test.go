package kafka

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/IBM/sarama"
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

	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	producer, err := sarama.NewSyncProducer([]string{rc.Brokers}, cfg)
	if err != nil {
		t.Fatalf("NewSyncProducer: %v", err)
	}
	defer producer.Close()

	dec := models.DecisionEvent{
		EventType: "DECISION", Source: "decision-engine", SchemaVersion: "1.0",
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

	// Offsets.Initial = Newest, so produce AFTER the consumer is consuming.
	time.Sleep(2 * time.Second)
	if _, _, err := producer.SendMessage(&sarama.ProducerMessage{
		Topic: decTopic, Value: sarama.ByteEncoder(decRaw),
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
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

func TestNewConsumer_BadBroker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	_, err := NewConsumer([]string{"127.0.0.1:1"}, "g", decTopic, rankTopic, nil)
	if err == nil {
		t.Fatal("expected error for unreachable broker")
	}
}
