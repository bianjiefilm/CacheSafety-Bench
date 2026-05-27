package cache

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
)

type SemanticVectorMetadata map[string]string

type SemanticVectorEntry struct {
	Prompt       string
	Answer       string
	Model        string
	Category     string
	TeacherScore float64
	Vector       []float64
	Metadata     SemanticVectorMetadata
}

type SemanticVectorCandidate struct {
	Entry      SemanticVectorEntry
	Similarity float64
}

type SemanticVectorStore struct {
	mu      sync.RWMutex
	entries []SemanticVectorEntry
	dim     int
}

func NewSemanticVectorStore() *SemanticVectorStore {
	return &SemanticVectorStore{}
}

func NewSemanticVectorStoreWithDim(dim int) *SemanticVectorStore {
	return &SemanticVectorStore{dim: dim}
}

func (s *SemanticVectorStore) Insert(ctx context.Context, item SemanticVectorEntry) error {
	if len(item.Vector) == 0 {
		return fmt.Errorf("vector dimension mismatch: got 0")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dim == 0 {
		s.dim = len(item.Vector)
	}
	if len(item.Vector) != s.dim {
		return fmt.Errorf("vector dimension mismatch: got %d want %d", len(item.Vector), s.dim)
	}
	item.Vector = append([]float64(nil), item.Vector...)
	s.entries = append(s.entries, item)
	return nil
}

func (s *SemanticVectorStore) Search(ctx context.Context, queryVector []float64, threshold float64, topK int) ([]SemanticVectorCandidate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.dim > 0 && len(queryVector) != s.dim {
		return nil, fmt.Errorf("vector dimension mismatch: got %d want %d", len(queryVector), s.dim)
	}
	if topK <= 0 {
		topK = 1
	}
	candidates := make([]SemanticVectorCandidate, 0, len(s.entries))
	for _, entry := range s.entries {
		score := CosineSimilarity(queryVector, entry.Vector)
		if score >= threshold {
			candidates = append(candidates, SemanticVectorCandidate{Entry: entry, Similarity: score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Similarity > candidates[j].Similarity
	})
	if len(candidates) > topK {
		return candidates[:topK], nil
	}
	return candidates, nil
}

func (s *SemanticVectorStore) Close() error {
	return nil
}

func (s *SemanticVectorStore) Dim() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dim
}

func CosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
