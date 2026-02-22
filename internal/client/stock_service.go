package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// StockServiceClient sends feedback entries to the stock-service REST API
// for permanent PostgreSQL storage.
type StockServiceClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewStockServiceClient creates a client for the stock-service feedback API.
// Returns nil if baseURL is empty.
func NewStockServiceClient(baseURL string) *StockServiceClient {
	if baseURL == "" {
		return nil
	}
	return &StockServiceClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

type feedbackRequest struct {
	Symbol     string  `json:"symbol"`
	Signal     string  `json:"signal"`
	Action     string  `json:"action"`
	Confidence float64 `json:"confidence,omitempty"`
}

// PostFeedback sends a feedback entry to stock-service for PostgreSQL storage.
// This is best-effort: errors are logged but do not propagate.
func (c *StockServiceClient) PostFeedback(ctx context.Context, symbol, signal, action string, confidence float64) {
	body, err := json.Marshal(feedbackRequest{
		Symbol:     symbol,
		Signal:     signal,
		Action:     action,
		Confidence: confidence,
	})
	if err != nil {
		log.Printf("WARNING: failed to marshal feedback for stock-service: %v", err)
		return
	}

	url := fmt.Sprintf("%s/api/v1/feedback", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("WARNING: failed to create feedback request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("WARNING: failed to POST feedback to stock-service: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		log.Printf("WARNING: stock-service feedback returned status %d", resp.StatusCode)
	}
}
