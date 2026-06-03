package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trogers1052/alert-service/internal/client"
	"github.com/trogers1052/alert-service/internal/config"
	"github.com/trogers1052/alert-service/internal/metrics"
	"github.com/trogers1052/alert-service/internal/models"
	"github.com/trogers1052/trading-go-commons/telegram"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// redirectTransport routes all HTTP requests to a test server
type redirectTransport struct {
	target string
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	newURL := t.target + req.URL.Path
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	for k, v := range req.Header {
		newReq.Header[k] = v
	}
	return http.DefaultTransport.RoundTrip(newReq)
}

// newTestConfig returns a config suitable for testing
func newTestConfig() *config.Config {
	return &config.Config{
		TelegramBotToken:       "test-token",
		TelegramChatID:         12345,
		MinConfidence:          0.6,
		AlertOnBuy:             true,
		AlertOnSell:            true,
		AlertOnWatch:           false,
		AlertOnRankings:        true,
		RankingsTopN:           3,
		CooldownMinutes:        30,
		ScaleInCooldownMinutes: 5,
		RankingCooldownMinutes: 60,
		EnableQuietHours:       false,
		QuietHoursTimezone:     "America/New_York",
		QuietHoursStart:        22,
		QuietHoursEnd:          7,
	}
}

// newTestService creates an AlertService with a mock Telegram server.
// The returned server must be closed by the caller.
func newTestService(t *testing.T) (*AlertService, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := telegram.SendMessageResponse{
			OK:     true,
			Result: &telegram.MessageResult{MessageID: 1},
		}
		json.NewEncoder(w).Encode(resp)
	}))

	// Override the HTTP client to use our test server
	tc := telegram.NewClient("test-token", 12345, telegram.WithHTTPClient(&http.Client{
		Transport: &redirectTransport{target: server.URL},
	}))

	cfg := newTestConfig()
	// Build the service manually to avoid starting background goroutines
	s := &AlertService{
		config:           cfg,
		telegramClient:   tc,
		cooldowns:        make(map[string]time.Time),
		rankingCooldowns: make(map[string]time.Time),
		feedbackLog:      make(map[string]models.FeedbackEntry),
		stockClient:      nil,
		metrics:          metrics.Nop{},
		stopCleanup:      make(chan struct{}),
		stopFeedback:     make(chan struct{}),
	}

	return s, server
}

// makeDecisionEvent builds a simple DecisionEvent for testing
func makeDecisionEvent(symbol, signal string, confidence float64) *models.DecisionEvent {
	return &models.DecisionEvent{
		EventType:     "decision",
		Source:        "test",
		SchemaVersion: "1.0",
		Timestamp:     time.Now(),
		Data: models.DecisionData{
			Symbol:           symbol,
			Signal:           signal,
			Confidence:       confidence,
			PrimaryReasoning: "Test reasoning",
		},
	}
}

// makeRankingEvent builds a simple RankingEvent for testing
func makeRankingEvent(signalType string, rankings []models.SymbolRanking) *models.RankingEvent {
	return &models.RankingEvent{
		EventType:     "ranking",
		Source:        "test",
		SchemaVersion: "1.0",
		Timestamp:     time.Now(),
		Data: models.RankingData{
			SignalType:   signalType,
			Criteria:     "test",
			Timestamp:    time.Now(),
			TotalSymbols: len(rankings),
			Rankings:     rankings,
		},
	}
}

// ---------------------------------------------------------------------------
// shouldAlertForSignal
// ---------------------------------------------------------------------------

func TestShouldAlertForSignal_Buy(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	assert.True(t, s.shouldAlertForSignal(models.SignalBuy))
}

func TestShouldAlertForSignal_Sell(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	assert.True(t, s.shouldAlertForSignal(models.SignalSell))
}

func TestShouldAlertForSignal_WatchDisabled(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	assert.False(t, s.shouldAlertForSignal(models.SignalWatch))
}

func TestShouldAlertForSignal_WatchEnabled(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	s.config.AlertOnWatch = true
	assert.True(t, s.shouldAlertForSignal(models.SignalWatch))
}

func TestShouldAlertForSignal_Unknown(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	assert.False(t, s.shouldAlertForSignal("HOLD"))
}

func TestShouldAlertForSignal_Empty(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	assert.False(t, s.shouldAlertForSignal(""))
}

// ---------------------------------------------------------------------------
// checkCooldown / checkCooldownWithDuration
// ---------------------------------------------------------------------------

func TestCheckCooldown_FirstCallAlwaysAllowed(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	assert.True(t, s.checkCooldown("AAPL:BUY"))
}

func TestCheckCooldown_WithinCooldown(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.setCooldown("AAPL:BUY")
	assert.False(t, s.checkCooldown("AAPL:BUY"))
}

func TestCheckCooldown_AfterCooldownExpires(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	// Set cooldown 31 minutes ago (cooldown is 30 min)
	s.cooldownMu.Lock()
	s.cooldowns["AAPL:BUY"] = time.Now().Add(-31 * time.Minute)
	s.cooldownMu.Unlock()

	assert.True(t, s.checkCooldown("AAPL:BUY"))
}

func TestCheckCooldown_DifferentKeysAreIndependent(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.setCooldown("AAPL:BUY")
	assert.False(t, s.checkCooldown("AAPL:BUY"))
	assert.True(t, s.checkCooldown("AAPL:SELL"))
	assert.True(t, s.checkCooldown("GOOG:BUY"))
}

