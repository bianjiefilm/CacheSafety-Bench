package embedding

import "context"

type EmbedRequest struct {
	Text  string
	Model string
}

type EmbedResult struct {
	Vector []float64
	Dim    int
	Model  string
}

type Embedder interface {
	Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error)
}
