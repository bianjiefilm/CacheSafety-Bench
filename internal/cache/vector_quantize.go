package cache

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

type QuantizedVector struct {
	Values []int8
	Scale  float64
}

type VectorSample struct {
	ID       string    `json:"id"`
	Prompt   string    `json:"prompt,omitempty"`
	Category string    `json:"category,omitempty"`
	Vector   []float64 `json:"-"`
}

type QuantizationEvalMetrics struct {
	TotalVectors           int                        `json:"total_vectors"`
	Dim                    int                        `json:"dim"`
	Top1MatchRate          float64                    `json:"top1_match_rate"`
	AvgSimilarityDelta     float64                    `json:"avg_similarity_delta"`
	P95SimilarityDelta     float64                    `json:"p95_similarity_delta"`
	MaxSimilarityDelta     float64                    `json:"max_similarity_delta"`
	Threshold              float64                    `json:"threshold"`
	ThresholdCrossingCount int                        `json:"threshold_crossing_count"`
	ThresholdCrossings     []ThresholdCrossingExample `json:"threshold_crossings,omitempty"`
	Note                   string                     `json:"note,omitempty"`
}

type ThresholdCrossingExample struct {
	QueryID                string  `json:"query_id"`
	BaselineTop1ID         string  `json:"baseline_top1_id"`
	Int8Top1ID             string  `json:"int8_top1_id"`
	BaselineSimilarity     float64 `json:"baseline_similarity"`
	Int8Similarity         float64 `json:"int8_similarity"`
	SimilarityDelta        float64 `json:"similarity_delta"`
	BaselineAboveThreshold bool    `json:"baseline_above_threshold"`
	Int8AboveThreshold     bool    `json:"int8_above_threshold"`
}

func QuantizeInt8(vector []float64) QuantizedVector {
	out := QuantizedVector{Values: make([]int8, len(vector))}
	var maxAbs float64
	for _, value := range vector {
		if abs := math.Abs(value); abs > maxAbs {
			maxAbs = abs
		}
	}
	if maxAbs == 0 {
		out.Scale = 1
		return out
	}
	out.Scale = maxAbs / 127
	for i, value := range vector {
		quantized := math.Round(value / out.Scale)
		if quantized > 127 {
			quantized = 127
		}
		if quantized < -127 {
			quantized = -127
		}
		out.Values[i] = int8(quantized)
	}
	return out
}

func DequantizeInt8(vector QuantizedVector) []float64 {
	out := make([]float64, len(vector.Values))
	scale := vector.Scale
	if scale == 0 {
		scale = 1
	}
	for i, value := range vector.Values {
		out[i] = float64(value) * scale
	}
	return out
}

func EvaluateQuantization(samples []VectorSample, threshold float64) QuantizationEvalMetrics {
	metrics := QuantizationEvalMetrics{
		TotalVectors: len(samples),
		Threshold:    threshold,
		Note:         "int8 candidate stores per-vector int8 blobs and dequantizes before cosine search; this is a quality eval path, not final sqlite-vec int8 performance shape",
	}
	if len(samples) == 0 {
		return metrics
	}
	metrics.Dim = len(samples[0].Vector)
	if len(samples) < 2 {
		return metrics
	}
	dequantized := make([][]float64, len(samples))
	for i, sample := range samples {
		dequantized[i] = DequantizeInt8(QuantizeInt8(sample.Vector))
	}
	var matched int
	var sumDelta float64
	deltas := make([]float64, 0, len(samples))
	for i, query := range samples {
		baseIdx, baseScore := top1ForQuery(samples, nil, i)
		int8Idx, int8Score := top1ForQuery(samples, dequantized, i)
		if baseIdx == int8Idx {
			matched++
		}
		delta := math.Abs(baseScore - int8Score)
		sumDelta += delta
		deltas = append(deltas, delta)
		if delta > metrics.MaxSimilarityDelta {
			metrics.MaxSimilarityDelta = delta
		}
		baseAbove := baseScore >= threshold
		int8Above := int8Score >= threshold
		if baseAbove != int8Above {
			metrics.ThresholdCrossingCount++
			if len(metrics.ThresholdCrossings) < 20 {
				metrics.ThresholdCrossings = append(metrics.ThresholdCrossings, ThresholdCrossingExample{
					QueryID:                query.ID,
					BaselineTop1ID:         samples[baseIdx].ID,
					Int8Top1ID:             samples[int8Idx].ID,
					BaselineSimilarity:     baseScore,
					Int8Similarity:         int8Score,
					SimilarityDelta:        delta,
					BaselineAboveThreshold: baseAbove,
					Int8AboveThreshold:     int8Above,
				})
			}
		}
	}
	metrics.Top1MatchRate = float64(matched) / float64(len(samples))
	metrics.AvgSimilarityDelta = sumDelta / float64(len(samples))
	sort.Float64s(deltas)
	metrics.P95SimilarityDelta = percentile(deltas, 0.95)
	return metrics
}

