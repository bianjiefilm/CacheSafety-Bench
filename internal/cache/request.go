package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Tool struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema,omitempty"`
}

type Request struct {
	Model          string            `json:"model"`
	SystemPrompt   string            `json:"system_prompt,omitempty"`
	Messages       []Message         `json:"messages"`
	Tools          []Tool            `json:"tools,omitempty"`
	Temperature    *float64          `json:"temperature,omitempty"`
	MaxTokens      *int              `json:"max_tokens,omitempty"`
	ResponseFormat string            `json:"response_format,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Extra          map[string]any    `json:"extra,omitempty"`
}

type Response struct {
	Text                       string      `json:"text"`
	TeacherScore               float64     `json:"teacher_score,omitempty"`
	ExactServeQualityDecision  string      `json:"exact_serve_quality_decision,omitempty"`
	ExactServeQualityCheckedAt string      `json:"exact_serve_quality_checked_at,omitempty"`
	HitCount                   int         `json:"hit_count,omitempty"`
	LastRefreshed              time.Time   `json:"last_refreshed,omitempty"`
	PromptTokens               int         `json:"prompt_tokens,omitempty"`
	CompletionTokens           int         `json:"completion_tokens,omitempty"`
	TotalTokens                int         `json:"total_tokens,omitempty"`
	CostUSD                    float64     `json:"cost_usd,omitempty"`
	LatencyMS                  int64       `json:"latency_ms,omitempty"`
	Observation                Observation `json:"observation,omitempty"`
}

func PromptText(req Request) string {
	var parts []string
	if strings.TrimSpace(req.SystemPrompt) != "" {
		parts = append(parts, req.SystemPrompt)
	}
	for _, msg := range req.Messages {
		parts = append(parts, msg.Content)
	}
	return strings.Join(parts, "\n")
}

func exactKey(req Request) string {
	return hashJSON(req)
}

func canonicalKey(req Request) string {
	normalized := req
	normalized.SystemPrompt = normalizeWhitespace(req.SystemPrompt)
	normalized.Messages = make([]Message, len(req.Messages))
	for i, msg := range req.Messages {
		normalized.Messages[i] = Message{
			Role:    strings.TrimSpace(msg.Role),
			Content: normalizeWhitespace(msg.Content),
		}
	}
	normalized.Metadata = canonicalMetadata(req.Metadata)
	return hashJSON(normalized)
}

func canonicalMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		switch normalizedKey {
		case "request_id", "trace_id", "session_id", "metadata_timestamp":
			continue
		default:
			out[normalizedKey] = strings.TrimSpace(value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func hashJSON(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
