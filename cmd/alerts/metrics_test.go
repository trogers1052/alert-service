package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

// TestPromRecorder exercises every metrics.Recorder method on the Prometheus
// backing implementation. These must not panic and must register/observe.
func TestPromRecorder(t *testing.T) {
	r := newPromRecorder()
	r.IncAlertSent("BUY", "sent")
	r.IncKafkaConsumed("trading.decisions")
	r.IncCooldownSkipped()
	r.IncQuietHoursSkipped()
	r.ObserveTelegramDuration(0.123)
	r.IncTelegramErrors()
	r.IncFeedbackReceived("traded")
	r.IncFeedbackPostErrors()
	r.SetCooldownEntries(5)
	r.IncDeadLetters()
}

func TestStartMetricsServer(t *testing.T) {
	port := freePort(t)
	t.Setenv("METRICS_PORT", port)

	startMetricsServer()

	url := "http://127.0.0.1:" + port + "/metrics"
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("metrics endpoint never became ready")
}

func TestStartMetricsServer_DefaultPort(t *testing.T) {
	// METRICS_PORT unset → defaults to 9094. Bind may fail in CI; we only assert
	// the default branch runs without panicking.
	t.Setenv("METRICS_PORT", "")
	startMetricsServer()
	time.Sleep(50 * time.Millisecond)
}
