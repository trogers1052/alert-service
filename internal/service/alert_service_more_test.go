package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trogers1052/alert-service/internal/client"
	"github.com/trogers1052/alert-service/internal/metrics"
	"github.com/trogers1052/alert-service/internal/models"
	"github.com/trogers1052/alert-service/internal/telegram"
)

// ---------------------------------------------------------------------------
// formatTierBadge — pure function, all branches
// ---------------------------------------------------------------------------

func TestFormatTierBadge(t *testing.T) {
	tests := []struct {
		name string
		td   *models.TierData
		want string
	}{
		{"nil", nil, ""},
		{"empty tier", &models.TierData{Tier: ""}, ""},
		{"S", &models.TierData{Tier: "S"}, " 🏆 [S-tier]"},
		{"A", &models.TierData{Tier: "A"}, " 🟢 [A-tier]"},
		{"B", &models.TierData{Tier: "B"}, " 🔵 [B-tier]"},
		{"C", &models.TierData{Tier: "C"}, " 🟡 [C-tier]"},
		{"D", &models.TierData{Tier: "D"}, " 🟠 [D-tier]"},
		{"F", &models.TierData{Tier: "F"}, " 🔴 [F-tier]"},
		{"unknown", &models.TierData{Tier: "Z"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatTierBadge(tc.td); got != tc.want {
				t.Fatalf("formatTierBadge(%v) = %q, want %q", tc.td, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewAlertService + Close lifecycle
// ---------------------------------------------------------------------------

func TestNewAlertService_AndClose(t *testing.T) {
	// A telegram server that returns empty getUpdates so the feedback poller
	// loops harmlessly until Close.
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(telegram.GetUpdatesResponse{OK: true})
	}))
	defer tgSrv.Close()

	tc := telegram.NewClient("test-token", 1)
	tc.SetHTTPClientForTest(&http.Client{Transport: &redirectTransport{target: tgSrv.URL}})

	cfg := newTestConfig()
	cfg.StockServiceURL = "" // no stock client
	svc := NewAlertService(cfg, tc, metrics.Nop{})
	if svc == nil {
		t.Fatal("expected service")
	}
	// Let the background goroutines start.
	time.Sleep(50 * time.Millisecond)
	svc.Close()
}

func TestNewAlertService_NilMetricsDefaults(t *testing.T) {
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(telegram.GetUpdatesResponse{OK: true})
	}))
	defer tgSrv.Close()

	tc := telegram.NewClient("test-token", 1)
	tc.SetHTTPClientForTest(&http.Client{Transport: &redirectTransport{target: tgSrv.URL}})

	cfg := newTestConfig()
	cfg.StockServiceURL = "http://stock-service.local" // exercises stock client SetMetrics branch
	svc := NewAlertService(cfg, tc, nil)
	defer svc.Close()
	if svc.metrics == nil {
		t.Fatal("nil metrics should default to Nop")
	}
}

// ---------------------------------------------------------------------------
// handleFeedbackCallback — UpdateFeedbackAction + PostFeedback branches
// ---------------------------------------------------------------------------

// newServiceWithStock builds a service whose stock client and telegram client
// both point at the given test server URL.
func newServiceWithStock(t *testing.T, serverURL string) *AlertService {
	t.Helper()
	tc := telegram.NewClient("test-token", 12345)
	tc.SetHTTPClientForTest(&http.Client{Transport: &redirectTransport{target: serverURL}})

	sc := client.NewStockServiceClient(serverURL, "")
	sc.SetMetrics(metrics.Nop{})

	return &AlertService{
		config:           newTestConfig(),
		telegramClient:   tc,
		cooldowns:        make(map[string]time.Time),
		rankingCooldowns: make(map[string]time.Time),
		feedbackLog:      make(map[string]models.FeedbackEntry),
		stockClient:      sc,
		metrics:          metrics.Nop{},
		stopCleanup:      make(chan struct{}),
		stopFeedback:     make(chan struct{}),
		stopRetry:        make(chan struct{}),
	}
}

func TestHandleFeedbackCallback_UpdatesExistingRow(t *testing.T) {
	var putCalls, postCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			atomic.AddInt32(&putCalls, 1)
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			atomic.AddInt32(&postCalls, 1)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 99})
		default:
			// Telegram answerCallbackQuery and others.
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		}
	}))
	defer srv.Close()

	s := newServiceWithStock(t, srv.URL)

	// Pre-seed an enriched entry with a stored FeedbackID → triggers PUT update.
	s.feedbackMu.Lock()
	s.feedbackLog["AAPL:BUY:120000"] = models.FeedbackEntry{
		Symbol: "AAPL", Signal: "BUY", Action: "skipped",
		FeedbackID: 42, Timestamp: time.Now(),
	}
	s.feedbackMu.Unlock()

	cb := &telegram.CallbackQuery{ID: "cb", Data: "fb:AAPL:BUY:120000:traded"}
	s.handleFeedbackCallback(context.Background(), cb)

	if atomic.LoadInt32(&putCalls) != 1 {
		t.Fatalf("expected 1 PUT (UpdateFeedbackAction), got %d", putCalls)
	}
}

