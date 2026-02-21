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

	rankingCooldowns   map[string]time.Time // signal type -> last ranking alert time
	rankingCooldownMu  sync.RWMutex
}

// NewAlertService creates a new alert service
func NewAlertService(cfg *config.Config, telegramClient *telegram.Client) *AlertService {
	s := &AlertService{
		config:           cfg,
		telegramClient:   telegramClient,
		cooldowns:        make(map[string]time.Time),
		rankingCooldowns: make(map[string]time.Time),
	}
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			s.cleanupExpiredCooldowns()
		}
	}()
	return s
}

func (s *AlertService) cleanupExpiredCooldowns() {
	cutoff := time.Now().Add(-time.Duration(s.config.CooldownMinutes*2) * time.Minute)

	s.cooldownMu.Lock()
	for key, t := range s.cooldowns {
		if t.Before(cutoff) {
			delete(s.cooldowns, key)
		}
	}
	s.cooldownMu.Unlock()

	s.rankingCooldownMu.Lock()
	for key, t := range s.rankingCooldowns {
		if t.Before(cutoff) {
			delete(s.rankingCooldowns, key)
		}
	}
	s.rankingCooldownMu.Unlock()
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

	// Check cooldown for this signal type
	if !s.checkRankingCooldown(ranking.Data.SignalType) {
		log.Printf("Skipping ranking alert for %s: in cooldown period", ranking.Data.SignalType)
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

	// Update cooldown
	s.setRankingCooldown(ranking.Data.SignalType)

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

// checkRankingCooldown returns true if we can send a ranking alert for this signal type
func (s *AlertService) checkRankingCooldown(signalType string) bool {
	s.rankingCooldownMu.RLock()
	lastAlert, exists := s.rankingCooldowns[signalType]
	s.rankingCooldownMu.RUnlock()

	if !exists {
		return true
	}

	cooldownDuration := time.Duration(s.config.RankingCooldownMinutes) * time.Minute
	return time.Since(lastAlert) >= cooldownDuration
}

// setRankingCooldown updates the cooldown time for a ranking signal type
func (s *AlertService) setRankingCooldown(signalType string) {
	s.rankingCooldownMu.Lock()
	s.rankingCooldowns[signalType] = time.Now()
	s.rankingCooldownMu.Unlock()
}

// isQuietHours checks if current time is within quiet hours.
//
// The comparison is performed in the configured IANA timezone
// (QUIET_HOURS_TIMEZONE, default "America/New_York") so that the quiet window
// is always relative to the trader's local time, not the Pi server clock.
// If the timezone string is invalid the function falls back to UTC and logs a
// warning once — it never silently sends alerts during intended quiet hours.
func (s *AlertService) isQuietHours() bool {
	if !s.config.EnableQuietHours {
		return false
	}

	loc, err := time.LoadLocation(s.config.QuietHoursTimezone)
	if err != nil {
		log.Printf("WARNING: invalid QUIET_HOURS_TIMEZONE %q: %v — falling back to UTC", s.config.QuietHoursTimezone, err)
		loc = time.UTC
	}

	hour := time.Now().In(loc).Hour()

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

	// Check if this is a scale-in (average down) signal
	isScaleIn := s.isScaleInSignal(&data)

	// Signal emoji
	var emoji string
	var signalLabel string
	switch data.Signal {
	case models.SignalBuy:
		if isScaleIn {
			emoji = "📈"
			signalLabel = "SCALE-IN"
		} else {
			emoji = "🟢"
			signalLabel = "BUY"
		}
	case models.SignalSell:
		emoji = "🔴"
		signalLabel = "SELL"
	case models.SignalWatch:
		emoji = "👀"
		signalLabel = "WATCH"
	}

	var sb strings.Builder

	// Header with setup type if trade plan available
	if data.TradePlan != nil {
		setupType := s.formatSetupType(data.TradePlan.SetupType)
		sb.WriteString(fmt.Sprintf("%s <b>%s Signal: %s</b>\n", emoji, signalLabel, data.Symbol))
		sb.WriteString(fmt.Sprintf("Setup: %s  |  Confidence: %.0f%%\n\n", setupType, data.Confidence*100))
	} else if isScaleIn {
		sb.WriteString(fmt.Sprintf("%s <b>%s Signal: %s</b>\n", emoji, signalLabel, data.Symbol))
		sb.WriteString("➕ <i>Adding to existing position</i>\n\n")
	} else {
		confidenceBar := s.formatConfidenceBar(data.Confidence)
		sb.WriteString(fmt.Sprintf("%s <b>%s Signal: %s</b>\n\n", emoji, signalLabel, data.Symbol))
		sb.WriteString(fmt.Sprintf("📊 Confidence: %.0f%% %s\n\n", data.Confidence*100, confidenceBar))
	}

	// Trade Plan details (if available)
	if data.TradePlan != nil {
		tp := data.TradePlan

		// Entry zone
		sb.WriteString(fmt.Sprintf("<b>Entry zone:</b>  $%.2f – $%.2f\n", tp.EntryZoneLow, tp.EntryZoneHigh))

		// Stop loss
		sb.WriteString(fmt.Sprintf("<b>Stop loss:</b>   $%.2f  (–%.1f%%)  [%s]\n",
			tp.StopPrice, tp.StopPct, s.formatStopMethod(tp.StopMethod)))

		// Targets with R:R
		sb.WriteString(fmt.Sprintf("<b>Target 1:</b>    $%.2f  (+%.1f%%)  [%.1f:1 R:R]\n",
			tp.Target1,
			((tp.Target1-tp.EntryPrice)/tp.EntryPrice)*100,
			tp.RiskRewardRatio))
		sb.WriteString(fmt.Sprintf("<b>Target 2:</b>    $%.2f  (+%.1f%%)  [3:1 R:R]\n\n",
			tp.Target2,
			((tp.Target2-tp.EntryPrice)/tp.EntryPrice)*100))

		// Position sizing
		sb.WriteString(fmt.Sprintf("<b>%d shares</b>  |  $%.2f risk  (%.1f%% of account)\n",
			tp.Shares, tp.DollarRisk, tp.RiskPct))
		sb.WriteString(fmt.Sprintf("Invalidation: $%.2f\n", tp.InvalidationPrice))

		// Valid until (parse and format nicely)
		sb.WriteString(fmt.Sprintf("Valid until: %s\n\n", s.formatValidUntil(tp.ValidUntil)))

		// R:R Warning if plan is not valid
		if !tp.PlanValid && tp.RRWarning != nil {
			sb.WriteString(fmt.Sprintf("⚠️ %s\n\n", *tp.RRWarning))
		}

		// Resistance note if present
		if tp.ResistanceNote != nil {
			sb.WriteString(fmt.Sprintf("📍 %s\n\n", *tp.ResistanceNote))
		}

		// Other warnings
		if len(tp.Warnings) > 0 {
			for _, w := range tp.Warnings {
				sb.WriteString(fmt.Sprintf("⚠️ %s\n", w))
			}
			sb.WriteString("\n")
		}
	} else {
		// Fallback to original format if no trade plan
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

		// Scale-in specific info
		if isScaleIn {
			sb.WriteString("⚠️ <b>Note:</b> This is an averaging down opportunity.\n")
			sb.WriteString("Review your position size before adding.\n\n")
		}
	}

	// Pre-trade checklist (BUY signals only)
	if data.Checklist != nil {
		sb.WriteString(s.formatChecklist(data.Checklist))
	}

	// Timestamp
	sb.WriteString(fmt.Sprintf("🕐 %s", event.Timestamp.Format("2006-01-02 15:04:05 MST")))

	return sb.String()
}

// formatChecklist renders the pre-trade checklist as a Telegram section
func (s *AlertService) formatChecklist(cl *models.Checklist) string {
	var sb strings.Builder

	// Status line
	var statusEmoji string
	switch cl.Status {
	case "GO":
		statusEmoji = "✅"
	case "BLOCKED":
		statusEmoji = "🚫"
	default:
		statusEmoji = "⚠️"
	}
	sb.WriteString(fmt.Sprintf("<b>Pre-Trade Checklist:</b> %s <b>%s</b>\n", statusEmoji, cl.Status))

	checkMark := func(ok bool) string {
		if ok {
			return "✅"
		}
		return "❌"
	}

	sb.WriteString(fmt.Sprintf("  %s Stop loss defined\n", checkMark(cl.StopLossDefined)))
	sb.WriteString(fmt.Sprintf("  %s Position sized ≤2%%  (%.1f%% risk)\n", checkMark(cl.PositionSizedCorrectly), cl.RiskPct))
	sb.WriteString(fmt.Sprintf("  %s R:R ≥ 2:1  (%.1f:1)\n", checkMark(cl.RRRatioAcceptable), cl.RRRatio))

	if cl.EarningsDate != nil {
		verified := ""
		if cl.EarningsVerified != nil && *cl.EarningsVerified {
			verified = " ✓confirmed"
		}
		daysAway := 0
		if cl.EarningsDaysAway != nil {
			daysAway = *cl.EarningsDaysAway
		}
		sb.WriteString(fmt.Sprintf("  %s No earnings within 5 days  (%s, %dd%s)\n",
			checkMark(cl.NoEarningsImminent), *cl.EarningsDate, daysAway, verified))
	} else {
		sb.WriteString(fmt.Sprintf("  %s No earnings within 5 days\n", checkMark(cl.NoEarningsImminent)))
	}

	sb.WriteString(fmt.Sprintf("  %s Regime compatible  (%s)\n\n", checkMark(cl.RegimeCompatible), cl.RegimeID))

	return sb.String()
}

// formatSetupType converts setup_type to human-readable format
func (s *AlertService) formatSetupType(setupType string) string {
	switch setupType {
	case "oversold_bounce":
		return "Oversold Bounce"
	case "pullback_to_support":
		return "Pullback to Support"
	case "breakout":
		return "Breakout"
	case "signal":
		return "Signal"
	default:
		return setupType
	}
}

// formatStopMethod converts stop method to display format
func (s *AlertService) formatStopMethod(method string) string {
	switch method {
	case "atr_2x":
		return "ATR×2"
	case "percentage_4pct":
		return "4%"
	case "percentage_10pct":
		return "10%"
	default:
		return method
	}
}

// formatValidUntil parses ISO timestamp and returns friendly format
func (s *AlertService) formatValidUntil(validUntil string) string {
	t, err := time.Parse(time.RFC3339, validUntil)
	if err != nil {
		return validUntil
	}

	now := time.Now()
	if t.Day() == now.Day() && t.Month() == now.Month() && t.Year() == now.Year() {
		return "EOD"
	}

	diff := t.Sub(now)
	if diff < 3*time.Hour {
		return fmt.Sprintf("%.0f hours", diff.Hours())
	}

	return t.Format("Jan 2 15:04")
}

// isScaleInSignal checks if this is an average down / scale-in signal
func (s *AlertService) isScaleInSignal(data *models.DecisionData) bool {
	// Check if "Average Down" rule triggered
	for _, rule := range data.RulesTriggered {
		if strings.Contains(strings.ToLower(rule.RuleName), "average down") {
			return true
		}
	}

	// Check reasoning for scale-in keywords
	reasoning := strings.ToLower(data.PrimaryReasoning)
	scaleInKeywords := []string{"average down", "scale-in", "scale in", "adding to position"}
	for _, keyword := range scaleInKeywords {
		if strings.Contains(reasoning, keyword) {
			return true
		}
	}

	return false
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