func top1ForQuery(samples []VectorSample, dequantized [][]float64, queryIdx int) (int, float64) {
	bestIdx := -1
	bestScore := math.Inf(-1)
	query := samples[queryIdx].Vector
	for i, sample := range samples {
		if i == queryIdx {
			continue
		}
		candidate := sample.Vector
		if dequantized != nil {
			candidate = dequantized[i]
		}
		score := CosineSimilarity(query, candidate)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return 0, 0
	}
	return bestIdx, bestScore
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func EncodeInt8(values []int8) []byte {
	out := make([]byte, len(values))
	for i, value := range values {
		out[i] = byte(value)
	}
	return out
}

func DecodeInt8(blob []byte) []int8 {
	out := make([]int8, len(blob))
	for i, value := range blob {
		out[i] = int8(value)
	}
	return out
}

func DecodeFloat32Vector(blob []byte) ([]float64, error) {
	if len(blob)%4 != 0 {
		return nil, fmt.Errorf("float32 vector blob length must be divisible by 4: %d", len(blob))
	}
	out := make([]float64, len(blob)/4)
	for i := range out {
		bits := binary.LittleEndian.Uint32(blob[i*4 : i*4+4])
		out[i] = float64(math.Float32frombits(bits))
	}
	return out, nil
}

func marshalSemanticMetadata(metadata SemanticVectorMetadata) (string, error) {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func unmarshalSemanticMetadata(payload string, metadata *SemanticVectorMetadata) error {
	if payload == "" {
		return nil
	}
	return json.Unmarshal([]byte(payload), metadata)
}

func LoadSQLiteFloatVectorSamples(ctx context.Context, path string) ([]VectorSample, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}
	sqlite_vec.Auto()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `SELECT v.rowid, m.prompt, m.category, v.embedding
		FROM semantic_vec v
		JOIN semantic_meta m ON m.rowid = v.rowid
		ORDER BY v.rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var samples []VectorSample
	for rows.Next() {
		var rowid int64
		var prompt, category string
		var blob []byte
		if err := rows.Scan(&rowid, &prompt, &category, &blob); err != nil {
			return nil, err
		}
		vector, err := DecodeFloat32Vector(blob)
		if err != nil {
			return nil, fmt.Errorf("decode vector rowid %d: %w", rowid, err)
		}
		samples = append(samples, VectorSample{
			ID:       fmt.Sprintf("%d", rowid),
			Prompt:   prompt,
			Category: category,
			Vector:   vector,
		})
	}
	return samples, rows.Err()
}

type SQLiteInt8VectorStore struct {
	db   *sql.DB
	path string
	dim  int
	mu   sync.Mutex
}

func NewSQLiteInt8VectorStore(ctx context.Context, path string, dim int) (*SQLiteInt8VectorStore, error) {
	if dim <= 0 {
		dim = defaultSQLiteVectorDim
	}
	if path == "" {
		file, err := os.CreateTemp("", "karpathy-lite-int8-vector-*.db")
		if err != nil {
			return nil, err
		}
		path = file.Name()
		_ = file.Close()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	store := &SQLiteInt8VectorStore{db: db, path: path, dim: dim}
	if err := store.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteInt8VectorStore) init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
DROP TABLE IF EXISTS semantic_int8_vec;
DROP TABLE IF EXISTS semantic_meta;

CREATE TABLE semantic_meta(
	rowid INTEGER PRIMARY KEY,
	prompt TEXT NOT NULL,
	answer TEXT NOT NULL,
	model TEXT NOT NULL,
	category TEXT,
	teacher_score REAL,
	metadata_json TEXT,
	created_at TEXT NOT NULL
);

CREATE TABLE semantic_int8_vec(
	rowid INTEGER PRIMARY KEY,
	embedding_int8 BLOB NOT NULL,
	scale REAL NOT NULL,
	dim INTEGER NOT NULL,
	FOREIGN KEY(rowid) REFERENCES semantic_meta(rowid)
);
`)
	return err
}

