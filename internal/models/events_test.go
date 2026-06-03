package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// DecisionEvent.Validate
// ---------------------------------------------------------------------------

func TestValidate_ValidBuyEvent(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "AAPL",
			Signal:     SignalBuy,
			Confidence: 0.85,
		},
	}
	assert.NoError(t, e.Validate())
}

func TestValidate_ValidSellEvent(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "GOOG",
			Signal:     SignalSell,
			Confidence: 0.0,
		},
	}
	assert.NoError(t, e.Validate())
}

func TestValidate_ValidWatchEvent(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "TSLA",
			Signal:     SignalWatch,
			Confidence: 1.0,
		},
	}
	assert.NoError(t, e.Validate())
}

// TestValidate_WrongEventType asserts Validate rejects an event whose
// event_type does not match the canonical producer value. This is the guard
// against silent cross-repo schema drift.
func TestValidate_WrongEventType(t *testing.T) {
	e := &DecisionEvent{
		EventType:     "DECISION", // legacy/wrong value
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "AAPL",
			Signal:     SignalBuy,
			Confidence: 0.85,
		},
	}
	err := e.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event_type")
}

// TestValidate_MissingSchemaVersion asserts schema_version must be present
// (lenient: any non-empty value is accepted for forward-compatibility).
func TestValidate_MissingSchemaVersion(t *testing.T) {
	e := &DecisionEvent{
		EventType: EventTypeDecision,
		Data: DecisionData{
			Symbol:     "AAPL",
			Signal:     SignalBuy,
			Confidence: 0.85,
		},
	}
	err := e.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_version")
}

// TestValidate_LenientSchemaVersion asserts a future schema_version is still
// accepted (forward-compatible) as long as event_type is canonical.
func TestValidate_LenientSchemaVersion(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "9.9",
		Data: DecisionData{
			Symbol:     "AAPL",
			Signal:     SignalBuy,
			Confidence: 0.85,
		},
	}
	assert.NoError(t, e.Validate())
}

func TestValidate_MissingSymbol(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "",
			Signal:     SignalBuy,
			Confidence: 0.7,
		},
	}
	err := e.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbol")
}

func TestValidate_SymbolTooLong(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "VERYLONGSYMB", // 12 chars > MaxSymbolLength(10)
			Signal:     SignalBuy,
			Confidence: 0.7,
		},
	}
	err := e.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds max length")
}

func TestValidate_SymbolExactlyMaxLength(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "ABCDEFGHIJ", // exactly 10 chars
			Signal:     SignalBuy,
			Confidence: 0.7,
		},
	}
	assert.NoError(t, e.Validate())
}

func TestValidate_MissingSignal(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "AAPL",
			Signal:     "",
			Confidence: 0.7,
		},
	}
	err := e.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signal_type")
}

func TestValidate_InvalidSignal(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "AAPL",
			Signal:     "HOLD",
			Confidence: 0.7,
		},
	}
	err := e.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signal_type")
}

func TestValidate_ConfidenceNegative(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "AAPL",
			Signal:     SignalBuy,
			Confidence: -0.1,
		},
	}
	err := e.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confidence")
}

func TestValidate_ConfidenceAboveOne(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "AAPL",
			Signal:     SignalBuy,
			Confidence: 1.01,
		},
	}
	err := e.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confidence")
}

func TestValidate_ConfidenceBoundaryZero(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "AAPL",
			Signal:     SignalBuy,
			Confidence: 0.0,
		},
	}
	assert.NoError(t, e.Validate())
}

func TestValidate_ConfidenceBoundaryOne(t *testing.T) {
	e := &DecisionEvent{
		EventType:     EventTypeDecision,
		SchemaVersion: "1.2",
		Data: DecisionData{
			Symbol:     "AAPL",
			Signal:     SignalBuy,
			Confidence: 1.0,
		},
	}
	assert.NoError(t, e.Validate())
}

// ---------------------------------------------------------------------------
// RankingEvent.Validate
// ---------------------------------------------------------------------------

func TestRankingValidate_Valid(t *testing.T) {
	e := &RankingEvent{
		EventType:     EventTypeRanking,
		SchemaVersion: "1.0",
		Data:          RankingData{SignalType: SignalBuy, TotalSymbols: 3},
	}
	assert.NoError(t, e.Validate())
}

func TestRankingValidate_WrongEventType(t *testing.T) {
	e := &RankingEvent{
		EventType:     "RANKING", // legacy/wrong value
		SchemaVersion: "1.0",
		Data:          RankingData{SignalType: SignalBuy},
	}
	err := e.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event_type")
}

