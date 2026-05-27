package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

const defaultSQLiteVectorDim = 2048

type VectorStore interface {
	Insert(context.Context, SemanticVectorEntry) error
	Search(context.Context, []float64, float64, int) ([]SemanticVectorCandidate, error)
	Close() error
	Dim() int
}

type SQLiteVectorStore struct {
	db   *sql.DB
	path string
	dim  int
	mu   sync.Mutex
}

func NewSQLiteVectorStore(ctx context.Context, path string, dim int) (*SQLiteVectorStore, error) {
	if dim <= 0 {
		dim = defaultSQLiteVectorDim
	}
	if path == "" {
		file, err := os.CreateTemp("", "karpathy-lite-vector-*.db")
		if err != nil {
			return nil, err
		}
		path = file.Name()
		_ = file.Close()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	sqlite_vec.Auto()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	store := &SQLiteVectorStore{db: db, path: path, dim: dim}
	if err := store.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteVectorStore) init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS semantic_vec`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS semantic_meta(
		rowid INTEGER PRIMARY KEY,
		prompt TEXT NOT NULL,
		answer TEXT NOT NULL,
		model TEXT NOT NULL,
		category TEXT,
		teacher_score REAL,
		metadata_json TEXT,
		created_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE VIRTUAL TABLE semantic_vec USING vec0(
		embedding float[%d] distance_metric=cosine
	)`, s.dim)); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteVectorStore) Insert(ctx context.Context, item SemanticVectorEntry) error {
	if len(item.Vector) != s.dim {
		return fmt.Errorf("vector dimension mismatch: got %d want %d", len(item.Vector), s.dim)
	}
	vector, err := serializeFloat64(item.Vector)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(item.Metadata)
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
		item.Prompt, item.Answer, item.Model, item.Category, item.TeacherScore, string(metadata), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	rowid, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO semantic_vec(rowid, embedding) VALUES (?, ?)`, rowid, vector); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteVectorStore) Search(ctx context.Context, queryVector []float64, threshold float64, topK int) ([]SemanticVectorCandidate, error) {
	if len(queryVector) != s.dim {
		return nil, fmt.Errorf("vector dimension mismatch: got %d want %d", len(queryVector), s.dim)
	}
	if topK <= 0 {
		topK = 1
	}
	vector, err := serializeFloat64(queryVector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
			m.prompt, m.answer, m.model, m.category, m.teacher_score, m.metadata_json, v.distance
		FROM semantic_vec v
		JOIN semantic_meta m ON m.rowid = v.rowid
		WHERE v.embedding MATCH ? AND k = ?
		ORDER BY v.distance
	`, vector, topK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	candidates := []SemanticVectorCandidate{}
	for rows.Next() {
		var entry SemanticVectorEntry
		var metadata string
		var distance float64
		if err := rows.Scan(&entry.Prompt, &entry.Answer, &entry.Model, &entry.Category, &entry.TeacherScore, &metadata, &distance); err != nil {
			return nil, err
		}
		if metadata != "" {
			if err := json.Unmarshal([]byte(metadata), &entry.Metadata); err != nil {
				return nil, err
			}
		}
		similarity := 1 - distance
		if similarity >= threshold {
			candidates = append(candidates, SemanticVectorCandidate{Entry: entry, Similarity: similarity})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func (s *SQLiteVectorStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteVectorStore) Dim() int {
	if s == nil {
		return 0
	}
	return s.dim
}

func (s *SQLiteVectorStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func serializeFloat64(values []float64) ([]byte, error) {
	out := make([]float32, len(values))
	for i, value := range values {
		out[i] = float32(value)
	}
	return sqlite_vec.SerializeFloat32(out)
}
