package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	telegramSendURL       = "https://api.telegram.org/bot%s/sendMessage"
	telegramGetUpdatesURL = "https://api.telegram.org/bot%s/getUpdates"
	telegramAnswerCBURL   = "https://api.telegram.org/bot%s/answerCallbackQuery"
)

// Client handles Telegram Bot API interactions
type Client struct {
	botToken   string
	chatID     int64
	httpClient *http.Client
}

// NewClient creates a new Telegram client
func NewClient(botToken string, chatID int64) *Client {
	return &Client{
		botToken: botToken,
		chatID:   chatID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// InlineKeyboardButton represents a Telegram inline keyboard button
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// InlineKeyboardMarkup represents a Telegram inline keyboard
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// SendMessageRequest represents a Telegram sendMessage request
type SendMessageRequest struct {
	ChatID      int64                 `json:"chat_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

// SendMessageResponse represents a Telegram API response
type SendMessageResponse struct {
	OK          bool           `json:"ok"`
	Description string         `json:"description,omitempty"`
	Result      *MessageResult `json:"result,omitempty"`
}

// MessageResult contains the sent message metadata
type MessageResult struct {
	MessageID int64 `json:"message_id"`
}

// CallbackQuery represents an incoming callback query from an inline button press
type CallbackQuery struct {
	ID      string           `json:"id"`
	Data    string           `json:"data"`
	Message *CallbackMessage `json:"message,omitempty"`
}

// CallbackMessage is a minimal message struct for callback context
type CallbackMessage struct {
	MessageID int64 `json:"message_id"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

// GetUpdatesResponse represents the Telegram getUpdates API response
type GetUpdatesResponse struct {
	OK     bool     `json:"ok"`
	Result []Update `json:"result"`
}

// Update represents a single Telegram update
type Update struct {
	UpdateID      int64          `json:"update_id"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// SendMessage sends a message to the configured chat
func (c *Client) SendMessage(ctx context.Context, message string) error {
	return c.SendMessageWithParseMode(ctx, message, "HTML")
}

// SendMessageWithParseMode sends a message with a specific parse mode, with retry.
func (c *Client) SendMessageWithParseMode(ctx context.Context, message, parseMode string) error {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt*attempt) * time.Second // 1s, 4s
			log.Printf("Telegram send attempt %d/%d failed, retrying in %s: %v", attempt, maxRetries, delay, lastErr)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		if err := c.doSend(ctx, message, parseMode); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("telegram send failed after %d attempts: %w", maxRetries, lastErr)
}

func (c *Client) doSend(ctx context.Context, message, parseMode string) error {
	url := fmt.Sprintf(telegramSendURL, c.botToken)

	reqBody := SendMessageRequest{
		ChatID:    c.chatID,
		Text:      message,
		ParseMode: parseMode,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var response SendMessageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if !response.OK {
		return fmt.Errorf("telegram API error: %s", response.Description)
	}

	log.Printf("Telegram message sent successfully (chat_id=%d)", c.chatID)
	return nil
}

// SendMessageWithKeyboard sends a message with inline keyboard buttons and returns the message ID.
// Retries up to 3 times with exponential backoff on transient failures.
func (c *Client) SendMessageWithKeyboard(ctx context.Context, message string, keyboard *InlineKeyboardMarkup) (int64, error) {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt*attempt) * time.Second // 1s, 4s
			log.Printf("Telegram keyboard send attempt %d/%d failed, retrying in %s: %v", attempt, maxRetries, delay, lastErr)
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(delay):
			}
		}
		messageID, err := c.doSendWithKeyboard(ctx, message, keyboard)
		if err != nil {
			lastErr = err
			continue
		}
		return messageID, nil
	}
	return 0, fmt.Errorf("telegram keyboard send failed after %d attempts: %w", maxRetries, lastErr)
}

func (c *Client) doSendWithKeyboard(ctx context.Context, message string, keyboard *InlineKeyboardMarkup) (int64, error) {
	url := fmt.Sprintf(telegramSendURL, c.botToken)

	reqBody := SendMessageRequest{
		ChatID:      c.chatID,
		Text:        message,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	var response SendMessageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if !response.OK {
		return 0, fmt.Errorf("telegram API error: %s", response.Description)
	}

	var messageID int64
	if response.Result != nil {
		messageID = response.Result.MessageID
	}
	return messageID, nil
}

// GetUpdates polls Telegram for new updates (callback queries, etc.)
func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	url := fmt.Sprintf(telegramGetUpdatesURL, c.botToken)

	reqBody := map[string]interface{}{
		"offset":          offset,
		"timeout":         5,
		"allowed_updates": []string{"callback_query"},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var response GetUpdatesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if !response.OK {
		return nil, fmt.Errorf("telegram getUpdates failed")
	}

	return response.Result, nil
}

// AnswerCallbackQuery acknowledges a callback query to remove the loading spinner
func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID string, text string) error {
	url := fmt.Sprintf(telegramAnswerCBURL, c.botToken)

	reqBody := map[string]string{
		"callback_query_id": callbackID,
		"text":              text,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

// SendMarkdownMessage sends a message with Markdown formatting
func (c *Client) SendMarkdownMessage(ctx context.Context, message string) error {
	return c.SendMessageWithParseMode(ctx, message, "MarkdownV2")
}

// SetHTTPClientForTest allows tests to inject a custom HTTP client (e.g. one
// that routes requests to an httptest.Server). This is not intended for
// production use.
func (c *Client) SetHTTPClientForTest(hc *http.Client) {
	c.httpClient = hc
}

// Close releases resources held by the Telegram client (e.g. idle HTTP
// connections). It is safe to call multiple times.
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}