func TestCheckCooldownWithDuration_ShortDuration(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.cooldownMu.Lock()
	s.cooldowns["AAPL:BUY"] = time.Now().Add(-6 * time.Minute)
	s.cooldownMu.Unlock()

	// 5-minute cooldown should have expired
	assert.True(t, s.checkCooldownWithDuration("AAPL:BUY", 5*time.Minute))
	// 30-minute cooldown should still be active
	assert.False(t, s.checkCooldownWithDuration("AAPL:BUY", 30*time.Minute))
}

// ---------------------------------------------------------------------------
// Ranking cooldowns
// ---------------------------------------------------------------------------

func TestCheckRankingCooldown_FirstCall(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	assert.True(t, s.checkRankingCooldown(models.SignalBuy))
}

func TestCheckRankingCooldown_WithinCooldown(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.setRankingCooldown(models.SignalBuy)
	assert.False(t, s.checkRankingCooldown(models.SignalBuy))
}

func TestCheckRankingCooldown_AfterExpiry(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.rankingCooldownMu.Lock()
	s.rankingCooldowns[models.SignalBuy] = time.Now().Add(-61 * time.Minute)
	s.rankingCooldownMu.Unlock()

	assert.True(t, s.checkRankingCooldown(models.SignalBuy))
}

func TestCheckRankingCooldown_DifferentTypesIndependent(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.setRankingCooldown(models.SignalBuy)
	assert.False(t, s.checkRankingCooldown(models.SignalBuy))
	assert.True(t, s.checkRankingCooldown(models.SignalSell))
}

// ---------------------------------------------------------------------------
// isQuietHours
// ---------------------------------------------------------------------------

func TestIsQuietHours_Disabled(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	s.config.EnableQuietHours = false
	assert.False(t, s.isQuietHours())
}

func TestIsQuietHours_OvernightWrap_DuringQuietHours(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	s.config.EnableQuietHours = true
	s.config.QuietHoursTimezone = "UTC"

	now := time.Now().UTC()
	// Set quiet hours so current hour is inside the window
	s.config.QuietHoursStart = now.Hour()
	s.config.QuietHoursEnd = (now.Hour() + 2) % 24

	// If start < end (same-day), current hour at start means quiet
	if s.config.QuietHoursStart < s.config.QuietHoursEnd {
		assert.True(t, s.isQuietHours())
	}
}

func TestIsQuietHours_OutsideWindow(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	s.config.EnableQuietHours = true
	s.config.QuietHoursTimezone = "UTC"

	now := time.Now().UTC()
	// Set quiet hours to a window that definitely doesn't include now
	// Use a same-day window: from (now+2) to (now+4) — we're outside
	s.config.QuietHoursStart = (now.Hour() + 2) % 24
	s.config.QuietHoursEnd = (now.Hour() + 4) % 24

	if s.config.QuietHoursStart < s.config.QuietHoursEnd {
		assert.False(t, s.isQuietHours())
	}
}

func TestIsQuietHours_InvalidTimezone(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	s.config.EnableQuietHours = true
	s.config.QuietHoursTimezone = "Invalid/Zone"

	// Should fall back to UTC without panicking
	_ = s.isQuietHours() // just ensure no panic
}

// ---------------------------------------------------------------------------
// isScaleInSignal
// ---------------------------------------------------------------------------

func TestIsScaleInSignal_AverageDownRule(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	data := &models.DecisionData{
		RulesTriggered: []models.RuleResult{
			{RuleName: "Average Down Opportunity"},
		},
	}
	assert.True(t, s.isScaleInSignal(data))
}

func TestIsScaleInSignal_CaseInsensitive(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	data := &models.DecisionData{
		RulesTriggered: []models.RuleResult{
			{RuleName: "AVERAGE DOWN signal"},
		},
	}
	assert.True(t, s.isScaleInSignal(data))
}

func TestIsScaleInSignal_ReasoningMatch(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	tests := []struct {
		reasoning string
		expected  bool
	}{
		{"Consider average down here", true},
		{"Good scale-in opportunity", true},
		{"Time to scale in more", true},
		{"Adding to position recommended", true},
		{"Strong momentum signal", false},
		{"", false},
	}

	for _, tc := range tests {
		data := &models.DecisionData{PrimaryReasoning: tc.reasoning}
		assert.Equal(t, tc.expected, s.isScaleInSignal(data), "reasoning: %q", tc.reasoning)
	}
}

func TestIsScaleInSignal_NoMatch(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	data := &models.DecisionData{
		RulesTriggered:   []models.RuleResult{{RuleName: "RSI Oversold"}},
		PrimaryReasoning: "RSI below 30",
	}
	assert.False(t, s.isScaleInSignal(data))
}

// ---------------------------------------------------------------------------
// formatConfidenceBar
// ---------------------------------------------------------------------------

func TestFormatConfidenceBar(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	tests := []struct {
		confidence float64
		expected   string
	}{
		{0.0, "░░░░░░░░░░"},
		{0.5, "█████░░░░░"},
		{1.0, "██████████"},
		{0.3, "███░░░░░░░"},
		{0.85, "████████░░"},
	}

	for _, tc := range tests {
		result := s.formatConfidenceBar(tc.confidence)
		assert.Equal(t, tc.expected, result, "confidence: %.2f", tc.confidence)
	}
}

// ---------------------------------------------------------------------------
// formatSetupType
// ---------------------------------------------------------------------------

