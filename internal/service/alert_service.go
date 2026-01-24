package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/trogers1052/alert-service/internal/config"
	"github.com/trogers1052/alert-service/internal/models"
	"github.com/trogers1052/alert-service/internal/telegram"
)

// AlertService handles alert logic and message formatting
type AlertService struct {
	config         *config.Config
	telegramClient *telegram.Client
	cooldowns      map[string]time.Time // symbol -> last alert time
	cooldownMu     sync.RWMutex
}

// NewAlertService creates a new alert service
func NewAlertService(cfg *config.Config, telegramClient *telegram.Client) *AlertService {
	return &AlertService{
		config:         cfg,
		telegramClient: telegramClient,
		cooldowns:      make(map[string]time.Time),
	}
}

// HandleDecisionEvent processes a decision event and sends alerts if appropriate
func (s *AlertService) HandleDecisionEvent(ctx context.Context, event interface{}) error {
	decision, ok := event.(*models.DecisionEvent)
	if !ok {
		return fmt.Errorf("invalid event type for decision handler")
	}

	data := decision.Data

	// Check if we should alert for this signal type
	if !s.shouldAlertForSignal(data.Signal) {
		log.Printf("Skipping alert for %s %s signal (not configured)", data.Symbol, data.Signal)
		return nil
	}

	// Check minimum confidence threshold
	if data.Confidence < s.config.MinConfidence {
		log.Printf("Skipping alert for %s: confidence %.2f below threshold %.2f",
			data.Symbol, data.Confidence, s.config.MinConfidence)
		return nil
	}

	// Check cooldown
	if !s.checkCooldown(data.Symbol) {
		log.Printf("Skipping alert for %s: in cooldown period", data.Symbol)
		return nil
	}

	// Check quiet hours
	if s.isQuietHours() {
		log.Printf("Skipping alert for %s: quiet hours active", data.Symbol)
		return nil
	}

	// Format and send the message
	message := s.formatDecisionMessage(decision)
	if err := s.telegramClient.SendMessage(ctx, message); err != nil {
		return fmt.Errorf("failed to send telegram message: %w", err)
	}

	// Update cooldown
	s.setCooldown(data.Symbol)

	log.Printf("Sent alert for %s %s signal (confidence: %.2f)", data.Symbol, data.Signal, data.Confidence)
	return nil
}

// HandleRankingEvent processes a ranking event and sends alerts if appropriate
func (s *AlertService) HandleRankingEvent(ctx context.Context, event interface{}) error {
	ranking, ok := event.(*models.RankingEvent)
	if !ok {
		return fmt.Errorf("invalid event type for ranking handler")
	}

	// Check if ranking alerts are enabled
	if !s.config.AlertOnRankings {
		return nil
	}

	// Check quiet hours
	if s.isQuietHours() {
		log.Printf("Skipping ranking alert: quiet hours active")
		return nil
	}

	// Format and send the message
	message := s.formatRankingMessage(ranking)
	if err := s.telegramClient.SendMessage(ctx, message); err != nil {
		return fmt.Errorf("failed to send telegram ranking message: %w", err)
	}

	log.Printf("Sent ranking alert for %s signals (%d symbols)",
		ranking.Data.SignalType, len(ranking.Data.Rankings))
	return nil
}

// shouldAlertForSignal checks if alerts are enabled for a signal type
func (s *AlertService) shouldAlertForSignal(signal string) bool {
	switch signal {
	case models.SignalBuy:
		return s.config.AlertOnBuy
	case models.SignalSell:
		return s.config.AlertOnSell
	case models.SignalWatch:
		return s.config.AlertOnWatch
	default:
		return false
	}
}

// checkCooldown returns true if we can send an alert for this symbol
func (s *AlertService) checkCooldown(symbol string) bool {
	s.cooldownMu.RLock()
	lastAlert, exists := s.cooldowns[symbol]
	s.cooldownMu.RUnlock()

	if !exists {
		return true
	}

	cooldownDuration := time.Duration(s.config.CooldownMinutes) * time.Minute
	return time.Since(lastAlert) >= cooldownDuration
}

// setCooldown updates the cooldown time for a symbol
func (s *AlertService) setCooldown(symbol string) {
	s.cooldownMu.Lock()
	s.cooldowns[symbol] = time.Now()
	s.cooldownMu.Unlock()
}

