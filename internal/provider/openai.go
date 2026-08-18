package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/shadowlog"
)

type OpenAIProvider struct {
	cfg        OpenAIConfig
	httpClient *http.Client
}

type openaiChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openaiChatMessage `json:"messages"`
	MaxTokens   *int                `json:"max_tokens,omitempty"`
	Temperature *float64            `json:"temperature,omitempty"`
}

type openaiChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

func NewOpenAIProvider(cfg OpenAIConfig, httpClient *http.Client) (*OpenAIProvider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("missing openai config: OPENAI_API_KEY is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = DefaultOpenAIBaseURL
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	}
	return &OpenAIProvider{cfg: cfg, httpClient: httpClient}, nil
}

func NewOpenAIProviderFromEnv() (*OpenAIProvider, OpenAIConfig, error) {
	cfg := LoadOpenAIConfigFromEnv()
	provider, err := NewOpenAIProvider(cfg, nil)
	return provider, cfg, err
}

func (p *OpenAIProvider) Complete(ctx context.Context, req cachepkg.Request) (Result, error) {
	if p == nil || p.httpClient == nil {
		return Result{}, errors.New("missing openai config: provider is not initialized")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(p.cfg.Model)
	}
	if model == "" {
		return Result{}, errors.New("missing openai config: request model is required")
	}

	payload, err := json.Marshal(openaiChatRequest{
		Model:       model,
		Messages:    toOpenAIMessages(req),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	})
	if err != nil {
		return Result{}, fmt.Errorf("openai request encode error: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(p.cfg.BaseURL, "chat/completions"), bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("openai request build error: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := p.httpClient.Do(httpReq)
	latency := time.Since(start)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			return Result{}, fmt.Errorf("openai timeout: %w", context.DeadlineExceeded)
		}
		return Result{}, fmt.Errorf("openai request failed: %s", p.sanitizeError(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, fmt.Errorf("openai response read error: %s", p.sanitizeError(err.Error()))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("openai HTTP status %d: %s", resp.StatusCode, p.sanitizeError(string(body)))
	}

	var parsed openaiChatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Result{}, fmt.Errorf("openai JSON parse error: %s", p.sanitizeError(err.Error()))
	}
	text, finishReason := extractOpenAICompletion(parsed)
	observation := cachepkg.ParseGatewayHeaders(resp.Header)
	attachUsageObservation(&observation, body)
	normalized := Response{
		Text:         text,
		Content:      text,
		Model:        firstNonEmpty(parsed.Model, model),
		Usage:        parsed.Usage,
		RawResponse:  json.RawMessage(body),
		LatencyMS:    latency.Milliseconds(),
		FinishReason: finishReason,
	}
	shadowlog.FromEnv().LogBestEffort(ctx, shadowlog.Record{
		Model:        model,
		Messages:     shadowlog.MessagesFromRequest(req),
		Response:     normalized.Content,
		Usage:        shadowlog.Usage{PromptTokens: parsed.Usage.PromptTokens, CompletionTokens: parsed.Usage.CompletionTokens, TotalTokens: parsed.Usage.TotalTokens},
		Source:       "real_app_call",
		LatencyMS:    normalized.LatencyMS,
		Status:       "success",
		FinishReason: normalized.FinishReason,
	})
	return Result{
		Response:    normalized,
		Usage:       parsed.Usage,
		Cost:        normalized.CostUSD,
		Observation: observation,
	}, nil
}

func (p *OpenAIProvider) sanitizeError(value string) string {
	return sanitizeSecret(value, p.cfg.APIKey)
}

func toOpenAIMessages(req cachepkg.Request) []openaiChatMessage {
	var messages []openaiChatMessage
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, openaiChatMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		messages = append(messages, openaiChatMessage{Role: role, Content: msg.Content})
	}
	return messages
}

func extractOpenAICompletion(resp openaiChatResponse) (string, string) {
	if len(resp.Choices) == 0 {
		return "", ""
	}
	return resp.Choices[0].Message.Content, resp.Choices[0].FinishReason
}

func joinURL(base, path string) string {
	return strings.TrimRight(strings.TrimSpace(base), "/") + "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
}

func attachUsageObservation(obs *cachepkg.Observation, body []byte) {
	var envelope struct {
		Usage struct {
			PromptTokens        *int `json:"prompt_tokens"`
			PromptTokensDetails *struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return
	}
	obs.PromptTokens = envelope.Usage.PromptTokens
	if envelope.Usage.PromptTokensDetails != nil {
		obs.CachedTokens = envelope.Usage.PromptTokensDetails.CachedTokens
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