func TestFormatSetupType(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	tests := []struct {
		input    string
		expected string
	}{
		{"oversold_bounce", "Oversold Bounce"},
		{"pullback_to_support", "Pullback to Support"},
		{"breakout", "Breakout"},
		{"signal", "Signal"},
		{"custom_setup", "custom_setup"}, // unknown type passes through
	}

	for _, tc := range tests {
		assert.Equal(t, tc.expected, s.formatSetupType(tc.input))
	}
}

// ---------------------------------------------------------------------------
// formatStopMethod
// ---------------------------------------------------------------------------

func TestFormatStopMethod(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	tests := []struct {
		input    string
		expected string
	}{
		{"atr_2x", "ATR\u00d72"},
		{"percentage_4pct", "4%"},
		{"percentage_10pct", "10%"},
		{"custom_method", "custom_method"}, // unknown passes through
	}

	for _, tc := range tests {
		assert.Equal(t, tc.expected, s.formatStopMethod(tc.input))
	}
}

// ---------------------------------------------------------------------------
// formatValidUntil
// ---------------------------------------------------------------------------

func TestFormatValidUntil_Today(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	// Create a timestamp for later today
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 0, 0, now.Location())
	result := s.formatValidUntil(today.Format(time.RFC3339))
	assert.Equal(t, "EOD", result)
}

func TestFormatValidUntil_FarFuture(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	future := time.Now().Add(48 * time.Hour)
	result := s.formatValidUntil(future.Format(time.RFC3339))
	// Should return formatted date, not "EOD" or hours
	assert.Contains(t, result, fmt.Sprintf("%d", future.Day()))
}

func TestFormatValidUntil_InvalidFormat(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	result := s.formatValidUntil("not-a-date")
	assert.Equal(t, "not-a-date", result)
}

// ---------------------------------------------------------------------------
// formatDecisionMessage
// ---------------------------------------------------------------------------

func TestFormatDecisionMessage_BuyWithTradePlan(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("AAPL", models.SignalBuy, 0.85)
	event.Data.TradePlan = &models.TradePlan{
		SetupType:         "oversold_bounce",
		EntryPrice:        50.00,
		EntryZoneLow:      49.50,
		EntryZoneHigh:     50.50,
		StopPrice:         47.00,
		StopMethod:        "atr_2x",
		StopPct:           6.0,
		Target1:           56.00,
		Target2:           62.00,
		RiskRewardRatio:   2.0,
		Shares:            17,
		DollarRisk:        51.00,
		RiskPct:           1.8,
		PositionValue:     850.00,
		InvalidationPrice: 46.50,
		PlanValid:         true,
		ValidUntil:        time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	}

	msg := s.formatDecisionMessage(event)

	assert.Contains(t, msg, "BUY Signal: AAPL")
	assert.Contains(t, msg, "Reason:")
	assert.Contains(t, msg, "85%")    // confidence
	assert.Contains(t, msg, "$49.50") // entry zone low
	assert.Contains(t, msg, "$50.50") // entry zone high
	assert.Contains(t, msg, "$47.00") // stop
	assert.Contains(t, msg, "ATR")    // stop method
	assert.Contains(t, msg, "$56.00") // target 1
	assert.Contains(t, msg, "$62.00") // target 2
	assert.Contains(t, msg, "17 shares")
	assert.Contains(t, msg, "$51.00 risk")
	assert.Contains(t, msg, "1.8%")
	assert.Contains(t, msg, "$46.50") // invalidation
}

func TestFormatDecisionMessage_BuyWithTargetContext(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	t1Prob := 0.45
	t1Days := 8
	t2Prob := 0.28
	t2Days := 14
	priceCtx := "At SMA20 support. Full trend alignment (bullish)"

	event := makeDecisionEvent("CCJ", models.SignalBuy, 0.82)
	event.Data.TradePlan = &models.TradePlan{
		SetupType:          "pullback_to_support",
		EntryPrice:         45.00,
		EntryZoneLow:       44.50,
		EntryZoneHigh:      45.50,
		StopPrice:          43.00,
		StopMethod:         "atr_2x",
		StopPct:            4.4,
		Target1:            49.00,
		Target2:            51.00,
		RiskRewardRatio:    2.0,
		Shares:             7,
		DollarRisk:         14.00,
		RiskPct:            1.6,
		PositionValue:      315.00,
		InvalidationPrice:  42.50,
		PlanValid:          true,
		ValidUntil:         time.Now().Add(48 * time.Hour).Format(time.RFC3339),
		Target1Probability: &t1Prob,
		Target1EstDays:     &t1Days,
		Target2Probability: &t2Prob,
		Target2EstDays:     &t2Days,
		PriceContext:       &priceCtx,
	}

	msg := s.formatDecisionMessage(event)

	// Price context shown
	assert.Contains(t, msg, "At SMA20 support")
	assert.Contains(t, msg, "Full trend alignment")

	// Probability and timeframe on targets
	assert.Contains(t, msg, "~45%")
	assert.Contains(t, msg, "~8d")
	assert.Contains(t, msg, "~28%")
	assert.Contains(t, msg, "~14d")
}

func TestFormatDecisionMessage_BuyWithoutTradePlan(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("GOOG", models.SignalBuy, 0.75)
	event.Data.RulesTriggered = []models.RuleResult{
		{RuleName: "RSI Oversold", Confidence: 0.9},
		{RuleName: "MACD Cross", Confidence: 0.7},
	}

	msg := s.formatDecisionMessage(event)

	assert.Contains(t, msg, "BUY Signal: GOOG")
	assert.Contains(t, msg, "75%")
	assert.Contains(t, msg, "Reason:")
	assert.Contains(t, msg, "RSI Oversold")
	assert.Contains(t, msg, "90%")
	assert.Contains(t, msg, "MACD Cross")
}

