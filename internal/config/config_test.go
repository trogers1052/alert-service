package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper to set multiple env vars and return a cleanup function
func setEnvs(t *testing.T, vars map[string]string) {
	t.Helper()
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

// requiredEnvs returns the minimum env vars needed for Load() to succeed
func requiredEnvs() map[string]string {
	return map[string]string{
		"TELEGRAM_BOT_TOKEN": "test-bot-token",
		"TELEGRAM_CHAT_ID":   "12345",
	}
}

// ---------------------------------------------------------------------------
// Load — happy path
// ---------------------------------------------------------------------------

func TestLoad_WithRequiredEnvs(t *testing.T) {
	setEnvs(t, requiredEnvs())

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "test-bot-token", cfg.TelegramBotToken)
	assert.Equal(t, int64(12345), cfg.TelegramChatID)
}

func TestLoad_DefaultValues(t *testing.T) {
	setEnvs(t, requiredEnvs())

	cfg, err := Load()
	require.NoError(t, err)

	// Kafka defaults
	assert.Equal(t, []string{"localhost:19092"}, cfg.KafkaBrokers)
	assert.Equal(t, "alert-service", cfg.KafkaConsumerGroup)
	assert.Equal(t, "trading.decisions", cfg.KafkaDecisionTopic)
	assert.Equal(t, "trading.rankings", cfg.KafkaRankingTopic)

	// Alert defaults
	assert.InDelta(t, 0.6, cfg.MinConfidence, 0.001)
	assert.True(t, cfg.AlertOnBuy)
	assert.True(t, cfg.AlertOnSell)
	assert.False(t, cfg.AlertOnWatch)
	assert.True(t, cfg.AlertOnRankings)
	assert.Equal(t, 5, cfg.RankingsTopN)
	assert.Equal(t, 30, cfg.CooldownMinutes)
	assert.Equal(t, 5, cfg.ScaleInCooldownMinutes)
	assert.Equal(t, 60, cfg.RankingCooldownMinutes)

	// Quiet hours defaults
	assert.Equal(t, 22, cfg.QuietHoursStart)
	assert.Equal(t, 7, cfg.QuietHoursEnd)
	assert.False(t, cfg.EnableQuietHours)
	assert.Equal(t, "America/New_York", cfg.QuietHoursTimezone)

	// Stock-service default
	assert.Equal(t, "http://stock-service:8081", cfg.StockServiceURL)
}

func TestLoad_CustomValues(t *testing.T) {
	setEnvs(t, map[string]string{
		"TELEGRAM_BOT_TOKEN":        "my-token",
		"TELEGRAM_CHAT_ID":          "99999",
		"KAFKA_BROKERS":             "broker1:9092,broker2:9092",
		"KAFKA_CONSUMER_GROUP":      "custom-group",
		"KAFKA_DECISION_TOPIC":      "custom.decisions",
		"KAFKA_RANKING_TOPIC":       "custom.rankings",
		"MIN_CONFIDENCE":            "0.8",
		"ALERT_ON_BUY":              "false",
		"ALERT_ON_SELL":             "false",
		"ALERT_ON_WATCH":            "true",
		"ALERT_ON_RANKINGS":         "false",
		"RANKINGS_TOP_N":            "10",
		"COOLDOWN_MINUTES":          "60",
		"SCALE_IN_COOLDOWN_MINUTES": "2",
		"RANKING_COOLDOWN_MINUTES":  "120",
		"QUIET_HOURS_START":         "20",
		"QUIET_HOURS_END":           "8",
		"ENABLE_QUIET_HOURS":        "true",
		"QUIET_HOURS_TIMEZONE":      "America/Chicago",
		"STOCK_SERVICE_URL":         "http://localhost:8081",
	})

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, []string{"broker1:9092", "broker2:9092"}, cfg.KafkaBrokers)
	assert.Equal(t, "custom-group", cfg.KafkaConsumerGroup)
	assert.Equal(t, "custom.decisions", cfg.KafkaDecisionTopic)
	assert.Equal(t, "custom.rankings", cfg.KafkaRankingTopic)
	assert.InDelta(t, 0.8, cfg.MinConfidence, 0.001)
	assert.False(t, cfg.AlertOnBuy)
	assert.False(t, cfg.AlertOnSell)
	assert.True(t, cfg.AlertOnWatch)
	assert.False(t, cfg.AlertOnRankings)
	assert.Equal(t, 10, cfg.RankingsTopN)
	assert.Equal(t, 60, cfg.CooldownMinutes)
	assert.Equal(t, 2, cfg.ScaleInCooldownMinutes)
	assert.Equal(t, 120, cfg.RankingCooldownMinutes)
	assert.Equal(t, 20, cfg.QuietHoursStart)
	assert.Equal(t, 8, cfg.QuietHoursEnd)
	assert.True(t, cfg.EnableQuietHours)
	assert.Equal(t, "America/Chicago", cfg.QuietHoursTimezone)
	assert.Equal(t, "http://localhost:8081", cfg.StockServiceURL)
}

