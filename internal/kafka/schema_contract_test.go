package kafka

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	schemas "github.com/trogers1052/trading-event-schemas"

	"github.com/trogers1052/alert-service/internal/models"
)

// TestSchemaContract_DecisionEvent_Consumer closes the consumer↔contract loop
// against the shared source of truth (trading-event-schemas v0.1.0) instead of
// a stale local fixture — the latter was the root cause of the original
// DECISION_UPDATE envelope drift bug.
//
// It (1) sanity-checks that the canonical example validates against its own
// JSON Schema, then (2) unmarshals that same canonical example into the
// consumer's own models.DecisionEvent and asserts the consumer's Validate()
// ACCEPTS it. Together these guarantee the alert-service consumer stays
// compatible with the producer contract.
func TestSchemaContract_DecisionEvent_Consumer(t *testing.T) {
	example, err := schemas.Example("decision_event")
	require.NoError(t, err, "load canonical decision_event example")

	// Sanity: the canonical example conforms to its own schema.
	require.NoError(t, schemas.Validate("decision_event", example),
		"canonical decision_event example must validate against its schema")

	// The consumer can parse the canonical contract.
	var got models.DecisionEvent
	require.NoError(t, json.Unmarshal(example, &got),
		"consumer failed to unmarshal canonical decision_event example into models.DecisionEvent")

	// The consumer ACCEPTS the canonical contract.
	require.NoError(t, got.Validate(),
		"consumer Validate() rejected the canonical decision_event example")

	// Envelope must match the schema const values exactly.
	assert.Equal(t, models.EventTypeDecision, got.EventType)
	assert.NotEmpty(t, got.SchemaVersion)
}

// TestSchemaContract_RankingEvent_Consumer is the ranking_event counterpart of
// TestSchemaContract_DecisionEvent_Consumer.
func TestSchemaContract_RankingEvent_Consumer(t *testing.T) {
	example, err := schemas.Example("ranking_event")
	require.NoError(t, err, "load canonical ranking_event example")

	// Sanity: the canonical example conforms to its own schema.
	require.NoError(t, schemas.Validate("ranking_event", example),
		"canonical ranking_event example must validate against its schema")

	// The consumer can parse the canonical contract.
	var got models.RankingEvent
	require.NoError(t, json.Unmarshal(example, &got),
		"consumer failed to unmarshal canonical ranking_event example into models.RankingEvent")

	// The consumer ACCEPTS the canonical contract.
	require.NoError(t, got.Validate(),
		"consumer Validate() rejected the canonical ranking_event example")

	// Envelope must match the schema const values exactly.
	assert.Equal(t, models.EventTypeRanking, got.EventType)
	assert.NotEmpty(t, got.SchemaVersion)
}
