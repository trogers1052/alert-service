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
	}))
	defer server.Close()

	c := NewStockServiceClient(server.URL)
	c.PostFeedback(context.Background(), "AAPL", "BUY", "traded", 0.85)

	assert.Equal(t, "AAPL", receivedBody.Symbol)
	assert.Equal(t, "BUY", receivedBody.Signal)
	assert.Equal(t, "traded", receivedBody.Action)
	assert.InDelta(t, 0.85, receivedBody.Confidence, 0.001)
}

func TestPostFeedback_NonCreatedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := NewStockServiceClient(server.URL)
	// Should not panic — errors are logged, not returned
	c.PostFeedback(context.Background(), "AAPL", "BUY", "traded", 0)
}

func TestPostFeedback_NetworkError(t *testing.T) {
	// Server is closed immediately — connection refused
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	c := NewStockServiceClient(server.URL)
	// Should not panic
	c.PostFeedback(context.Background(), "AAPL", "BUY", "traded", 0)
}

func TestPostFeedback_CancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	c := NewStockServiceClient(server.URL)
	// Should not panic — cancelled context is handled
	c.PostFeedback(ctx, "AAPL", "BUY", "traded", 0)
}
