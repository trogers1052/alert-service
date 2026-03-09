package main

import (
	"log"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/trogers1052/alert-service/internal/metrics"
)

// Application-level Prometheus metrics for the alert-service.

var (
	alertsSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "alerts_sent_total",
		Help: "Total alerts dispatched, by signal type and status.",
	}, []string{"signal_type", "status"})

	kafkaConsumed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "alerts_kafka_consumed_total",
		Help: "Total Kafka messages consumed, by topic.",
	}, []string{"topic"})

	cooldownSkipped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "alerts_cooldown_skipped_total",
		Help: "Total signals skipped due to cooldown.",
	})

	quietHoursSkipped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "alerts_quiet_hours_skipped_total",
		Help: "Total signals skipped during quiet hours.",
	})

	telegramDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "alerts_telegram_duration_seconds",
		Help:    "Histogram of Telegram API call latency in seconds.",
		Buckets: prometheus.DefBuckets,
	})

	telegramErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "alerts_telegram_errors_total",
		Help: "Total Telegram API failures.",
	})

	feedbackReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "alerts_feedback_received_total",
		Help: "Total feedback responses received, by action.",
	}, []string{"action"})

	feedbackPostErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "alerts_feedback_post_errors_total",
		Help: "Total stock-service feedback POST/PUT failures.",
	})

	cooldownEntries = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "alerts_cooldown_entries",
		Help: "Current number of active cooldown entries.",
	})

	deadLetters = promauto.NewCounter(prometheus.CounterOpts{
		Name: "alerts_dead_letters_total",
		Help: "Total messages written to dead letter queue.",
	})
)

// promRecorder implements metrics.Recorder backed by promauto counters.
type promRecorder struct{}

var _ metrics.Recorder = promRecorder{}

func (promRecorder) IncAlertSent(signalType, status string)  { alertsSent.WithLabelValues(signalType, status).Inc() }
func (promRecorder) IncKafkaConsumed(topic string)           { kafkaConsumed.WithLabelValues(topic).Inc() }
func (promRecorder) IncCooldownSkipped()                     { cooldownSkipped.Inc() }
func (promRecorder) IncQuietHoursSkipped()                   { quietHoursSkipped.Inc() }
func (promRecorder) ObserveTelegramDuration(seconds float64) { telegramDuration.Observe(seconds) }
func (promRecorder) IncTelegramErrors()                      { telegramErrors.Inc() }
func (promRecorder) IncFeedbackReceived(action string)       { feedbackReceived.WithLabelValues(action).Inc() }
func (promRecorder) IncFeedbackPostErrors()                  { feedbackPostErrors.Inc() }
func (promRecorder) SetCooldownEntries(n float64)            { cooldownEntries.Set(n) }
func (promRecorder) IncDeadLetters()                         { deadLetters.Inc() }

// newPromRecorder returns a metrics.Recorder backed by Prometheus.
func newPromRecorder() metrics.Recorder {
	return promRecorder{}
}

func startMetricsServer() {
	port := os.Getenv("METRICS_PORT")
	if port == "" {
		port = "9094"
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe(":"+port, metricsMux); err != nil {
			log.Printf("Metrics server error: %v", err)
		}
	}()
	log.Printf("Metrics server listening on :%s/metrics", port)
}
