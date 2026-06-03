// Package metrics defines the interface for application-level metrics recording.
// Internal packages (service, kafka, client) depend on this interface rather than
// importing Prometheus directly, keeping the Prometheus wiring in cmd/alerts.
package metrics

// Recorder is the interface that internal packages use to record metrics.
// A Prometheus implementation is injected from cmd/alerts; tests and headless
// runs can use the Nop implementation.
type Recorder interface {
	// IncAlertSent increments the alerts_sent_total counter.
	IncAlertSent(signalType, status string)

	// IncKafkaConsumed increments the alerts_kafka_consumed_total counter.
	IncKafkaConsumed(topic string)

	// IncCooldownSkipped increments the alerts_cooldown_skipped_total counter.
	IncCooldownSkipped()

	// IncQuietHoursSkipped increments the alerts_quiet_hours_skipped_total counter.
	IncQuietHoursSkipped()

	// ObserveTelegramDuration records a Telegram API call duration.
	ObserveTelegramDuration(seconds float64)

	// IncTelegramErrors increments the alerts_telegram_errors_total counter.
	IncTelegramErrors()

	// IncFeedbackReceived increments the alerts_feedback_received_total counter.
	IncFeedbackReceived(action string)

	// IncFeedbackPostErrors increments the alerts_feedback_post_errors_total counter.
	IncFeedbackPostErrors()

	// SetCooldownEntries sets the alerts_cooldown_entries gauge.
	SetCooldownEntries(n float64)

	// IncDeadLetters increments the alerts_dead_letters_total counter.
	IncDeadLetters()
}

// Nop is a no-op implementation of Recorder. It is the default when no
// Prometheus metrics are wired (e.g. in tests).
type Nop struct{}

func (Nop) IncAlertSent(string, string)     {}
func (Nop) IncKafkaConsumed(string)         {}
func (Nop) IncCooldownSkipped()             {}
func (Nop) IncQuietHoursSkipped()           {}
func (Nop) ObserveTelegramDuration(float64) {}
func (Nop) IncTelegramErrors()              {}
func (Nop) IncFeedbackReceived(string)      {}
func (Nop) IncFeedbackPostErrors()          {}
func (Nop) SetCooldownEntries(float64)      {}
func (Nop) IncDeadLetters()                 {}
