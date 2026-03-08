package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NewStockServiceClient
// ---------------------------------------------------------------------------

func TestNewStockServiceClient_EmptyURL(t *testing.T) {
	c := NewStockServiceClient("")
	assert.Nil(t, c)
}

func TestNewStockServiceClient_WithURL(t *testing.T) {
	c := NewStockServiceClient("http://localhost:8081")
	require.NotNil(t, c)
	assert.Equal(t, "http://localhost:8081", c.baseURL)
}

// ---------------------------------------------------------------------------
// PostFeedback
// ---------------------------------------------------------------------------

func TestPostFeedback_Success(t *testing.T) {
	var receivedBody feedbackRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/feedback", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]int{"id": 42})
	}))
	defer server.Close()

	c := NewStockServiceClient(server.URL)
	id := c.PostFeedback(context.Background(), "AAPL", "BUY", "traded", 0.85, nil, "", 0, nil)

	assert.Equal(t, 42, id)
	assert.Equal(t, "AAPL", receivedBody.Symbol)
	assert.Equal(t, "BUY", receivedBody.Signal)
	assert.Equal(t, "traded", receivedBody.Action)
	assert.InDelta(t, 0.85, receivedBody.Confidence, 0.001)
}

func TestPostFeedback_WithEnrichment(t *testing.T) {
	var receivedBody feedbackRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]int{"id": 99})
	}))
	defer server.Close()

	c := NewStockServiceClient(server.URL)
	rules := []string{"MomentumReversal", "RSIOversold"}
	id := c.PostFeedback(context.Background(), "TSLA", "BUY", "skipped", 0.72,
		rules, "BULL", 0.72, nil)

	assert.Equal(t, 99, id)
	assert.Equal(t, "TSLA", receivedBody.Symbol)
	assert.Equal(t, []string{"MomentumReversal", "RSIOversold"}, receivedBody.RulesTriggered)
	assert.Equal(t, "BULL", receivedBody.RegimeID)
	assert.InDelta(t, 0.72, receivedBody.DecisionConfidence, 0.001)
}

func TestPostFeedback_WithTradePlanParams(t *testing.T) {
	var receivedBody feedbackRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]int{"id": 55})
	}))
	defer server.Close()

	c := NewStockServiceClient(server.URL)
	params := &FeedbackParams{
		EntryPrice: 25.50,
		StopPrice:  24.00,
		Target1:    28.00,
		Target2:    30.50,
		ValidUntil: "2026-03-10T16:00:00Z",
	}
	id := c.PostFeedback(context.Background(), "XYZ", "BUY", "skipped", 0.80,
		[]string{"RSIOversold"}, "BULL", 0.80, params)

	assert.Equal(t, 55, id)
	assert.InDelta(t, 25.50, receivedBody.EntryPrice, 0.001)
	assert.InDelta(t, 24.00, receivedBody.StopPrice, 0.001)
	assert.InDelta(t, 28.00, receivedBody.Target1, 0.001)
	assert.InDelta(t, 30.50, receivedBody.Target2, 0.001)
	assert.Equal(t, "2026-03-10T16:00:00Z", receivedBody.ValidUntil)
}

func TestPostFeedback_NonCreatedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := NewStockServiceClient(server.URL)
	id := c.PostFeedback(context.Background(), "AAPL", "BUY", "traded", 0, nil, "", 0, nil)
	assert.Equal(t, 0, id)
}

func TestPostFeedback_NetworkError(t *testing.T) {
	// Server is closed immediately — connection refused
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	c := NewStockServiceClient(server.URL)
	id := c.PostFeedback(context.Background(), "AAPL", "BUY", "traded", 0, nil, "", 0, nil)
	assert.Equal(t, 0, id)
}

func TestPostFeedback_CancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]int{"id": 1})
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	c := NewStockServiceClient(server.URL)
	id := c.PostFeedback(ctx, "AAPL", "BUY", "traded", 0, nil, "", 0, nil)
	assert.Equal(t, 0, id)
}

// ---------------------------------------------------------------------------
// UpdateFeedbackAction
// ---------------------------------------------------------------------------

func TestUpdateFeedbackAction_Success(t *testing.T) {
	var receivedBody map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/feedback/42", r.URL.Path)
		assert.Equal(t, http.MethodPut, r.Method)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewStockServiceClient(server.URL)
	c.UpdateFeedbackAction(context.Background(), 42, "traded")

	assert.Equal(t, "traded", receivedBody["action"])
}

func TestUpdateFeedbackAction_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	c := NewStockServiceClient(server.URL)
	// Should not panic
	c.UpdateFeedbackAction(context.Background(), 42, "traded")
}