// isQuietHours checks if current time is within quiet hours
func (s *AlertService) isQuietHours() bool {
	if !s.config.EnableQuietHours {
		return false
	}

	now := time.Now()
	hour := now.Hour()

	start := s.config.QuietHoursStart
	end := s.config.QuietHoursEnd

	// Handle overnight quiet hours (e.g., 22:00 to 07:00)
	if start > end {
		return hour >= start || hour < end
	}

	// Same-day quiet hours (e.g., 13:00 to 14:00)
	return hour >= start && hour < end
}

// formatDecisionMessage formats a decision event into a Telegram message
func (s *AlertService) formatDecisionMessage(event *models.DecisionEvent) string {
	data := event.Data

	// Signal emoji
	var emoji string
	switch data.Signal {
	case models.SignalBuy:
		emoji = "🟢"
	case models.SignalSell:
		emoji = "🔴"
	case models.SignalWatch:
		emoji = "👀"
	}

	// Confidence bar
	confidenceBar := s.formatConfidenceBar(data.Confidence)

	var sb strings.Builder

	// Header
	sb.WriteString(fmt.Sprintf("%s <b>%s Signal: %s</b>\n\n", emoji, data.Signal, data.Symbol))

	// Confidence
	sb.WriteString(fmt.Sprintf("📊 Confidence: %.0f%% %s\n\n", data.Confidence*100, confidenceBar))

	// Primary reasoning
	sb.WriteString(fmt.Sprintf("💡 <b>Reason:</b>\n%s\n\n", data.PrimaryReasoning))

	// Rules triggered
	if len(data.RulesTriggered) > 0 {
		sb.WriteString("📋 <b>Rules Triggered:</b>\n")
		for _, rule := range data.RulesTriggered {
			sb.WriteString(fmt.Sprintf("  • %s (%.0f%%)\n", rule.RuleName, rule.Confidence*100))
		}
		sb.WriteString("\n")
	}

	// Key indicators
	if len(data.IndicatorsSnapshot) > 0 {
		sb.WriteString("📈 <b>Key Indicators:</b>\n")
		for name, value := range data.IndicatorsSnapshot {
			sb.WriteString(fmt.Sprintf("  • %s: %.2f\n", name, value))
		}
		sb.WriteString("\n")
	}

	// Timestamp
	sb.WriteString(fmt.Sprintf("🕐 %s", event.Timestamp.Format("2006-01-02 15:04:05 MST")))

	return sb.String()
}

// formatRankingMessage formats a ranking event into a Telegram message
func (s *AlertService) formatRankingMessage(event *models.RankingEvent) string {
	data := event.Data

	// Signal emoji
	var emoji string
	switch data.SignalType {
	case models.SignalBuy:
		emoji = "🟢"
	case models.SignalSell:
		emoji = "🔴"
	}

	var sb strings.Builder

	// Header
	sb.WriteString(fmt.Sprintf("%s <b>%s Rankings Update</b>\n", emoji, data.SignalType))
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", data.Timestamp.Format("2006-01-02 15:04")))

	// Show top N rankings
	count := s.config.RankingsTopN
	if count > len(data.Rankings) {
		count = len(data.Rankings)
	}

	sb.WriteString(fmt.Sprintf("<b>Top %d %s Candidates:</b>\n\n", count, data.SignalType))

	for i := 0; i < count; i++ {
		r := data.Rankings[i]
		medal := ""
		switch i {
		case 0:
			medal = "🥇"
		case 1:
			medal = "🥈"
		case 2:
			medal = "🥉"
		default:
			medal = fmt.Sprintf("%d.", i+1)
		}

		sb.WriteString(fmt.Sprintf("%s <b>%s</b> - Score: %.2f (%.0f%% confidence)\n",
			medal, r.Symbol, r.Score, r.Confidence*100))

		if r.Reasoning != "" {
			// Truncate long reasoning
			reasoning := r.Reasoning
			if len(reasoning) > 100 {
				reasoning = reasoning[:97] + "..."
			}
			sb.WriteString(fmt.Sprintf("    └ %s\n", reasoning))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("📊 Total symbols analyzed: %d", data.TotalSymbols))

	return sb.String()
}

// formatConfidenceBar creates a visual confidence bar
func (s *AlertService) formatConfidenceBar(confidence float64) string {
	filled := int(confidence * 10)
	empty := 10 - filled

	return strings.Repeat("█", filled) + strings.Repeat("░", empty)
}
