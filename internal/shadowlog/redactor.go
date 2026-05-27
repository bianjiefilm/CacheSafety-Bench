package shadowlog

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

var (
	emailPattern         = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	urlPattern           = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+`)
	phonePattern         = regexp.MustCompile(`(?i)(?:\+?\d[\d\s().-]{7,}\d)`)
	longNumberPattern    = regexp.MustCompile(`\b\d{8,}\b`)
	bearerPattern        = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	authorizationPattern = regexp.MustCompile(`(?i)\bAuthorization\s*:\s*[^\s]+(?:\s+[^\s]+)?`)
	apiKeyPattern        = regexp.MustCompile(`(?i)\b(?:api[_-]?key|token|secret)\s*[:=]\s*[A-Za-z0-9._~+/=-]+`)
	arkKeyPattern        = regexp.MustCompile(`\bark-[A-Za-z0-9-]{12,}\b`)
	secretMarkerPattern  = regexp.MustCompile(`(?i)\b(?:Authorization|Bearer|api[_-]?key|secret)\b\s*[:=]?`)
)

type RedactionResult struct {
	Value   string
	Changed bool
}

func RedactText(value string) RedactionResult {
	out := value
	out = authorizationPattern.ReplaceAllString(out, "[REDACTED_SECRET]")
	out = bearerPattern.ReplaceAllString(out, "[REDACTED_SECRET]")
	out = apiKeyPattern.ReplaceAllString(out, "[REDACTED_SECRET]")
	out = arkKeyPattern.ReplaceAllString(out, "[REDACTED_SECRET]")
	out = secretMarkerPattern.ReplaceAllString(out, "[REDACTED_SECRET]")
	out = emailPattern.ReplaceAllString(out, "[REDACTED_EMAIL]")
	out = urlPattern.ReplaceAllString(out, "[REDACTED_URL]")
	out = longNumberPattern.ReplaceAllString(out, "[REDACTED_NUMBER]")
	out = phonePattern.ReplaceAllString(out, "[REDACTED_PHONE]")
	return RedactionResult{Value: out, Changed: out != value}
}

func RedactMessages(messages []cachepkg.Message) ([]cachepkg.Message, bool) {
	out := make([]cachepkg.Message, 0, len(messages))
	changed := false
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		result := RedactText(msg.Content)
		changed = changed || result.Changed
		out = append(out, cachepkg.Message{Role: role, Content: result.Value})
	}
	return out, changed
}

func MessagesFromRequest(req cachepkg.Request) []cachepkg.Message {
	messages := make([]cachepkg.Message, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.SystemPrompt) != "" {
		messages = append(messages, cachepkg.Message{Role: "system", Content: req.SystemPrompt})
	}
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		messages = append(messages, cachepkg.Message{Role: role, Content: msg.Content})
	}
	return messages
}

func ModelAlias(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "model_unknown"
	}
	if strings.HasPrefix(model, "model_") || model == "fake" || model == "test-model" {
		return model
	}
	sum := sha256.Sum256([]byte(model))
	return "model_" + hex.EncodeToString(sum[:])[:8]
}