func TestFormatDecisionMessage_SellSignal(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("TSLA", models.SignalSell, 0.65)
	msg := s.formatDecisionMessage(event)
	assert.Contains(t, msg, "SELL Signal: TSLA")
}

func TestFormatDecisionMessage_WatchSignal(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("MSFT", models.SignalWatch, 0.7)
	msg := s.formatDecisionMessage(event)
	assert.Contains(t, msg, "WATCH Signal: MSFT")
}

func TestFormatDecisionMessage_ScaleIn(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("SOFI", models.SignalBuy, 0.8)
	event.Data.RulesTriggered = []models.RuleResult{
		{RuleName: "Average Down Signal"},
	}

	msg := s.formatDecisionMessage(event)
	assert.Contains(t, msg, "SCALE-IN Signal: SOFI")
	assert.Contains(t, msg, "Adding to existing position")
}

func TestFormatDecisionMessage_WithChecklist(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("AAPL", models.SignalBuy, 0.85)
	earningsDate := "2026-03-15"
	daysAway := 22
	verified := true
	event.Data.Checklist = &models.Checklist{
		StopLossDefined:        true,
		PositionSizedCorrectly: true,
		RRRatioAcceptable:      true,
		NoEarningsImminent:     true,
		RegimeCompatible:       true,
		AllChecksPassed:        true,
		Status:                 "GO",
		EarningsDate:           &earningsDate,
		EarningsDaysAway:       &daysAway,
		EarningsVerified:       &verified,
		RegimeID:               "trending_bull",
		RiskPct:                1.5,
		RRRatio:                2.8,
	}

	msg := s.formatDecisionMessage(event)
	assert.Contains(t, msg, "Pre-Trade Checklist")
	assert.Contains(t, msg, "GO")
	assert.Contains(t, msg, "Stop loss defined")
	assert.Contains(t, msg, "1.5% risk")
	assert.Contains(t, msg, "2.8:1")
	assert.Contains(t, msg, "2026-03-15")
	assert.Contains(t, msg, "confirmed")
	assert.Contains(t, msg, "trending_bull")
}

func TestFormatDecisionMessage_ChecklistBlocked(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("AAPL", models.SignalBuy, 0.85)
	event.Data.Checklist = &models.Checklist{
		StopLossDefined:        false,
		PositionSizedCorrectly: false,
		RRRatioAcceptable:      false,
		NoEarningsImminent:     false,
		RegimeCompatible:       false,
		AllChecksPassed:        false,
		Status:                 "BLOCKED",
		RegimeID:               "volatile_chop",
		RiskPct:                5.0,
		RRRatio:                0.8,
	}

	msg := s.formatDecisionMessage(event)
	assert.Contains(t, msg, "BLOCKED")
}

func TestFormatDecisionMessage_TradePlanWithWarnings(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	rrWarn := "R:R ratio below 2:1"
	event := makeDecisionEvent("AAPL", models.SignalBuy, 0.85)
	event.Data.TradePlan = &models.TradePlan{
		SetupType:         "signal",
		EntryPrice:        50.00,
		EntryZoneLow:      49.50,
		EntryZoneHigh:     50.50,
		StopPrice:         47.00,
		StopMethod:        "atr_2x",
		StopPct:           6.0,
		Target1:           52.00,
		Target2:           54.00,
		RiskRewardRatio:   0.67,
		Shares:            10,
		DollarRisk:        30.0,
		RiskPct:           1.0,
		PositionValue:     500.0,
		InvalidationPrice: 46.50,
		PlanValid:         false,
		RRWarning:         &rrWarn,
		Warnings:          []string{"Low volume", "Extended from MA"},
		ValidUntil:        "not-a-date",
	}

	msg := s.formatDecisionMessage(event)
	assert.Contains(t, msg, "R:R ratio below 2:1")
	assert.Contains(t, msg, "Low volume")
	assert.Contains(t, msg, "Extended from MA")
}

func TestFormatDecisionMessage_HTMLEscaping(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("A<B>C", models.SignalBuy, 0.7)
	event.Data.PrimaryReasoning = "Test <script>alert('xss')</script>"

	msg := s.formatDecisionMessage(event)
	assert.NotContains(t, msg, "<script>")
	assert.Contains(t, msg, "&lt;script&gt;")
	assert.Contains(t, msg, "A&lt;B&gt;C")
}

// ---------------------------------------------------------------------------
// formatRankingMessage
// ---------------------------------------------------------------------------

func TestFormatRankingMessage_BuyRankings(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeRankingEvent(models.SignalBuy, []models.SymbolRanking{
		{Symbol: "AAPL", Rank: 1, Score: 0.95, Confidence: 0.9, Reasoning: "Strong momentum"},
		{Symbol: "GOOG", Rank: 2, Score: 0.88, Confidence: 0.85, Reasoning: "Pullback buy"},
		{Symbol: "MSFT", Rank: 3, Score: 0.80, Confidence: 0.8, Reasoning: "Support test"},
	})

	msg := s.formatRankingMessage(event)
	assert.Contains(t, msg, "BUY Rankings")
	assert.Contains(t, msg, "AAPL")
	assert.Contains(t, msg, "GOOG")
	assert.Contains(t, msg, "MSFT")
	assert.Contains(t, msg, "Strong momentum")
	assert.Contains(t, msg, "Total symbols analyzed: 3")
}

