package embedding

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
)

type LocalHashEmbedder struct {
	Dim int
}

func (e LocalHashEmbedder) Embed(ctx context.Context, req EmbedRequest) (EmbedResult, error) {
	dim := e.Dim
	if dim <= 0 {
		dim = 64
	}
	vector := make([]float64, dim)
	for _, token := range strings.Fields(strings.ToLower(req.Text)) {
		token = strings.Trim(token, " \t\n\r.,!?;:\"'()[]{}")
		if token == "" || hashStopword(token) {
			continue
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(token))
		sum := h.Sum64()
		index := int(sum % uint64(dim))
		sign := 1.0
		if (sum>>63)&1 == 1 {
			sign = -1.0
		}
		vector[index] += sign
	}
	normalize(vector)
	return EmbedResult{Vector: vector, Dim: dim, Model: "local-hash"}, nil
}

func hashStopword(token string) bool {
	switch token {
	case "reply", "give", "with", "one", "word", "about", "describing", "describe", "exactly", "briefly", "the", "a", "an", "is", "to", "for":
		return true
	default:
		return false
	}
}

func normalize(vector []float64) {
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm == 0 {
		return
	}
	norm = math.Sqrt(norm)
	for i := range vector {
		vector[i] /= norm
	}
}
