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

const telegramAPIURL = "https://api.telegram.org/bot%s/sendMessage"

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

// SendMessageRequest represents a Telegram sendMessage request
type SendMessageRequest struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

// SendMessageResponse represents a Telegram API response
type SendMessageResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
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
	url := fmt.Sprintf(telegramAPIURL, c.botToken)

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

	return nil
}

// SendMarkdownMessage sends a message with Markdown formatting
func (c *Client) SendMarkdownMessage(ctx context.Context, message string) error {
	return c.SendMessageWithParseMode(ctx, message, "MarkdownV2")
}
