package embedding

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

const (
	EnvVolcengineEmbeddingModel = "VOLCENGINE_EMBEDDING_MODEL"
	EnvRunEmbeddingIntegration  = "RUN_EMBEDDING_INTEGRATION"
	envVolcengineAPIKey         = "VOLCENGINE_API_KEY"
	envVolcengineBaseURL        = "VOLCENGINE_BASE_URL"
	defaultEmbeddingTimeout     = 60 * time.Second
)

type VolcengineEmbedder struct {
	model  string
	apiKey string
	client *arkruntime.Client
}

var embeddingRequestIDPattern = regexp.MustCompile(`Request id: [A-Za-z0-9_-]+`)

func NewVolcengineEmbedder(apiKey string, model string, baseURL string, timeout time.Duration) (*VolcengineEmbedder, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("missing volcengine embedding config: VOLCENGINE_API_KEY is required")
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("missing volcengine embedding config: VOLCENGINE_EMBEDDING_MODEL is required")
	}
	if timeout <= 0 {
		timeout = defaultEmbeddingTimeout
	}
	options := []arkruntime.ConfigOption{arkruntime.WithTimeout(timeout)}
	if strings.TrimSpace(baseURL) != "" {
		options = append(options, arkruntime.WithBaseUrl(baseURL))
	}
	return &VolcengineEmbedder{
		model:  model,
		apiKey: apiKey,
		client: arkruntime.NewClientWithApiKey(apiKey, options...),
	}, nil
}

func NewVolcengineEmbedderFromEnv(modelOverride string) (*VolcengineEmbedder, bool, error) {
	if os.Getenv(EnvRunEmbeddingIntegration) != "1" {
		return nil, false, nil
	}
	model := strings.TrimSpace(modelOverride)
	if model == "" {
		model = strings.TrimSpace(os.Getenv(EnvVolcengineEmbeddingModel))
	}
	if model == "" {
		return nil, false, nil
	}
	embedder, err := NewVolcengineEmbedder(
		os.Getenv(envVolcengineAPIKey),
		model,
		os.Getenv(envVolcengineBaseURL),
		defaultEmbeddingTimeout,
	)
	return embedder, true, err
}

func (e *VolcengineEmbedder) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = e.model
	}
	res, err := e.client.CreateEmbeddings(ctx, arkmodel.EmbeddingRequestStrings{
		Input:          []string{req.Text},
		Model:          model,
		EncodingFormat: arkmodel.EmbeddingEncodingFormatFloat,
	})
	if err == nil {
		if len(res.Data) == 0 {
			return EmbedResult{}, fmt.Errorf("volcengine embedding response is empty")
		}
		vector := make([]float64, len(res.Data[0].Embedding))
		for i, value := range res.Data[0].Embedding {
			vector[i] = float64(value)
		}
		return EmbedResult{Vector: vector, Dim: len(vector), Model: res.Model}, nil
	}

	if !looksLikeUnsupportedEmbeddingAPI(err) {
		return EmbedResult{}, fmt.Errorf("volcengine embedding request failed: %s", e.sanitizeError(err.Error(), model))
	}
	multimodal, multiErr := e.embedMultimodal(ctx, model, req.Text)
	if multiErr != nil {
		return EmbedResult{}, fmt.Errorf("volcengine multimodal embedding request failed: %s", e.sanitizeError(multiErr.Error(), model))
	}
	return multimodal, nil
}

func (e *VolcengineEmbedder) embedMultimodal(ctx context.Context, model string, text string) (EmbedResult, error) {
	encoding := arkmodel.EmbeddingEncodingFormatFloat
	res, err := e.client.CreateMultiModalEmbeddings(ctx, arkmodel.MultiModalEmbeddingRequest{
		Model:          model,
		EncodingFormat: &encoding,
		Input: []arkmodel.MultimodalEmbeddingInput{{
			Type: arkmodel.MultiModalEmbeddingInputTypeText,
			Text: &text,
		}},
	})
	if err != nil {
		return EmbedResult{}, err
	}
	if len(res.Data.Embedding) == 0 {
		return EmbedResult{}, fmt.Errorf("volcengine multimodal embedding response is empty")
	}
	vector := make([]float64, len(res.Data.Embedding))
	for i, value := range res.Data.Embedding {
		vector[i] = float64(value)
	}
	return EmbedResult{Vector: vector, Dim: len(vector), Model: res.Model}, nil
}

func looksLikeUnsupportedEmbeddingAPI(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "does not support this api")
}

func (e *VolcengineEmbedder) sanitizeError(value string, model string) string {
	value = strings.ReplaceAll(value, e.apiKey, "[redacted]")
	value = strings.ReplaceAll(value, model, "[volcengine_embedding_model]")
	value = embeddingRequestIDPattern.ReplaceAllString(value, "Request id: [redacted]")
	return value
}
