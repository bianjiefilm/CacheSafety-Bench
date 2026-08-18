package provider

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvOpenAIAPIKey             = "OPENAI_API_KEY"
	EnvOpenAIBaseURL            = "OPENAI_BASE_URL"
	EnvOpenAIModel              = "OPENAI_MODEL"
	EnvVolcengineAPIKey         = "VOLCENGINE_API_KEY"
	EnvVolcengineBaseURL        = "VOLCENGINE_BASE_URL"
	EnvVolcengineModel1         = "VOLCENGINE_MODEL_1"
	EnvVolcengineModel2         = "VOLCENGINE_MODEL_2"
	EnvVolcengineModel3         = "VOLCENGINE_MODEL_3"
	EnvVolcengineModel4         = "VOLCENGINE_MODEL_4"
	EnvRunVolcengineIntegration = "RUN_VOLCENGINE_INTEGRATION"
	DefaultOpenAIBaseURL        = "https://api.openai.com/v1"
)

type OpenAIConfig struct {
	APIKey         string
	BaseURL        string
	Model          string
	Timeout        time.Duration
	RedactedAPIKey string
}

func LoadOpenAIConfigFromEnv() OpenAIConfig {
	apiKey := strings.TrimSpace(os.Getenv(EnvOpenAIAPIKey))
	baseURL := strings.TrimSpace(os.Getenv(EnvOpenAIBaseURL))
	if baseURL == "" {
		baseURL = DefaultOpenAIBaseURL
	}
	timeoutSeconds := getenvInt("OPENAI_TIMEOUT_SECONDS", 60)
	return OpenAIConfig{
		APIKey:         apiKey,
		BaseURL:        baseURL,
		Model:          strings.TrimSpace(os.Getenv(EnvOpenAIModel)),
		Timeout:        time.Duration(timeoutSeconds) * time.Second,
		RedactedAPIKey: redactSecret(apiKey),
	}
}

type VolcengineConfig struct {
	APIKey         string
	BaseURL        string
	Models         []string
	Timeout        time.Duration
	Enabled        bool
	RedactedAPIKey string
}

func LoadVolcengineConfigFromEnv() VolcengineConfig {
	apiKey := strings.TrimSpace(os.Getenv(EnvVolcengineAPIKey))
	timeoutSeconds := getenvInt("VOLCENGINE_TIMEOUT_SECONDS", 60)
	return VolcengineConfig{
		APIKey:  apiKey,
		BaseURL: strings.TrimSpace(os.Getenv(EnvVolcengineBaseURL)),
		Models: compactStrings([]string{
			os.Getenv(EnvVolcengineModel1),
			os.Getenv(EnvVolcengineModel2),
			os.Getenv(EnvVolcengineModel3),
			os.Getenv(EnvVolcengineModel4),
		}),
		Timeout:        time.Duration(timeoutSeconds) * time.Second,
		Enabled:        os.Getenv(EnvRunVolcengineIntegration) == "1",
		RedactedAPIKey: redactSecret(apiKey),
	}
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func redactSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "[redacted]"
	}
	return value[:4] + "...[redacted]..." + value[len(value)-4:]
}
