package config

import (
	"fmt"

	"github.com/trogers1052/trading-go-commons/env"
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
	MinConfidence          float64 // Minimum confidence to send alert
	AlertOnBuy             bool    // Send alerts for BUY signals
	AlertOnSell            bool    // Send alerts for SELL signals
	AlertOnWatch           bool    // Send alerts for WATCH signals
	AlertOnRankings        bool    // Send daily ranking summaries
	RankingsTopN           int     // Number of top stocks to include in ranking alerts
	MinRRRatio             float64 // Minimum R:R ratio to send BUY alert (0 = disabled)
	CooldownMinutes        int     // Cooldown between alerts for same symbol
	ScaleInCooldownMinutes int     // Shorter cooldown for scale-in signals
	RankingCooldownMinutes int     // Cooldown between ranking alerts for same signal type
	QuietHoursStart        int     // Hour to start quiet hours (0-23), in QuietHoursTimezone
	QuietHoursEnd          int     // Hour to end quiet hours (0-23), in QuietHoursTimezone
	EnableQuietHours       bool    // Whether to enable quiet hours
	QuietHoursTimezone     string  // IANA timezone name for quiet hours (e.g. "America/New_York")

	// Symbol muting — context/indicator symbols that should never trigger alerts
	MutedSymbols map[string]bool

	// Stock-service — used for persisting feedback entries to PostgreSQL
	StockServiceURL    string
	StockServiceAPIKey string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		// Kafka
		KafkaBrokers:       env.StringSliceRaw("KAFKA_BROKERS", []string{"localhost:19092"}, ","),
		KafkaConsumerGroup: env.String("KAFKA_CONSUMER_GROUP", "alert-service"),
		KafkaDecisionTopic: env.String("KAFKA_DECISION_TOPIC", "trading.decisions"),
		KafkaRankingTopic:  env.String("KAFKA_RANKING_TOPIC", "trading.rankings"),

		// Telegram
		TelegramBotToken: env.String("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:   env.Int64("TELEGRAM_CHAT_ID", 0),

		// Alert settings
		MinConfidence:          env.Float("MIN_CONFIDENCE", 0.6),
		AlertOnBuy:             env.Bool("ALERT_ON_BUY", true),
		AlertOnSell:            env.Bool("ALERT_ON_SELL", true),
		AlertOnWatch:           env.Bool("ALERT_ON_WATCH", false),
		AlertOnRankings:        env.Bool("ALERT_ON_RANKINGS", true),
		RankingsTopN:           env.Int("RANKINGS_TOP_N", 5),
		MinRRRatio:             env.Float("MIN_RR_RATIO", 2.0),
		CooldownMinutes:        env.Int("COOLDOWN_MINUTES", 30),
		ScaleInCooldownMinutes: env.Int("SCALE_IN_COOLDOWN_MINUTES", 5),
		RankingCooldownMinutes: env.Int("RANKING_COOLDOWN_MINUTES", 60),
		QuietHoursStart:        env.Int("QUIET_HOURS_START", 22), // 10 PM
		QuietHoursEnd:          env.Int("QUIET_HOURS_END", 7),    // 7 AM
		EnableQuietHours:       env.Bool("ENABLE_QUIET_HOURS", false),
		QuietHoursTimezone:     env.String("QUIET_HOURS_TIMEZONE", "America/New_York"),

		// Symbol muting
		MutedSymbols: env.StringSet("MUTED_SYMBOLS"),

		// Stock-service
		StockServiceURL:    env.String("STOCK_SERVICE_URL", "http://stock-service:8081"),
		StockServiceAPIKey: env.String("STOCK_SERVICE_API_KEY", ""),
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
