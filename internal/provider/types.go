package provider

import (
	"context"
	"encoding/json"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Response struct {
	Text         string          `json:"text"`
	Content      string          `json:"content"`
	Model        string          `json:"model"`
	Usage        Usage           `json:"usage"`
	RawResponse  json.RawMessage `json:"raw_response,omitempty"`
	LatencyMS    int64           `json:"latency_ms"`
	CostUSD      float64         `json:"cost_usd"`
	FinishReason string          `json:"finish_reason,omitempty"`
}

type Provider interface {
	Complete(ctx context.Context, req cachepkg.Request) (Result, error)
}

type Result struct {
	Response Response `json:"response"`
	Usage    Usage    `json:"usage"`
	Cost     float64  `json:"cost_usd"`
}

type FakeProvider struct {
	Result Result
	Err    error
	Calls  int
}

func (p *FakeProvider) Complete(ctx context.Context, req cachepkg.Request) (Result, error) {
	p.Calls++
	if p.Err != nil {
		return Result{}, p.Err
	}
	return p.Result, nil
}
