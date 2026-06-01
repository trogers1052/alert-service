package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/trogers1052/alert-service/internal/metrics"
)

func TestSetMetrics_NilIgnored(t *testing.T) {
	c := NewStockServiceClient("http://example.com", "")
	// Setting nil must not overwrite the existing (Nop) recorder or panic.
	c.SetMetrics(nil)
	c.SetMetrics(metrics.Nop{})
}

func TestPostFeedback_BadJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := NewStockServiceClient(srv.URL, "")
	id := c.PostFeedback(context.Background(), "AAPL", "BUY", "traded", 0.8, nil, "", 0, nil)
	if id != 0 {
		t.Fatalf("expected 0 id on bad JSON, got %d", id)
	}
}

func TestUpdateFeedbackAction_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewStockServiceClient(srv.URL, "an-api-key")
	// Best-effort: non-OK status is logged, must not panic.
	c.UpdateFeedbackAction(context.Background(), 7, "skipped")
}

func TestPostFeedback_WithAPIKeyHeader(t *testing.T) {
	var sawKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") == "secret" {
			sawKey = true
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":5}`))
	}))
	defer srv.Close()

	c := NewStockServiceClient(srv.URL, "secret")
	if id := c.PostFeedback(context.Background(), "AAPL", "BUY", "traded", 0.8, nil, "", 0, nil); id != 5 {
		t.Fatalf("expected id 5, got %d", id)
	}
	if !sawKey {
		t.Fatal("expected X-API-Key header to be sent")
	}
}
