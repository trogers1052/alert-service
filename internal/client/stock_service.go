package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/trogers1052/alert-service/internal/metrics"
)

// StockServiceClient sends feedback entries to the stock-service REST API
// for permanent PostgreSQL storage.
type StockServiceClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	metrics    metrics.Recorder
}

// NewStockServiceClient creates a client for the stock-service feedback API.
// Returns nil if baseURL is empty.
func NewStockServiceClient(baseURL string, opts ...string) *StockServiceClient {
	if baseURL == "" {
		return nil
	}
	apiKey := ""
	if len(opts) > 0 {
		apiKey = opts[0]
	}
	return &StockServiceClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		metrics: metrics.Nop{},
	}
}

// SetMetrics configures the metrics recorder for this client.
func (c *StockServiceClient) SetMetrics(m metrics.Recorder) {
	if m != nil {
		c.metrics = m
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
	EntryPrice         float64  `json:"entry_price,omitempty"`
	StopPrice          float64  `json:"stop_price,omitempty"`
	Target1            float64  `json:"target_1,omitempty"`
	Target2            float64  `json:"target_2,omitempty"`
	ValidUntil         string   `json:"valid_until,omitempty"`
}

type feedbackResponse struct {
	ID int `json:"id"`
}

// FeedbackParams holds optional trade plan data for feedback persistence.
type FeedbackParams struct {
	EntryPrice float64
	StopPrice  float64
	Target1    float64
	Target2    float64
	ValidUntil string
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
	params *FeedbackParams,
) int {
	req := feedbackRequest{
		Symbol:             symbol,
		Signal:             signal,
		Action:             action,
		Confidence:         confidence,
		RulesTriggered:     rulesTriggered,
		RegimeID:           regimeID,
		DecisionConfidence: decisionConfidence,
	}
	if params != nil {
		req.EntryPrice = params.EntryPrice
		req.StopPrice = params.StopPrice
		req.Target1 = params.Target1
		req.Target2 = params.Target2
		req.ValidUntil = params.ValidUntil
	}
	body, err := json.Marshal(req)
	if err != nil {
		log.Printf("WARNING: failed to marshal feedback for stock-service: %v", err)
		c.metrics.IncFeedbackPostErrors()
		return 0
	}

	url := fmt.Sprintf("%s/api/v1/feedback", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("WARNING: failed to create feedback request: %v", err)
		c.metrics.IncFeedbackPostErrors()
		return 0
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("X-API-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Printf("WARNING: failed to POST feedback to stock-service: %v", err)
		c.metrics.IncFeedbackPostErrors()
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		log.Printf("WARNING: stock-service feedback returned status %d", resp.StatusCode)
		c.metrics.IncFeedbackPostErrors()
		return 0
	}

	var fbResp feedbackResponse
	if err := json.NewDecoder(resp.Body).Decode(&fbResp); err != nil {
		log.Printf("WARNING: failed to decode feedback response: %v", err)
		c.metrics.IncFeedbackPostErrors()
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
		c.metrics.IncFeedbackPostErrors()
		return
	}

	url := fmt.Sprintf("%s/api/v1/feedback/%d", c.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("WARNING: failed to create feedback update request: %v", err)
		c.metrics.IncFeedbackPostErrors()
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("WARNING: failed to PUT feedback update to stock-service: %v", err)
		c.metrics.IncFeedbackPostErrors()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("WARNING: stock-service feedback update returned status %d", resp.StatusCode)
		c.metrics.IncFeedbackPostErrors()
	}
}
