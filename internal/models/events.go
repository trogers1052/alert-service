package models

import (
	"fmt"
	"time"
)

// ValidSignalTypes contains the set of accepted signal type values.
var ValidSignalTypes = map[string]bool{
	SignalBuy:   true,
	SignalSell:  true,
	SignalWatch: true,
}

// MaxSymbolLength is the maximum allowed length for a ticker symbol.
const MaxSymbolLength = 10

// DecisionEvent represents a trading decision from the decision-engine
type DecisionEvent struct {
	EventType     string        `json:"event_type"`
	Source        string        `json:"source"`
	SchemaVersion string        `json:"schema_version"`
	Timestamp     time.Time     `json:"timestamp"`
	Data          DecisionData  `json:"data"`
}

// DecisionData contains the actual decision information
type DecisionData struct {
	Symbol             string                 `json:"symbol"`
	Signal             string                 `json:"signal"` // BUY, SELL, WATCH
	Confidence         float64                `json:"confidence"`
	PrimaryReasoning   string                 `json:"primary_reasoning"`
	RulesTriggered     []RuleResult           `json:"rules_triggered"`
	IndicatorsSnapshot map[string]float64     `json:"indicators_snapshot"`
	Metadata           map[string]interface{} `json:"metadata"`
	TradePlan          *TradePlan             `json:"trade_plan,omitempty"`
	Checklist          *Checklist             `json:"checklist,omitempty"`
}

// Validate checks that all required fields in a DecisionEvent are present and
// within acceptable ranges. It returns a descriptive error for the first
// violation found, or nil when the message is well-formed.
func (e *DecisionEvent) Validate() error {
	d := e.Data

	// Symbol: required, non-empty, max 10 characters
	if d.Symbol == "" {
		return fmt.Errorf("missing required field: symbol")
	}
	if len(d.Symbol) > MaxSymbolLength {
		return fmt.Errorf("symbol %q exceeds max length of %d characters", d.Symbol, MaxSymbolLength)
	}

	// Signal (signal_type): must be one of the known values
	if d.Signal == "" {
		return fmt.Errorf("missing required field: signal_type")
	}
	if !ValidSignalTypes[d.Signal] {
		return fmt.Errorf("invalid signal_type %q: expected one of BUY, SELL, WATCH", d.Signal)
	}

	// Confidence: must be in [0, 1]
	if d.Confidence < 0 || d.Confidence > 1 {
		return fmt.Errorf("confidence %.4f out of range [0, 1]", d.Confidence)
	}

	return nil
}

// Checklist holds the pre-trade checklist evaluation result
type Checklist struct {
	StopLossDefined         bool    `json:"stop_loss_defined"`
	PositionSizedCorrectly  bool    `json:"position_sized_correctly"`
	RRRatioAcceptable       bool    `json:"rr_ratio_acceptable"`
	NoEarningsImminent      bool    `json:"no_earnings_imminent"`
	RegimeCompatible        bool    `json:"regime_compatible"`
	AllChecksPassed         bool    `json:"all_checks_passed"`
	Status                  string  `json:"status"` // "GO" | "REVIEW" | "BLOCKED"
	EarningsDate            *string `json:"earnings_date,omitempty"`
	EarningsDaysAway        *int    `json:"earnings_days_away,omitempty"`
	EarningsVerified        *bool   `json:"earnings_verified,omitempty"`
	RegimeID                string  `json:"regime_id"`
	RiskPct                 float64 `json:"risk_pct"`
	RRRatio                 float64 `json:"rr_ratio"`
}

// TradePlan contains entry/stop/target details for a trade
type TradePlan struct {
	SetupType        string   `json:"setup_type"`
	RulesContributed []string `json:"rules_contributed"`
	EntryPrice       float64  `json:"entry_price"`
	EntryZoneLow     float64  `json:"entry_zone_low"`
	EntryZoneHigh    float64  `json:"entry_zone_high"`
	ValidUntil       string   `json:"valid_until"`
	StopPrice        float64  `json:"stop_price"`
	StopMethod       string   `json:"stop_method"`
	StopPct          float64  `json:"stop_pct"`
	SupportLevelUsed *string  `json:"support_level_used,omitempty"`
	Target1          float64  `json:"target_1"`
	Target2          float64  `json:"target_2"`
	SymbolTargetPct  *float64 `json:"symbol_target_pct,omitempty"`
	ResistanceNote   *string  `json:"resistance_note,omitempty"`

	// Target context — probability, timeframe, and price positioning
	Target1Probability *float64 `json:"target_1_probability,omitempty"`
	Target1EstDays     *int     `json:"target_1_est_days,omitempty"`
	Target2Probability *float64 `json:"target_2_probability,omitempty"`
	Target2EstDays     *int     `json:"target_2_est_days,omitempty"`
	PriceContext       *string  `json:"price_context,omitempty"`

	RiskRewardRatio      float64  `json:"risk_reward_ratio"`
	Shares               int      `json:"shares"`
	DollarRisk           float64  `json:"dollar_risk"`
	RiskPct              float64  `json:"risk_pct"`
	PositionValue        float64  `json:"position_value"`
	GoalYears            *float64 `json:"goal_years,omitempty"`
	ExpectedAnnualReturn *float64 `json:"expected_annual_return,omitempty"`
	InvalidationPrice    float64  `json:"invalidation_price"`
	PlanValid        bool     `json:"plan_valid"`
	RRWarning        *string  `json:"rr_warning,omitempty"`
	Warnings         []string `json:"warnings"`
}

// RuleResult represents a single rule that was triggered
type RuleResult struct {
	RuleName   string  `json:"rule_name"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

// RankingEvent represents a ranking update from the decision-engine
type RankingEvent struct {
	EventType     string       `json:"event_type"`
	Source        string       `json:"source"`
	SchemaVersion string       `json:"schema_version"`
	Timestamp     time.Time    `json:"timestamp"`
	Data          RankingData  `json:"data"`
}

// RankingData contains the ranking information
type RankingData struct {
	SignalType   string          `json:"signal_type"` // BUY, SELL, WATCH
	Criteria     string          `json:"criteria"`
	Timestamp    time.Time       `json:"timestamp"`
	TotalSymbols int             `json:"total_symbols"`
	Rankings     []SymbolRanking `json:"rankings"`
}

// SymbolRanking represents a single symbol's ranking
type SymbolRanking struct {
	Symbol         string             `json:"symbol"`
	Rank           int                `json:"rank"`
	Score          float64            `json:"score"`
	SignalType     string             `json:"signal_type"`
	Confidence     float64            `json:"confidence"`
	Reasoning      string             `json:"reasoning"`
	RankingFactors map[string]float64 `json:"ranking_factors"`
}

// Signal types
const (
	SignalBuy   = "BUY"
	SignalSell  = "SELL"
	SignalWatch = "WATCH"
)

// FeedbackAction represents user response to an alert
const (
	FeedbackTraded = "traded"
	FeedbackSkipped = "skipped"
)

// FeedbackEntry records whether the user acted on a signal
type FeedbackEntry struct {
	Symbol    string    `json:"symbol"`
	Signal    string    `json:"signal"`
	Action    string    `json:"action"` // "traded" or "skipped"
	Timestamp time.Time `json:"timestamp"`
}