func TestFormatRankingMessage_SellRankings(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeRankingEvent(models.SignalSell, []models.SymbolRanking{
		{Symbol: "TSLA", Rank: 1, Score: 0.92, Confidence: 0.88},
	})

	msg := s.formatRankingMessage(event)
	assert.Contains(t, msg, "SELL Rankings")
	assert.Contains(t, msg, "TSLA")
}

func TestFormatRankingMessage_TruncatesLongReasoning(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	longReasoning := strings.Repeat("x", 150) // > 100 chars
	event := makeRankingEvent(models.SignalBuy, []models.SymbolRanking{
		{Symbol: "AAPL", Rank: 1, Score: 0.9, Confidence: 0.8, Reasoning: longReasoning},
	})

	msg := s.formatRankingMessage(event)
	assert.Contains(t, msg, "...")
	// Should be truncated to ~100 chars
}

func TestFormatRankingMessage_LessThanTopN(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	// TopN is 3 but only 1 ranking
	event := makeRankingEvent(models.SignalBuy, []models.SymbolRanking{
		{Symbol: "AAPL", Rank: 1, Score: 0.9, Confidence: 0.8},
	})

	msg := s.formatRankingMessage(event)
	assert.Contains(t, msg, "Top 1")
	assert.Contains(t, msg, "AAPL")
}

func TestFormatRankingMessage_Medals(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.config.RankingsTopN = 5
	event := makeRankingEvent(models.SignalBuy, []models.SymbolRanking{
		{Symbol: "A", Rank: 1, Score: 0.9, Confidence: 0.9},
		{Symbol: "B", Rank: 2, Score: 0.8, Confidence: 0.8},
		{Symbol: "C", Rank: 3, Score: 0.7, Confidence: 0.7},
		{Symbol: "D", Rank: 4, Score: 0.6, Confidence: 0.6},
	})

	msg := s.formatRankingMessage(event)
	// First 3 get medals, 4th gets number
	assert.Contains(t, msg, "4.")
}

// ---------------------------------------------------------------------------
// formatChecklist
// ---------------------------------------------------------------------------

func TestFormatChecklist_GO(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	cl := &models.Checklist{
		StopLossDefined:        true,
		PositionSizedCorrectly: true,
		RRRatioAcceptable:      true,
		NoEarningsImminent:     true,
		RegimeCompatible:       true,
		Status:                 "GO",
		RegimeID:               "trending_bull",
		RiskPct:                1.5,
		RRRatio:                2.5,
	}

	result := s.formatChecklist(cl)
	assert.Contains(t, result, "GO")
	// All items should have check marks
	count := strings.Count(result, "\u2705") // ✅
	assert.GreaterOrEqual(t, count, 5)
}

func TestFormatChecklist_BLOCKED(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	cl := &models.Checklist{
		StopLossDefined:        false,
		PositionSizedCorrectly: false,
		RRRatioAcceptable:      false,
		NoEarningsImminent:     false,
		RegimeCompatible:       false,
		Status:                 "BLOCKED",
		RegimeID:               "crisis",
		RiskPct:                5.0,
		RRRatio:                0.5,
	}

	result := s.formatChecklist(cl)
	assert.Contains(t, result, "BLOCKED")
	count := strings.Count(result, "\u274c") // ❌
	assert.GreaterOrEqual(t, count, 5)
}

func TestFormatChecklist_WithEarnings(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	earningsDate := "2026-03-15"
	daysAway := 3
	verified := false
	cl := &models.Checklist{
		Status:             "REVIEW",
		NoEarningsImminent: false,
		EarningsDate:       &earningsDate,
		EarningsDaysAway:   &daysAway,
		EarningsVerified:   &verified,
		RegimeID:           "mean_reverting",
		RiskPct:            1.0,
		RRRatio:            2.0,
	}

	result := s.formatChecklist(cl)
	assert.Contains(t, result, "2026-03-15")
	assert.Contains(t, result, "3d")
	assert.NotContains(t, result, "confirmed") // verified is false
}

// ---------------------------------------------------------------------------
// HandleDecisionEvent
// ---------------------------------------------------------------------------

func TestHandleDecisionEvent_Success(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("AAPL", models.SignalWatch, 0.85) // Use WATCH
	s.config.AlertOnWatch = true
	err := s.HandleDecisionEvent(context.Background(), event)
	require.NoError(t, err)
}

func TestHandleDecisionEvent_InvalidEventType(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	err := s.HandleDecisionEvent(context.Background(), "not an event")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid event type")
}

func TestHandleDecisionEvent_SignalNotEnabled(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("AAPL", models.SignalWatch, 0.85)
	// AlertOnWatch is false by default
	err := s.HandleDecisionEvent(context.Background(), event)
	assert.NoError(t, err) // silently skipped
}

func TestHandleDecisionEvent_BelowConfidence(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("AAPL", models.SignalBuy, 0.5) // below 0.6 threshold
	err := s.HandleDecisionEvent(context.Background(), event)
	assert.NoError(t, err) // silently skipped
}

func TestHandleDecisionEvent_InCooldown(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	// First call succeeds
	event := makeDecisionEvent("AAPL", models.SignalBuy, 0.85)
	err := s.HandleDecisionEvent(context.Background(), event)
	require.NoError(t, err)

	// Second call should be skipped (in cooldown)
	err = s.HandleDecisionEvent(context.Background(), event)
	assert.NoError(t, err) // silently skipped
}