// ---------------------------------------------------------------------------
// Load — validation errors
// ---------------------------------------------------------------------------

func TestLoad_MissingBotToken(t *testing.T) {
	t.Setenv("TELEGRAM_CHAT_ID", "12345")
	// No TELEGRAM_BOT_TOKEN set

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TELEGRAM_BOT_TOKEN")
}

func TestLoad_MissingChatID(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	// No TELEGRAM_CHAT_ID set (defaults to 0)

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TELEGRAM_CHAT_ID")
}

func TestLoad_BothMissing(t *testing.T) {
	// Ensure neither is set
	os.Unsetenv("TELEGRAM_BOT_TOKEN")
	os.Unsetenv("TELEGRAM_CHAT_ID")

	_, err := Load()
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func TestGetEnv_Present(t *testing.T) {
	t.Setenv("TEST_KEY", "value")
	assert.Equal(t, "value", getEnv("TEST_KEY", "default"))
}

func TestGetEnv_Missing(t *testing.T) {
	os.Unsetenv("TEST_MISSING_KEY")
	assert.Equal(t, "default", getEnv("TEST_MISSING_KEY", "default"))
}

func TestGetEnvInt_Valid(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	assert.Equal(t, 42, getEnvInt("TEST_INT", 0))
}

func TestGetEnvInt_Invalid(t *testing.T) {
	t.Setenv("TEST_INT_BAD", "not-a-number")
	assert.Equal(t, 99, getEnvInt("TEST_INT_BAD", 99))
}

func TestGetEnvInt_Missing(t *testing.T) {
	os.Unsetenv("TEST_INT_MISSING")
	assert.Equal(t, 7, getEnvInt("TEST_INT_MISSING", 7))
}

func TestGetEnvInt64_Valid(t *testing.T) {
	t.Setenv("TEST_INT64", "9876543210")
	assert.Equal(t, int64(9876543210), getEnvInt64("TEST_INT64", 0))
}

func TestGetEnvInt64_Invalid(t *testing.T) {
	t.Setenv("TEST_INT64_BAD", "abc")
	assert.Equal(t, int64(5), getEnvInt64("TEST_INT64_BAD", 5))
}

func TestGetEnvFloat_Valid(t *testing.T) {
	t.Setenv("TEST_FLOAT", "0.75")
	assert.InDelta(t, 0.75, getEnvFloat("TEST_FLOAT", 0.0), 0.001)
}

func TestGetEnvFloat_Invalid(t *testing.T) {
	t.Setenv("TEST_FLOAT_BAD", "xyz")
	assert.InDelta(t, 0.6, getEnvFloat("TEST_FLOAT_BAD", 0.6), 0.001)
}

func TestGetEnvBool_True(t *testing.T) {
	t.Setenv("TEST_BOOL", "true")
	assert.True(t, getEnvBool("TEST_BOOL", false))
}

func TestGetEnvBool_False(t *testing.T) {
	t.Setenv("TEST_BOOL", "false")
	assert.False(t, getEnvBool("TEST_BOOL", true))
}

func TestGetEnvBool_Invalid(t *testing.T) {
	t.Setenv("TEST_BOOL_BAD", "maybe")
	assert.True(t, getEnvBool("TEST_BOOL_BAD", true))
}

func TestGetEnvBool_Missing(t *testing.T) {
	os.Unsetenv("TEST_BOOL_MISSING")
	assert.False(t, getEnvBool("TEST_BOOL_MISSING", false))
}
