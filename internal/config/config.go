package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all configuration for the alert service
type Config struct {
	// Kafka
	KafkaBrokers       []string
	KafkaConsumerGroup string
	KafkaDecisionTopic string // trading.decisions from decision-engine
	KafkaRankingTopic  string // trading.rankings from decision-engine

	// Telegram
	TelegramBotToken string
	TelegramChatID   int64

	// Alert settings
	MinConfidence     float64 // Minimum confidence to send alert
	AlertOnBuy        bool    // Send alerts for BUY signals
	AlertOnSell       bool    // Send alerts for SELL signals
	AlertOnWatch      bool    // Send alerts for WATCH signals
	AlertOnRankings   bool    // Send daily ranking summaries
	RankingsTopN      int     // Number of top stocks to include in ranking alerts
	CooldownMinutes   int     // Cooldown between alerts for same symbol
	QuietHoursStart   int     // Hour to start quiet hours (0-23)
	QuietHoursEnd     int     // Hour to end quiet hours (0-23)
	EnableQuietHours  bool    // Whether to enable quiet hours
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		// Kafka
		KafkaBrokers:       strings.Split(getEnv("KAFKA_BROKERS", "localhost:19092"), ","),
		KafkaConsumerGroup: getEnv("KAFKA_CONSUMER_GROUP", "alert-service"),
		KafkaDecisionTopic: getEnv("KAFKA_DECISION_TOPIC", "trading.decisions"),
		KafkaRankingTopic:  getEnv("KAFKA_RANKING_TOPIC", "trading.rankings"),

		// Telegram
		TelegramBotToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:   getEnvInt64("TELEGRAM_CHAT_ID", 0),

		// Alert settings
		MinConfidence:    getEnvFloat("MIN_CONFIDENCE", 0.6),
		AlertOnBuy:       getEnvBool("ALERT_ON_BUY", true),
		AlertOnSell:      getEnvBool("ALERT_ON_SELL", true),
		AlertOnWatch:     getEnvBool("ALERT_ON_WATCH", false),
		AlertOnRankings:  getEnvBool("ALERT_ON_RANKINGS", true),
		RankingsTopN:     getEnvInt("RANKINGS_TOP_N", 5),
		CooldownMinutes:  getEnvInt("COOLDOWN_MINUTES", 30),
		QuietHoursStart:  getEnvInt("QUIET_HOURS_START", 22), // 10 PM
		QuietHoursEnd:    getEnvInt("QUIET_HOURS_END", 7),    // 7 AM
		EnableQuietHours: getEnvBool("ENABLE_QUIET_HOURS", false),
	}

	// Validate required fields
	if cfg.TelegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	if cfg.TelegramChatID == 0 {
		return nil, fmt.Errorf("TELEGRAM_CHAT_ID is required")
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}
