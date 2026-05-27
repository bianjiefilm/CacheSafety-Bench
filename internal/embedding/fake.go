package embedding

import (
	"context"
	"strings"
)

type FakeEmbedder struct{}

func (e FakeEmbedder) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	text := strings.ToLower(req.Text)
	vector := []float64{0, 1, 0}
	switch {
	case strings.Contains(text, "semantic cache reuse"):
		vector = []float64{1, 0, 0}
	case strings.Contains(text, "semantic cache trap"):
		vector = []float64{0.94, 0.341174442, 0}
	case strings.Contains(text, "exact cache"):
		vector = []float64{0, 0, 1}
	case strings.Contains(text, "canonical"):
		vector = []float64{0, 0.5, 0.5}
	default:
		return LocalHashEmbedder{Dim: 32}.Embed(ctx, req)
	}
	return EmbedResult{Vector: vector, Dim: len(vector), Model: req.Model}, nil
}