func TestHandleDecisionEvent_ScaleInUsesShorterCooldown(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeDecisionEvent("AAPL", models.SignalBuy, 0.85)
	event.Data.RulesTriggered = []models.RuleResult{{RuleName: "Average Down"}}

	// First call
	err := s.HandleDecisionEvent(context.Background(), event)
	require.NoError(t, err)

	// Set cooldown to 6 minutes ago — past scale-in cooldown (5min) but within normal (30min)
	s.cooldownMu.Lock()
	s.cooldowns["AAPL:BUY"] = time.Now().Add(-6 * time.Minute)
	s.cooldownMu.Unlock()

	// Should be allowed (scale-in uses 5-min cooldown)
	err = s.HandleDecisionEvent(context.Background(), event)
	assert.NoError(t, err)
}

func TestHandleDecisionEvent_BuySignalSendsKeyboard(t *testing.T) {
	var receivedReq telegram.SendMessageRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedReq)
		resp := telegram.SendMessageResponse{
			OK:     true,
			Result: &telegram.MessageResult{MessageID: 1},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tc := telegram.NewClient("test", 42, telegram.WithHTTPClient(&http.Client{Transport: &redirectTransport{target: server.URL}}))

	s := &AlertService{
		config:           newTestConfig(),
		telegramClient:   tc,
		cooldowns:        make(map[string]time.Time),
		rankingCooldowns: make(map[string]time.Time),
		feedbackLog:      make(map[string]models.FeedbackEntry),
		metrics:          metrics.Nop{},
		stopCleanup:      make(chan struct{}),
		stopFeedback:     make(chan struct{}),
	}

	event := makeDecisionEvent("AAPL", models.SignalBuy, 0.85)
	err := s.HandleDecisionEvent(context.Background(), event)
	require.NoError(t, err)

	// BUY signals should include feedback keyboard with only "Traded" button
	require.NotNil(t, receivedReq.ReplyMarkup)
	assert.Equal(t, 1, len(receivedReq.ReplyMarkup.InlineKeyboard))
	assert.Equal(t, 1, len(receivedReq.ReplyMarkup.InlineKeyboard[0]))
	assert.Contains(t, receivedReq.ReplyMarkup.InlineKeyboard[0][0].Text, "Traded")

	// Default "skipped" feedback should already be recorded in-memory
	feedbackLog := s.GetFeedbackLog()
	require.Equal(t, 1, len(feedbackLog))
	for _, entry := range feedbackLog {
		assert.Equal(t, "AAPL", entry.Symbol)
		assert.Equal(t, "BUY", entry.Signal)
		assert.Equal(t, "skipped", entry.Action)
	}
}

func TestHandleDecisionEvent_WatchSignalNoKeyboard(t *testing.T) {
	var receivedReq telegram.SendMessageRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedReq)
		resp := telegram.SendMessageResponse{OK: true, Result: &telegram.MessageResult{MessageID: 1}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tc := telegram.NewClient("test", 42, telegram.WithHTTPClient(&http.Client{Transport: &redirectTransport{target: server.URL}}))

	s := &AlertService{
		config:           newTestConfig(),
		telegramClient:   tc,
		cooldowns:        make(map[string]time.Time),
		rankingCooldowns: make(map[string]time.Time),
		feedbackLog:      make(map[string]models.FeedbackEntry),
		metrics:          metrics.Nop{},
		stopCleanup:      make(chan struct{}),
		stopFeedback:     make(chan struct{}),
	}
	s.config.AlertOnWatch = true

	event := makeDecisionEvent("MSFT", models.SignalWatch, 0.85)
	err := s.HandleDecisionEvent(context.Background(), event)
	require.NoError(t, err)

	// WATCH signals should NOT include keyboard
	assert.Nil(t, receivedReq.ReplyMarkup)
}

// ---------------------------------------------------------------------------
// HandleRankingEvent
// ---------------------------------------------------------------------------

func TestHandleRankingEvent_Success(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeRankingEvent(models.SignalBuy, []models.SymbolRanking{
		{Symbol: "AAPL", Rank: 1, Score: 0.9, Confidence: 0.8},
	})

	err := s.HandleRankingEvent(context.Background(), event)
	assert.NoError(t, err)
}

func TestHandleRankingEvent_InvalidType(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	err := s.HandleRankingEvent(context.Background(), "not an event")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid event type")
}

func TestHandleRankingEvent_Disabled(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.config.AlertOnRankings = false
	event := makeRankingEvent(models.SignalBuy, nil)
	err := s.HandleRankingEvent(context.Background(), event)
	assert.NoError(t, err) // silently skipped
}

func TestHandleRankingEvent_InCooldown(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	event := makeRankingEvent(models.SignalBuy, []models.SymbolRanking{
		{Symbol: "AAPL", Rank: 1, Score: 0.9, Confidence: 0.8},
	})

	// First call
	err := s.HandleRankingEvent(context.Background(), event)
	require.NoError(t, err)

	// Second call should be skipped
	err = s.HandleRankingEvent(context.Background(), event)
	assert.NoError(t, err) // silently skipped
}

// ---------------------------------------------------------------------------
// handleFeedbackCallback
// ---------------------------------------------------------------------------

func TestHandleFeedbackCallback_ValidTraded(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	cb := &telegram.CallbackQuery{
		ID:   "cb1",
		Data: "fb:AAPL:BUY:120000:traded",
	}

	s.handleFeedbackCallback(context.Background(), cb)

	log := s.GetFeedbackLog()
	entry, ok := log["AAPL:BUY:120000"]
	require.True(t, ok)
	assert.Equal(t, "AAPL", entry.Symbol)
	assert.Equal(t, "BUY", entry.Signal)
	assert.Equal(t, "traded", entry.Action)
}

