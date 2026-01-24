package models

import "time"

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