func (s *SQLiteInt8VectorStore) Insert(ctx context.Context, item SemanticVectorEntry) error {
	if len(item.Vector) != s.dim {
		return fmt.Errorf("vector dimension mismatch: got %d want %d", len(item.Vector), s.dim)
	}
	quantized := QuantizeInt8(item.Vector)
	metadata, err := marshalSemanticMetadata(item.Metadata)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO semantic_meta(prompt, answer, model, category, teacher_score, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item.Prompt, item.Answer, item.Model, item.Category, item.TeacherScore, metadata, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	rowid, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO semantic_int8_vec(rowid, embedding_int8, scale, dim) VALUES (?, ?, ?, ?)`,
		rowid, EncodeInt8(quantized.Values), quantized.Scale, s.dim); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteInt8VectorStore) Search(ctx context.Context, queryVector []float64, threshold float64, topK int) ([]SemanticVectorCandidate, error) {
	if len(queryVector) != s.dim {
		return nil, fmt.Errorf("vector dimension mismatch: got %d want %d", len(queryVector), s.dim)
	}
	if topK <= 0 {
		topK = 1
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
			m.prompt, m.answer, m.model, m.category, m.teacher_score, m.metadata_json,
			v.embedding_int8, v.scale, v.dim
		FROM semantic_int8_vec v
		JOIN semantic_meta m ON m.rowid = v.rowid
		ORDER BY v.rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	candidates := []SemanticVectorCandidate{}
	for rows.Next() {
		var entry SemanticVectorEntry
		var metadata string
		var blob []byte
		var scale float64
		var dim int
		if err := rows.Scan(&entry.Prompt, &entry.Answer, &entry.Model, &entry.Category, &entry.TeacherScore, &metadata, &blob, &scale, &dim); err != nil {
			return nil, err
		}
		if dim != s.dim {
			return nil, fmt.Errorf("stored vector dimension mismatch: got %d want %d", dim, s.dim)
		}
		if len(blob) != s.dim {
			return nil, fmt.Errorf("stored int8 vector dimension mismatch: got %d want %d", len(blob), s.dim)
		}
		if metadata != "" {
			if err := unmarshalSemanticMetadata(metadata, &entry.Metadata); err != nil {
				return nil, err
			}
		}
		entry.Vector = DequantizeInt8(QuantizedVector{Values: DecodeInt8(blob), Scale: scale})
		score := CosineSimilarity(queryVector, entry.Vector)
		if score >= threshold {
			candidates = append(candidates, SemanticVectorCandidate{Entry: entry, Similarity: score})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Similarity > candidates[j].Similarity
	})
	if len(candidates) > topK {
		return candidates[:topK], nil
	}
	return candidates, nil
}

func (s *SQLiteInt8VectorStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteInt8VectorStore) Dim() int {
	if s == nil {
		return 0
	}
	return s.dim
}

func (s *SQLiteInt8VectorStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}
