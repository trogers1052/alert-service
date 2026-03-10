package kafka

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	testkit "github.com/trogers1052/trading-testkit"

	"github.com/trogers1052/alert-service/internal/models"
)

func TestContract_DecisionEvent_Consumer(t *testing.T) {
	raw := testkit.LoadContract("decision_event.json")

	var got models.DecisionEvent
	err := json.Unmarshal(raw, &got)
	require.NoError(t, err, "failed to unmarshal decision_event.json into models.DecisionEvent")

	assert.Equal(t, "DECISION", got.EventType)
	assert.Equal(t, "decision-engine", got.Source)
	assert.Equal(t, "1.0", got.SchemaVersion)
	assert.False(t, got.Timestamp.IsZero(), "Timestamp should not be zero")

	assert.Equal(t, "AAPL", got.Data.Symbol)
	assert.Equal(t, "BUY", got.Data.Signal)
	assert.Equal(t, 0.85, got.Data.Confidence)

	require.Len(t, got.Data.RulesTriggered, 1)
	assert.Equal(t, "MomentumReversalRule", got.Data.RulesTriggered[0].RuleName)

	require.NotNil(t, got.Data.TradePlan, "TradePlan should not be nil")
	assert.Equal(t, 175.50, got.Data.TradePlan.EntryPrice)
	assert.Equal(t, 169.10, got.Data.TradePlan.StopPrice)
	assert.True(t, got.Data.TradePlan.PlanValid)

	require.NotNil(t, got.Data.Checklist, "Checklist should not be nil")
	assert.True(t, got.Data.Checklist.AllChecksPassed)
	assert.Equal(t, "GO", got.Data.Checklist.Status)
}

func TestContract_RankingEvent_Consumer(t *testing.T) {
	raw := testkit.LoadContract("ranking_event.json")

	var got models.RankingEvent
	err := json.Unmarshal(raw, &got)
	require.NoError(t, err, "failed to unmarshal ranking_event.json into models.RankingEvent")

	assert.Equal(t, "RANKING", got.EventType)
	assert.Equal(t, "decision-engine", got.Source)

	assert.Equal(t, "BUY", got.Data.SignalType)
	assert.Equal(t, 3, got.Data.TotalSymbols)

	require.Len(t, got.Data.Rankings, 2)

	assert.Equal(t, "NVDA", got.Data.Rankings[0].Symbol)
	assert.Equal(t, 1, got.Data.Rankings[0].Rank)
	assert.InDelta(t, 0.92, got.Data.Rankings[0].Score, 0.001)
	assert.Greater(t, len(got.Data.Rankings[0].RankingFactors), 0, "RankingFactors should not be empty")

	assert.Equal(t, "AMD", got.Data.Rankings[1].Symbol)
}