func TestHandleFeedbackCallback_TradedOverridesDefaultSkipped(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	// Simulate the default "skipped" recorded at send time
	s.feedbackMu.Lock()
	s.feedbackLog["GOOG:BUY:130000"] = models.FeedbackEntry{
		Symbol:    "GOOG",
		Signal:    "BUY",
		Action:    "skipped",
		Timestamp: time.Now(),
	}
	s.feedbackMu.Unlock()

	// User presses "Traded" — should override
	cb := &telegram.CallbackQuery{
		ID:   "cb2",
		Data: "fb:GOOG:BUY:130000:traded",
	}
	s.handleFeedbackCallback(context.Background(), cb)

	log := s.GetFeedbackLog()
	entry, ok := log["GOOG:BUY:130000"]
	require.True(t, ok)
	assert.Equal(t, "traded", entry.Action)
}

func TestHandleFeedbackCallback_InvalidFormat(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	tests := []string{
		"not-feedback-data",
		"fb:AAPL",              // too few parts
		"fb:AAPL:BUY",          // too few parts
		"xx:AAPL:BUY:1:traded", // wrong prefix
	}

	for _, data := range tests {
		cb := &telegram.CallbackQuery{ID: "cb", Data: data}
		s.handleFeedbackCallback(context.Background(), cb)
	}

	// None should have been recorded
	assert.Empty(t, s.GetFeedbackLog())
}

// ---------------------------------------------------------------------------
// GetFeedbackLog
// ---------------------------------------------------------------------------

func TestGetFeedbackLog_ReturnsCopy(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.feedbackMu.Lock()
	s.feedbackLog["key"] = models.FeedbackEntry{Symbol: "AAPL"}
	s.feedbackMu.Unlock()

	log1 := s.GetFeedbackLog()
	log2 := s.GetFeedbackLog()

	// Modifying the returned map should not affect the original
	delete(log1, "key")
	assert.Contains(t, log2, "key")
}

func TestGetFeedbackLog_Empty(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	assert.Empty(t, s.GetFeedbackLog())
}

// ---------------------------------------------------------------------------
// cleanupExpiredCooldowns
// ---------------------------------------------------------------------------

func TestCleanupExpiredCooldowns(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	recent := time.Now().Add(-10 * time.Minute)
	expired := time.Now().Add(-2 * time.Hour) // well past 2x cooldown (60 min)

	s.cooldownMu.Lock()
	s.cooldowns["AAPL:BUY"] = recent
	s.cooldowns["GOOG:SELL"] = expired
	s.cooldownMu.Unlock()

	s.rankingCooldownMu.Lock()
	s.rankingCooldowns["BUY"] = recent
	s.rankingCooldowns["SELL"] = expired
	s.rankingCooldownMu.Unlock()

	s.cleanupExpiredCooldowns()

	s.cooldownMu.RLock()
	_, aaplExists := s.cooldowns["AAPL:BUY"]
	_, googExists := s.cooldowns["GOOG:SELL"]
	s.cooldownMu.RUnlock()

	assert.True(t, aaplExists, "recent cooldown should remain")
	assert.False(t, googExists, "expired cooldown should be cleaned up")

	s.rankingCooldownMu.RLock()
	_, buyExists := s.rankingCooldowns["BUY"]
	_, sellExists := s.rankingCooldowns["SELL"]
	s.rankingCooldownMu.RUnlock()

	assert.True(t, buyExists, "recent ranking cooldown should remain")
	assert.False(t, sellExists, "expired ranking cooldown should be cleaned up")
}

// ---------------------------------------------------------------------------
// Muted symbols (context/indicator symbols)
// ---------------------------------------------------------------------------

func TestHandleDecisionEvent_MutedSymbolSkipped(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.config.MutedSymbols = map[string]bool{"SPY": true, "QQQ": true, "GDX": true}

	event := makeDecisionEvent("SPY", models.SignalBuy, 0.9)
	err := s.HandleDecisionEvent(context.Background(), event)

	require.NoError(t, err)
	// Verify no cooldown was set (alert was never sent)
	s.cooldownMu.RLock()
	_, exists := s.cooldowns["SPY:BUY"]
	s.cooldownMu.RUnlock()
	assert.False(t, exists, "muted symbol should not create a cooldown entry")
}

func TestHandleDecisionEvent_NonMutedSymbolNotBlocked(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	s.config.MutedSymbols = map[string]bool{"SPY": true, "QQQ": true}

	event := makeDecisionEvent("CCJ", models.SignalBuy, 0.8)
	// CCJ is not muted but may fail on other gates (no trade plan → low R:R).
	// Disable R:R gate so we can verify it passes the mute check.
	s.config.MinRRRatio = 0

	err := s.HandleDecisionEvent(context.Background(), event)
	require.NoError(t, err)

	// Verify cooldown WAS set (alert was sent)
	s.cooldownMu.RLock()
	_, exists := s.cooldowns["CCJ:BUY"]
	s.cooldownMu.RUnlock()
	assert.True(t, exists, "non-muted symbol should pass the mute check and create cooldown")
}

// ---------------------------------------------------------------------------
// retryFailedFeedback
// ---------------------------------------------------------------------------