func TestRankingValidate_MissingSchemaVersion(t *testing.T) {
	e := &RankingEvent{
		EventType: EventTypeRanking,
		Data:      RankingData{SignalType: SignalBuy},
	}
	err := e.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_version")
}

// ---------------------------------------------------------------------------
// JSON round-trip
// ---------------------------------------------------------------------------

func TestDecisionEvent_JSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	rr := "resistance warning"
	original := &DecisionEvent{
		EventType:     "decision",
		Source:        "decision-engine",
		SchemaVersion: "1.0",
		Timestamp:     now,
		Data: DecisionData{
			Symbol:           "AAPL",
			Signal:           SignalBuy,
			Confidence:       0.85,
			PrimaryReasoning: "Strong momentum",
			RulesTriggered: []RuleResult{
				{RuleName: "RSI Oversold", Confidence: 0.9, Reasoning: "RSI < 30"},
			},
			IndicatorsSnapshot: map[string]float64{"rsi": 28.5, "macd": 0.5},
			TradePlan: &TradePlan{
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
				ResistanceNote:    &rr,
			},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded DecisionEvent
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, original.EventType, decoded.EventType)
	assert.Equal(t, original.Data.Symbol, decoded.Data.Symbol)
	assert.Equal(t, original.Data.Signal, decoded.Data.Signal)
	assert.InDelta(t, original.Data.Confidence, decoded.Data.Confidence, 0.001)
	require.NotNil(t, decoded.Data.TradePlan)
	assert.Equal(t, 17, decoded.Data.TradePlan.Shares)
	assert.Equal(t, "atr_2x", decoded.Data.TradePlan.StopMethod)
	assert.Equal(t, &rr, decoded.Data.TradePlan.ResistanceNote)
}

func TestDecisionEvent_JSONNoTradePlan(t *testing.T) {
	original := &DecisionEvent{
		Data: DecisionData{
			Symbol:     "GOOG",
			Signal:     SignalSell,
			Confidence: 0.7,
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded DecisionEvent
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Nil(t, decoded.Data.TradePlan)
}

func TestRankingEvent_JSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	original := &RankingEvent{
		EventType:     "ranking",
		Source:        "decision-engine",
		SchemaVersion: "1.0",
		Timestamp:     now,
		Data: RankingData{
			SignalType:   SignalBuy,
			Criteria:     "composite_score",
			Timestamp:    now,
			TotalSymbols: 50,
			Rankings: []SymbolRanking{
				{Symbol: "AAPL", Rank: 1, Score: 0.95, Confidence: 0.9, Reasoning: "Strong setup"},
				{Symbol: "GOOG", Rank: 2, Score: 0.88, Confidence: 0.85},
			},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded RankingEvent
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, 2, len(decoded.Data.Rankings))
	assert.Equal(t, "AAPL", decoded.Data.Rankings[0].Symbol)
	assert.Equal(t, 50, decoded.Data.TotalSymbols)
}

func TestChecklist_JSON(t *testing.T) {
	earningsDate := "2026-03-15"
	daysAway := 22
	verified := true
	cl := &Checklist{
		StopLossDefined:        true,
		PositionSizedCorrectly: true,
		RRRatioAcceptable:      true,
		NoEarningsImminent:     true,
		RegimeCompatible:       false,
		AllChecksPassed:        false,
		Status:                 "REVIEW",
		EarningsDate:           &earningsDate,
		EarningsDaysAway:       &daysAway,
		EarningsVerified:       &verified,
		RegimeID:               "volatile_chop",
		RiskPct:                1.5,
		RRRatio:                2.8,
	}

	data, err := json.Marshal(cl)
	require.NoError(t, err)

	var decoded Checklist
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, "REVIEW", decoded.Status)
	assert.True(t, decoded.StopLossDefined)
	assert.False(t, decoded.RegimeCompatible)
	require.NotNil(t, decoded.EarningsDate)
	assert.Equal(t, "2026-03-15", *decoded.EarningsDate)
}

// ---------------------------------------------------------------------------
// Constants & valid signal types
// ---------------------------------------------------------------------------

func TestValidSignalTypes(t *testing.T) {
	assert.True(t, ValidSignalTypes[SignalBuy])
	assert.True(t, ValidSignalTypes[SignalSell])
	assert.True(t, ValidSignalTypes[SignalWatch])
	assert.False(t, ValidSignalTypes["HOLD"])
	assert.False(t, ValidSignalTypes[""])
}

func TestMaxSymbolLength(t *testing.T) {
	assert.Equal(t, 10, MaxSymbolLength)
}

func TestFeedbackConstants(t *testing.T) {
	assert.Equal(t, "traded", FeedbackTraded)
	assert.Equal(t, "skipped", FeedbackSkipped)
}
