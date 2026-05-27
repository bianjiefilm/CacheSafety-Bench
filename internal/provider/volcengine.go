package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/shadowlog"

	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

type VolcengineProvider struct {
	cfg    VolcengineConfig
	client *arkruntime.Client
}

var (
	accountIDPattern = regexp.MustCompile(`account %!s\(int64=\d+\)`)
	requestIDPattern = regexp.MustCompile(`Request id: [A-Za-z0-9_-]+`)
)

func NewVolcengineProvider(cfg VolcengineConfig) (*VolcengineProvider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("missing volcengine config: VOLCENGINE_API_KEY is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}

	options := []arkruntime.ConfigOption{
		arkruntime.WithTimeout(cfg.Timeout),
	}
	if strings.TrimSpace(cfg.BaseURL) != "" {
		options = append(options, arkruntime.WithBaseUrl(cfg.BaseURL))
	}

	return &VolcengineProvider{
		cfg:    cfg,
		client: arkruntime.NewClientWithApiKey(cfg.APIKey, options...),
	}, nil
}

func NewVolcengineProviderFromEnv() (*VolcengineProvider, VolcengineConfig, error) {
	cfg := LoadVolcengineConfigFromEnv()
	provider, err := NewVolcengineProvider(cfg)
	return provider, cfg, err
}

func (p *VolcengineProvider) Complete(ctx context.Context, req cachepkg.Request) (Result, error) {
	if p == nil || p.client == nil {
		return Result{}, errors.New("missing volcengine config: provider is not initialized")
	}
	if strings.TrimSpace(req.Model) == "" {
		return Result{}, errors.New("missing volcengine config: request model is required")
	}

	request := arkmodel.CreateChatCompletionRequest{
		Model:       req.Model,
		Messages:    toArkMessages(req),
		MaxTokens:   req.MaxTokens,
		Temperature: toFloat32Ptr(req.Temperature),
	}

	start := time.Now()
	resp, err := p.client.CreateChatCompletion(ctx, request)
	latency := time.Since(start)
	if err != nil {
		return Result{}, p.wrapError(err, req.Model)
	}

	text, finishReason := extractChatCompletion(resp)
	usage := Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return Result{}, fmt.Errorf("volcengine JSON parse error: marshal raw response: %w", err)
	}

	normalized := Response{
		Text:         text,
		Content:      text,
		Model:        resp.Model,
		Usage:        usage,
		RawResponse:  raw,
		LatencyMS:    latency.Milliseconds(),
		CostUSD:      0,
		FinishReason: finishReason,
	}
	shadowlog.FromEnv().LogBestEffort(ctx, shadowlog.Record{
		Model:        req.Model,
		Messages:     shadowlog.MessagesFromRequest(req),
		Response:     normalized.Content,
		Usage:        shadowlog.Usage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens},
		Source:       "real_app_call",
		LatencyMS:    normalized.LatencyMS,
		Status:       "success",
		FinishReason: normalized.FinishReason,
	})
	return Result{
		Response: normalized,
		Usage:    usage,
		Cost:     normalized.CostUSD,
	}, nil
}

func (p *VolcengineProvider) wrapError(err error, requestModel string) error {
	msg := p.sanitizeError(err.Error(), requestModel)
	var apiErr *arkmodel.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("volcengine upstream error response: status=%d code=%s message=%s", apiErr.HTTPStatusCode, apiErr.Code, p.sanitizeError(apiErr.Message, requestModel))
	}
	var requestErr *arkmodel.RequestError
	if errors.As(err, &requestErr) {
		if errors.Is(requestErr.Err, context.DeadlineExceeded) {
			return fmt.Errorf("volcengine timeout: %w", context.DeadlineExceeded)
		}
		if requestErr.HTTPStatusCode < 200 || requestErr.HTTPStatusCode >= 300 {
			return fmt.Errorf("volcengine HTTP status %d: %s", requestErr.HTTPStatusCode, p.sanitizeError(requestErr.Err.Error(), requestModel))
		}
		return fmt.Errorf("volcengine request error: %s", msg)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("volcengine timeout: %w", context.DeadlineExceeded)
	}
	return fmt.Errorf("volcengine request failed: %s", msg)
}

func (p *VolcengineProvider) sanitizeError(value string, requestModel string) string {
	value = sanitizeSecret(value, p.cfg.APIKey)
	value = accountIDPattern.ReplaceAllString(value, "account [redacted]")
	value = requestIDPattern.ReplaceAllString(value, "Request id: [redacted]")
	models := append([]string(nil), p.cfg.Models...)
	models = append(models, requestModel)
	for i, model := range models {
		model = strings.TrimSpace(model)
		if model != "" {
			value = strings.ReplaceAll(value, model, fmt.Sprintf("[volcengine_model_%d]", i+1))
		}
	}
	return value
}

func toArkMessages(req cachepkg.Request) []*arkmodel.ChatCompletionMessage {
	var messages []*arkmodel.ChatCompletionMessage
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, arkMessage(arkmodel.ChatMessageRoleSystem, req.SystemPrompt))
	}
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = arkmodel.ChatMessageRoleUser
		}
		messages = append(messages, arkMessage(role, msg.Content))
	}
	return messages
}

func arkMessage(role, content string) *arkmodel.ChatCompletionMessage {
	text := content
	return &arkmodel.ChatCompletionMessage{
		Role: role,
		Content: &arkmodel.ChatCompletionMessageContent{
			StringValue: &text,
		},
	}
}

func toFloat32Ptr(value *float64) *float32 {
	if value == nil {
		return nil
	}
	out := float32(*value)
	return &out
}

func extractChatCompletion(resp arkmodel.ChatCompletionResponse) (string, string) {
	if len(resp.Choices) == 0 || resp.Choices[0] == nil {
		return "", ""
	}
	choice := resp.Choices[0]
	content := contentToString(choice.Message.Content)
	return content, string(choice.FinishReason)
}

func contentToString(content *arkmodel.ChatCompletionMessageContent) string {
	if content == nil {
		return ""
	}
	if content.StringValue != nil {
		return *content.StringValue
	}
	if len(content.ListValue) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range content.ListValue {
		if part != nil && part.Text != "" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func sanitizeSecret(value, apiKey string) string {
	if apiKey == "" {
		return value
	}
	return strings.ReplaceAll(value, apiKey, "[redacted]")
}
