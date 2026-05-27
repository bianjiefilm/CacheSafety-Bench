package shadowlog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

const (
	EnvEnableShadowTrafficLog = "ENABLE_SHADOW_TRAFFIC_LOG"
	EnvShadowTrafficLogPath   = "SHADOW_TRAFFIC_LOG_PATH"
	DefaultShadowTrafficPath  = "data/shadow/real_logs_v0.jsonl"
)

type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type Record struct {
	Timestamp        string             `json:"timestamp"`
	Model            string             `json:"model"`
	Messages         []cachepkg.Message `json:"messages,omitempty"`
	Response         string             `json:"response,omitempty"`
	Usage            Usage              `json:"usage,omitempty"`
	Source           string             `json:"source"`
	RedactionApplied bool               `json:"redaction_applied"`
	LatencyMS        int64              `json:"latency_ms,omitempty"`
	Status           string             `json:"status,omitempty"`
	FinishReason     string             `json:"finish_reason,omitempty"`
	ErrorType        string             `json:"error_type,omitempty"`
	CategoryHint     string             `json:"category_hint,omitempty"`
}

type Logger struct {
	enabled bool
	path    string
	now     func() time.Time
}

func FromEnv() Logger {
	path := strings.TrimSpace(os.Getenv(EnvShadowTrafficLogPath))
	if path == "" {
		path = DefaultShadowTrafficPath
	}
	return Logger{
		enabled: strings.TrimSpace(os.Getenv(EnvEnableShadowTrafficLog)) == "1",
		path:    path,
		now:     time.Now,
	}
}

func New(enabled bool, path string) Logger {
	if strings.TrimSpace(path) == "" {
		path = DefaultShadowTrafficPath
	}
	return Logger{enabled: enabled, path: path, now: time.Now}
}

func (l Logger) Enabled() bool {
	return l.enabled
}

func (l Logger) Path() string {
	return l.path
}

func (l Logger) Log(ctx context.Context, record Record) error {
	if !l.enabled {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.now == nil {
		l.now = time.Now
	}
	if record.Timestamp == "" {
		record.Timestamp = l.now().UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(record.Source) == "" {
		record.Source = "real_app_call"
	}
	originalModel := strings.TrimSpace(record.Model)
	record.Model = ModelAlias(record.Model)
	if record.Model != originalModel {
		record.RedactionApplied = true
	}
	var messagesChanged bool
	record.Messages, messagesChanged = RedactMessages(record.Messages)
	record.RedactionApplied = record.RedactionApplied || messagesChanged
	if response := RedactText(record.Response); response.Changed {
		record.Response = response.Value
		record.RedactionApplied = true
	}
	if errType := RedactText(record.ErrorType); errType.Changed {
		record.ErrorType = errType.Value
		record.RedactionApplied = true
	}
	if finishReason := RedactText(record.FinishReason); finishReason.Changed {
		record.FinishReason = finishReason.Value
		record.RedactionApplied = true
	}
	if hint := RedactText(record.CategoryHint); hint.Changed {
		record.CategoryHint = hint.Value
		record.RedactionApplied = true
	}

	dir := filepath.Dir(l.path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return json.NewEncoder(file).Encode(record)
}

func (l Logger) LogBestEffort(ctx context.Context, record Record) {
	_ = l.Log(ctx, record)
}
