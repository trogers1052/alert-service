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
	Symbol             string   `json:"symbol"`
	Signal             string   `json:"signal"`
	Action             string   `json:"action"`
	Confidence         float64  `json:"confidence,omitempty"`
	RulesTriggered     []string `json:"rules_triggered,omitempty"`
	RegimeID           string   `json:"regime_id,omitempty"`
	DecisionConfidence float64  `json:"decision_confidence,omitempty"`
}

type feedbackResponse struct {
	ID int `json:"id"`
}

// PostFeedback sends a feedback entry to stock-service for PostgreSQL storage.
// Returns the feedback row ID (0 on failure). Errors are logged but do not propagate.
func (c *StockServiceClient) PostFeedback(
	ctx context.Context,
	symbol, signal, action string,
	confidence float64,
	rulesTriggered []string,
	regimeID string,
	decisionConfidence float64,
) int {
	body, err := json.Marshal(feedbackRequest{
		Symbol:             symbol,
		Signal:             signal,
		Action:             action,
		Confidence:         confidence,
		RulesTriggered:     rulesTriggered,
		RegimeID:           regimeID,
		DecisionConfidence: decisionConfidence,
	})
	if err != nil {
		log.Printf("WARNING: failed to marshal feedback for stock-service: %v", err)
		return 0
	}

	url := fmt.Sprintf("%s/api/v1/feedback", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("WARNING: failed to create feedback request: %v", err)
		return 0
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("WARNING: failed to POST feedback to stock-service: %v", err)
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		log.Printf("WARNING: stock-service feedback returned status %d", resp.StatusCode)
		return 0
	}

	var fbResp feedbackResponse
	if err := json.NewDecoder(resp.Body).Decode(&fbResp); err != nil {
		log.Printf("WARNING: failed to decode feedback response: %v", err)
		return 0
	}

	return fbResp.ID
}

// UpdateFeedbackAction updates the action field of an existing feedback row.
// Best-effort: errors are logged but do not propagate.
func (c *StockServiceClient) UpdateFeedbackAction(ctx context.Context, id int, action string) {
	body, err := json.Marshal(map[string]string{"action": action})
	if err != nil {
		log.Printf("WARNING: failed to marshal feedback update: %v", err)
		return
	}

	url := fmt.Sprintf("%s/api/v1/feedback/%d", c.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("WARNING: failed to create feedback update request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("WARNING: failed to PUT feedback update to stock-service: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("WARNING: stock-service feedback update returned status %d", resp.StatusCode)
	}
}
