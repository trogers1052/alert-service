package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// telegramOK returns a handler that responds with a successful Telegram API response
func telegramOK(messageID int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := SendMessageResponse{
			OK: true,
			Result: &MessageResult{
				MessageID: messageID,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// telegramError returns a handler that responds with a Telegram API error
func telegramError(desc string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := SendMessageResponse{
			OK:          false,
			Description: desc,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// ---------------------------------------------------------------------------
// NewClient
// ---------------------------------------------------------------------------

func TestNewClient(t *testing.T) {
	c := NewClient("my-token", 42)
	assert.Equal(t, "my-token", c.botToken)
	assert.Equal(t, int64(42), c.chatID)
	assert.NotNil(t, c.httpClient)
}

// ---------------------------------------------------------------------------
// SendMessage / SendMessageWithParseMode (via doSend)
// ---------------------------------------------------------------------------

func TestSendMessage_WithMockServer(t *testing.T) {
	var receivedReq SendMessageRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedReq))

		telegramOK(100)(w, r)
	}))
	defer server.Close()

	// Replace the telegramSendURL temporarily by creating a client
	// that constructs the correct URL. We'll do this by using a
	// RoundTripper that redirects requests to our test server.
	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	err := c.SendMessage(context.Background(), "<b>Hello</b> world")
	require.NoError(t, err)

	assert.Equal(t, int64(42), receivedReq.ChatID)
	assert.Equal(t, "<b>Hello</b> world", receivedReq.Text)
	assert.Equal(t, "HTML", receivedReq.ParseMode)
}

func TestSendMessage_APIError(t *testing.T) {
	server := httptest.NewServer(telegramError("Bad Request: chat not found"))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	err := c.SendMessage(context.Background(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chat not found")
}

func TestSendMessageWithKeyboard_Success(t *testing.T) {
	var receivedReq SendMessageRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedReq)
		telegramOK(200)(w, r)
	}))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "Yes", CallbackData: "yes"},
				{Text: "No", CallbackData: "no"},
			},
		},
	}

	msgID, err := c.SendMessageWithKeyboard(context.Background(), "Choose:", kb)
	require.NoError(t, err)
	assert.Equal(t, int64(200), msgID)
	assert.Equal(t, "HTML", receivedReq.ParseMode)
	require.NotNil(t, receivedReq.ReplyMarkup)
	assert.Equal(t, 1, len(receivedReq.ReplyMarkup.InlineKeyboard))
	assert.Equal(t, 2, len(receivedReq.ReplyMarkup.InlineKeyboard[0]))
}

func TestSendMessageWithKeyboard_APIError(t *testing.T) {
	server := httptest.NewServer(telegramError("message too long"))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	_, err := c.SendMessageWithKeyboard(context.Background(), "test", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message too long")
}

// ---------------------------------------------------------------------------
// GetUpdates
// ---------------------------------------------------------------------------

func TestGetUpdates_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GetUpdatesResponse{
			OK: true,
			Result: []Update{
				{
					UpdateID: 100,
					CallbackQuery: &CallbackQuery{
						ID:   "cb1",
						Data: "fb:AAPL:BUY:120000:traded",
					},
				},
				{
					UpdateID: 101,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	updates, err := c.GetUpdates(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, 2, len(updates))
	assert.Equal(t, int64(100), updates[0].UpdateID)
	require.NotNil(t, updates[0].CallbackQuery)
	assert.Equal(t, "cb1", updates[0].CallbackQuery.ID)
	assert.Nil(t, updates[1].CallbackQuery)
}

func TestGetUpdates_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GetUpdatesResponse{OK: false}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	_, err := c.GetUpdates(context.Background(), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getUpdates failed")
}

// ---------------------------------------------------------------------------
// AnswerCallbackQuery
// ---------------------------------------------------------------------------

func TestAnswerCallbackQuery_Success(t *testing.T) {
	var receivedBody map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	err := c.AnswerCallbackQuery(context.Background(), "cb123", "Got it!")
	require.NoError(t, err)
	assert.Equal(t, "cb123", receivedBody["callback_query_id"])
	assert.Equal(t, "Got it!", receivedBody["text"])
}

// ---------------------------------------------------------------------------
// SendMarkdownMessage
// ---------------------------------------------------------------------------

func TestSendMarkdownMessage(t *testing.T) {
	var receivedReq SendMessageRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedReq)
		telegramOK(300)(w, r)
	}))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	err := c.SendMarkdownMessage(context.Background(), "*bold text*")
	require.NoError(t, err)
	assert.Equal(t, "MarkdownV2", receivedReq.ParseMode)
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestClose(t *testing.T) {
	c := NewClient("token", 42)
	// Should not panic
	c.Close()
	c.Close() // safe to call multiple times
}

// ---------------------------------------------------------------------------
// Retry logic (SendMessageWithParseMode)
// ---------------------------------------------------------------------------

func TestSendMessageWithParseMode_RetriesOnFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			telegramError("temporarily unavailable")(w, r)
			return
		}
		telegramOK(100)(w, r)
	}))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	err := c.SendMessageWithParseMode(context.Background(), "retry test", "HTML")
	require.NoError(t, err)
	assert.Equal(t, 3, attempts)
}

func TestSendMessageWithParseMode_FailsAfterMaxRetries(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		telegramError("always fails")(w, r)
	}))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	err := c.SendMessageWithParseMode(context.Background(), "fail test", "HTML")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed after 3 attempts")
	assert.Equal(t, 3, attempts)
}

func TestSendMessageWithParseMode_CancelledContextStopsRetries(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		telegramError("fail")(w, r)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	err := c.SendMessageWithParseMode(ctx, "cancel test", "HTML")
	require.Error(t, err)
	// Should bail out early due to context cancellation
	assert.LessOrEqual(t, attempts, 2) // at most 1 attempt + 1 retry before context check
}

// ---------------------------------------------------------------------------
// redirectTransport — routes all requests to the httptest server
// ---------------------------------------------------------------------------

type redirectTransport struct {
	target string
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace the host with our test server, keep path
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

// ---------------------------------------------------------------------------
// JSON response parsing edge cases
// ---------------------------------------------------------------------------

func TestSendMessage_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	err := c.SendMessage(context.Background(), "test")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "unmarshal") || strings.Contains(err.Error(), "failed"))
}

func TestSendMessageWithKeyboard_NilResult(t *testing.T) {
	// OK but no result — message ID should be 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := SendMessageResponse{OK: true, Result: nil}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := &Client{
		botToken: "test",
		chatID:   42,
		httpClient: &http.Client{
			Transport: &redirectTransport{target: server.URL},
		},
	}

	msgID, err := c.SendMessageWithKeyboard(context.Background(), "test", nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), msgID)
}