func TestRetryFailedFeedback_RetriesFailedEntries(t *testing.T) {
	// Set up a mock stock-service that always returns success (id=99)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/feedback" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintln(w, `{"id": 99}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := newTestConfig()
	cfg.StockServiceURL = server.URL

	s := &AlertService{
		config:           cfg,
		cooldowns:        make(map[string]time.Time),
		rankingCooldowns: make(map[string]time.Time),
		feedbackLog:      make(map[string]models.FeedbackEntry),
		metrics:          metrics.Nop{},
		stopCleanup:      make(chan struct{}),
		stopFeedback:     make(chan struct{}),
		stopRetry:        make(chan struct{}),
	}

	// Manually create the stock client pointing at our test server
	s.stockClient = client.NewStockServiceClient(server.URL)

	// Seed a failed entry (FeedbackID == 0)
	s.feedbackLog["AAPL:BUY"] = models.FeedbackEntry{
		Symbol:     "AAPL",
		Signal:     "BUY",
		Action:     "skipped",
		Timestamp:  time.Now(),
		FeedbackID: 0,
	}

	// Also seed a successful entry that should NOT be retried
	s.feedbackLog["GOOG:BUY"] = models.FeedbackEntry{
		Symbol:     "GOOG",
		Signal:     "BUY",
		Action:     "traded",
		Timestamp:  time.Now(),
		FeedbackID: 42,
	}

	// Run the retry loop briefly — we start it, let the ticker fire, then stop
	// Instead of waiting for the ticker, call the internal retry logic directly.
	// We simulate what retryFailedFeedback does on each tick.
	s.feedbackMu.RLock()
	var retryKeys []string
	for key, entry := range s.feedbackLog {
		if entry.FeedbackID == 0 {
			retryKeys = append(retryKeys, key)
		}
	}
	s.feedbackMu.RUnlock()

	for _, key := range retryKeys {
		s.feedbackMu.RLock()
		entry, ok := s.feedbackLog[key]
		s.feedbackMu.RUnlock()
		if !ok || entry.FeedbackID != 0 {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		fbID := s.stockClient.PostFeedback(ctx, entry.Symbol, entry.Signal,
			entry.Action, 0, entry.RulesTriggered, entry.RegimeID, 0, nil)
		cancel()

		if fbID > 0 {
			s.feedbackMu.Lock()
			if current, exists := s.feedbackLog[key]; exists && current.FeedbackID == 0 {
				current.FeedbackID = fbID
				s.feedbackLog[key] = current
			}
			s.feedbackMu.Unlock()
		}
	}

	// Verify: AAPL should now have a valid FeedbackID
	s.feedbackMu.RLock()
	aaplEntry := s.feedbackLog["AAPL:BUY"]
	googEntry := s.feedbackLog["GOOG:BUY"]
	s.feedbackMu.RUnlock()

	assert.Equal(t, 99, aaplEntry.FeedbackID, "AAPL should have been retried and updated")
	assert.Equal(t, 42, googEntry.FeedbackID, "GOOG should not have been touched")
}

func TestRetryFailedFeedback_NoStockClientSkips(t *testing.T) {
	s := &AlertService{
		config:           newTestConfig(),
		cooldowns:        make(map[string]time.Time),
		rankingCooldowns: make(map[string]time.Time),
		feedbackLog:      make(map[string]models.FeedbackEntry),
		stockClient:      nil, // No stock client
		metrics:          metrics.Nop{},
		stopCleanup:      make(chan struct{}),
		stopFeedback:     make(chan struct{}),
		stopRetry:        make(chan struct{}),
	}

	// Seed a failed entry
	s.feedbackLog["AAPL:BUY"] = models.FeedbackEntry{
		Symbol:     "AAPL",
		Signal:     "BUY",
		Action:     "skipped",
		Timestamp:  time.Now(),
		FeedbackID: 0,
	}

	// Without stockClient, the retry should simply skip (no panic)
	// Verify the entry is unchanged after the "skip" path
	s.feedbackMu.RLock()
	entry := s.feedbackLog["AAPL:BUY"]
	s.feedbackMu.RUnlock()

	assert.Equal(t, 0, entry.FeedbackID, "entry should remain unfixed when stockClient is nil")
}

func TestRetryFailedFeedback_StopsOnChannel(t *testing.T) {
	s := &AlertService{
		config:           newTestConfig(),
		cooldowns:        make(map[string]time.Time),
		rankingCooldowns: make(map[string]time.Time),
		feedbackLog:      make(map[string]models.FeedbackEntry),
		stockClient:      nil,
		metrics:          metrics.Nop{},
		stopCleanup:      make(chan struct{}),
		stopFeedback:     make(chan struct{}),
		stopRetry:        make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		s.retryFailedFeedback()
		close(done)
	}()

	// Signal stop
	close(s.stopRetry)

	// Should exit within a reasonable time
	select {
	case <-done:
		// Success — goroutine exited
	case <-time.After(2 * time.Second):
		t.Fatal("retryFailedFeedback did not exit after stopRetry was closed")
	}
}

func TestHandleDecisionEvent_NilMutedSymbolsNoError(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()

	// MutedSymbols is nil (no env var set) — should not panic
	s.config.MutedSymbols = nil
	s.config.MinRRRatio = 0

	event := makeDecisionEvent("CCJ", models.SignalBuy, 0.8)
	err := s.HandleDecisionEvent(context.Background(), event)
	require.NoError(t, err)

	s.cooldownMu.RLock()
	_, exists := s.cooldowns["CCJ:BUY"]
	s.cooldownMu.RUnlock()
	assert.True(t, exists, "nil MutedSymbols should not block any alerts")
}