func TestHandleFeedbackCallback_PostsNewRowWhenNoStoredID(t *testing.T) {
	var postCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the stock-service feedback endpoint counts; Telegram's
		// answerCallbackQuery is also a POST but lands on a different path.
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/feedback") {
			atomic.AddInt32(&postCalls, 1)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 7})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	s := newServiceWithStock(t, srv.URL)

	// No pre-seeded entry → fallback POST path.
	cb := &telegram.CallbackQuery{ID: "cb", Data: "fb:TSLA:SELL:130000:traded"}
	s.handleFeedbackCallback(context.Background(), cb)

	if atomic.LoadInt32(&postCalls) != 1 {
		t.Fatalf("expected 1 POST (fallback PostFeedback), got %d", postCalls)
	}
}

func TestHandleFeedbackCallback_InvalidAction(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	// action not in {traded, skipped} → ignored, nothing recorded.
	cb := &telegram.CallbackQuery{ID: "cb", Data: "fb:AAPL:BUY:120000:hacked"}
	s.handleFeedbackCallback(context.Background(), cb)
	if len(s.GetFeedbackLog()) != 0 {
		t.Fatalf("invalid action should not be recorded, got %v", s.GetFeedbackLog())
	}
}

// ---------------------------------------------------------------------------
// pollFeedbackCallbacks — one delivery then stop
// ---------------------------------------------------------------------------

func TestPollFeedbackCallbacks_DeliversAndStops(t *testing.T) {
	var served int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Telegram method is the last path segment (e.g. /bot<token>/getUpdates).
		if strings.HasSuffix(r.URL.Path, "/answerCallbackQuery") {
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
			return
		}
		// getUpdates: return the callback only on the first poll, then empty so
		// the callback isn't re-delivered on every iteration.
		if atomic.AddInt32(&served, 1) == 1 {
			_ = json.NewEncoder(w).Encode(telegram.GetUpdatesResponse{
				OK: true,
				Result: []telegram.Update{
					{
						UpdateID:      1,
						CallbackQuery: &telegram.CallbackQuery{ID: "cbq", Data: "fb:AAPL:BUY:120000:traded"},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(telegram.GetUpdatesResponse{OK: true})
	}))
	defer srv.Close()

	s := newServiceWithStock(t, srv.URL)

	go s.pollFeedbackCallbacks()

	// Wait for the callback to be recorded in the feedback log.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := s.GetFeedbackLog()["AAPL:BUY:120000"]; ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(s.stopFeedback)
	time.Sleep(50 * time.Millisecond)

	if _, ok := s.GetFeedbackLog()["AAPL:BUY:120000"]; !ok {
		t.Fatal("expected feedback recorded from polled callback")
	}
}

// ---------------------------------------------------------------------------
// retryFailedFeedbackOnce
// ---------------------------------------------------------------------------

func TestRetryFailedFeedbackOnce_PersistsFailedEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/feedback") {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 77})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()

	s := newServiceWithStock(t, srv.URL)
	s.feedbackLog["AAPL:BUY"] = models.FeedbackEntry{Symbol: "AAPL", Signal: "BUY", Action: "skipped", FeedbackID: 0, Timestamp: time.Now()}
	s.feedbackLog["GOOG:BUY"] = models.FeedbackEntry{Symbol: "GOOG", Signal: "BUY", Action: "traded", FeedbackID: 42, Timestamp: time.Now()}

	s.retryFailedFeedbackOnce()

	got := s.GetFeedbackLog()
	if got["AAPL:BUY"].FeedbackID != 77 {
		t.Fatalf("expected AAPL retried to id 77, got %d", got["AAPL:BUY"].FeedbackID)
	}
	if got["GOOG:BUY"].FeedbackID != 42 {
		t.Fatalf("GOOG should be untouched, got %d", got["GOOG:BUY"].FeedbackID)
	}
}

func TestRetryFailedFeedbackOnce_NilStockClientNoOp(t *testing.T) {
	s, srv := newTestService(t) // stockClient is nil
	defer srv.Close()
	s.feedbackLog["X:BUY"] = models.FeedbackEntry{Symbol: "X", Signal: "BUY", FeedbackID: 0, Timestamp: time.Now()}
	s.retryFailedFeedbackOnce() // must not panic
	if s.GetFeedbackLog()["X:BUY"].FeedbackID != 0 {
		t.Fatal("expected no change with nil stock client")
	}
}

func TestRetryFailedFeedbackOnce_PostFailsKeepsZero(t *testing.T) {
	// Stock-service returns non-201 → PostFeedback returns 0 → entry unchanged.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := newServiceWithStock(t, srv.URL)
	s.feedbackLog["F:BUY"] = models.FeedbackEntry{Symbol: "F", Signal: "BUY", FeedbackID: 0, Timestamp: time.Now()}
	s.retryFailedFeedbackOnce()
	if s.GetFeedbackLog()["F:BUY"].FeedbackID != 0 {
		t.Fatal("expected FeedbackID to remain 0 after failed retry")
	}
}

func TestPollFeedbackCallbacks_StopBeforePoll(t *testing.T) {
	s, srv := newTestService(t)
	defer srv.Close()
	close(s.stopFeedback)
	// Should return immediately via the stop branch.
	done := make(chan struct{})
	go func() { s.pollFeedbackCallbacks(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pollFeedbackCallbacks did not return after stop")
	}
}
