package benchmark

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type RequestLogStore struct {
	db *sql.DB
}

type BenchmarkRunLog struct {
	RunID         string
	Dataset       string
	ProviderMode  string
	JudgeMode     string
	EmbeddingMode string
	VectorStore   string
	VectorDim     int
	Threshold     float64
	Seed          int64
	StartedAt     time.Time
	FinishedAt    time.Time
}

type RequestLogWrite struct {
	Run     BenchmarkRunLog
	Metrics any
	Records []DecisionRecord
}

type BenchmarkRunRow struct {
	RunID         string          `json:"run_id"`
	Dataset       string          `json:"dataset"`
	ProviderMode  string          `json:"provider_mode"`
	JudgeMode     string          `json:"judge_mode"`
	EmbeddingMode string          `json:"embedding_mode"`
	VectorStore   string          `json:"vector_store"`
	VectorDim     int             `json:"vector_dim"`
	Threshold     float64         `json:"threshold"`
	Seed          int64           `json:"seed"`
	StartedAt     string          `json:"started_at"`
	FinishedAt    string          `json:"finished_at"`
	MetricsJSON   json.RawMessage `json:"metrics_json,omitempty"`
}

type RequestLogRow struct {
	SampleID                    string  `json:"sample_id"`
	Category                    string  `json:"category,omitempty"`
	ExpectedRisk                string  `json:"expected_risk,omitempty"`
	SourceKind                  string  `json:"source_kind,omitempty"`
	ExpectedCacheBehavior       string  `json:"expected_cache_behavior,omitempty"`
	ModelAlias                  string  `json:"model_alias,omitempty"`
	CacheLayer                  string  `json:"cache_layer"`
	SemanticCandidateFound      bool    `json:"semantic_candidate_found"`
	Similarity                  float64 `json:"similarity,omitempty"`
	RequestPreview              string  `json:"request_preview,omitempty"`
	CandidatePromptPreview      string  `json:"candidate_prompt_preview,omitempty"`
	CandidateAnswerPreview      string  `json:"candidate_answer_preview,omitempty"`
	FreshAnswerPreview          string  `json:"fresh_answer_preview,omitempty"`
	SafetyAllowed               bool    `json:"safety_allowed"`
	RefusedReason               string  `json:"refused_reason,omitempty"`
	RefusedDetail               string  `json:"refused_detail,omitempty"`
	JudgeMode                   string  `json:"judge_mode,omitempty"`
	JudgeCacheSafe              *bool   `json:"judge_cache_safe,omitempty"`
	CachedScore                 float64 `json:"cached_score,omitempty"`
	FreshScore                  float64 `json:"fresh_score,omitempty"`
	JudgeDelta                  float64 `json:"judge_delta,omitempty"`
	JudgeReason                 string  `json:"judge_reason,omitempty"`
	JudgeError                  string  `json:"judge_error,omitempty"`
	BadHit                      bool    `json:"bad_hit"`
	Unknown                     bool    `json:"unknown"`
	EstimatedUpstreamCost       float64 `json:"estimated_upstream_cost"`
	SafeCostSaved               float64 `json:"safe_cost_saved"`
	PromptTokens                int     `json:"prompt_tokens,omitempty"`
	CompletionTokens            int     `json:"completion_tokens,omitempty"`
	TotalTokens                 int     `json:"total_tokens,omitempty"`
	CachedPromptTokensSaved     int     `json:"cached_prompt_tokens_saved,omitempty"`
	CachedCompletionTokensSaved int     `json:"cached_completion_tokens_saved,omitempty"`
	UpstreamCostUSD             float64 `json:"upstream_cost_usd,omitempty"`
	CachedCostUSDSaved          float64 `json:"cached_cost_usd_saved,omitempty"`
	EmbeddingCostUSD            float64 `json:"embedding_cost_usd,omitempty"`
	JudgeCostUSD                float64 `json:"judge_cost_usd,omitempty"`
	NetCostSavedUSD             float64 `json:"net_cost_saved_usd,omitempty"`
	TokenEstimationMode         string  `json:"token_estimation_mode,omitempty"`
}

type RequestLogSummary struct {
	RunID                   string                    `json:"run_id"`
	Total                   int                       `json:"total"`
	ByCacheLayer            map[string]int            `json:"by_cache_layer"`
	BySourceKind            map[string]int            `json:"by_source_kind,omitempty"`
	ByExpectedCacheBehavior map[string]int            `json:"by_expected_cache_behavior,omitempty"`
	ByCategory              map[string]CategoryMetric `json:"by_category,omitempty"`
	SemanticCandidates      int                       `json:"semantic_candidates"`
	SemanticJudgedSafe      int                       `json:"semantic_judged_safe"`
	SemanticRefused         int                       `json:"semantic_refused"`
	BadHitCount             int                       `json:"bad_hit_count"`
	BadHitRate              float64                   `json:"bad_hit_rate"`
	UnknownCount            int                       `json:"unknown_count"`
	SafeCostSaved           float64                   `json:"safe_cost_saved"`
	EstimatedUpstreamCost   float64                   `json:"estimated_upstream_cost"`
	TotalPromptTokens       int                       `json:"total_prompt_tokens,omitempty"`
	TotalCompletionTokens   int                       `json:"total_completion_tokens,omitempty"`
	TotalTokens             int                       `json:"total_tokens,omitempty"`
	CachedTokensSaved       int                       `json:"cached_tokens_saved,omitempty"`
	GrossCostUSD            float64                   `json:"gross_cost_usd,omitempty"`
	CachedCostUSDSaved      float64                   `json:"cached_cost_usd_saved,omitempty"`
	EmbeddingCostUSD        float64                   `json:"embedding_cost_usd,omitempty"`
	JudgeCostUSD            float64                   `json:"judge_cost_usd,omitempty"`
	NetCostSavedUSD         float64                   `json:"net_cost_saved_usd,omitempty"`
	ByRefusedReason         map[string]int            `json:"by_refused_reason"`
}

func OpenRequestLogStore(ctx context.Context, path string) (*RequestLogStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("request log db path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	store := &RequestLogStore{db: db}
	if err := store.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *RequestLogStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *RequestLogStore) init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS benchmark_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id TEXT NOT NULL UNIQUE,
	dataset TEXT,
	provider_mode TEXT,
	judge_mode TEXT,
	embedding_mode TEXT,
	vector_store TEXT,
	vector_dim INTEGER,
	threshold REAL,
	seed INTEGER,
	started_at TEXT NOT NULL,
	finished_at TEXT NOT NULL,
	metrics_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS request_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id TEXT NOT NULL,
	sample_id TEXT,
	category TEXT,
	expected_risk TEXT,
	source_kind TEXT,
	expected_cache_behavior TEXT,
	model_alias TEXT,
	cache_layer TEXT,
	semantic_candidate_found INTEGER NOT NULL DEFAULT 0,
	similarity REAL,
	request_preview TEXT,
	candidate_prompt_preview TEXT,
	candidate_answer_preview TEXT,
	fresh_answer_preview TEXT,
	safety_allowed INTEGER NOT NULL DEFAULT 0,
	refused_reason TEXT,
	refused_detail TEXT,
	judge_mode TEXT,
	judge_cache_safe INTEGER,
	cached_score REAL,
	fresh_score REAL,
	judge_delta REAL,
	judge_reason TEXT,
	judge_error TEXT,
	bad_hit INTEGER NOT NULL DEFAULT 0,
	unknown INTEGER NOT NULL DEFAULT 0,
	estimated_upstream_cost REAL,
	safe_cost_saved REAL,
	prompt_tokens INTEGER,
	completion_tokens INTEGER,
	total_tokens INTEGER,
	cached_prompt_tokens_saved INTEGER,
	cached_completion_tokens_saved INTEGER,
	upstream_cost_usd REAL,
	cached_cost_usd_saved REAL,
	embedding_cost_usd REAL,
	judge_cost_usd REAL,
	net_cost_saved_usd REAL,
	token_estimation_mode TEXT,
	created_at TEXT NOT NULL,
	FOREIGN KEY(run_id) REFERENCES benchmark_runs(run_id)
);

CREATE TABLE IF NOT EXISTS regression_exports (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id TEXT,
	case_id TEXT,
	case_type TEXT,
	output_path TEXT,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_request_log_run_id ON request_log(run_id);
CREATE INDEX IF NOT EXISTS idx_request_log_bad_hit ON request_log(run_id, bad_hit);
CREATE INDEX IF NOT EXISTS idx_request_log_refusal ON request_log(run_id, refused_reason);
`)
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		`ALTER TABLE request_log ADD COLUMN judge_reason TEXT`,
		`ALTER TABLE request_log ADD COLUMN judge_error TEXT`,
		`ALTER TABLE request_log ADD COLUMN source_kind TEXT`,
		`ALTER TABLE request_log ADD COLUMN expected_cache_behavior TEXT`,
		`ALTER TABLE request_log ADD COLUMN prompt_tokens INTEGER`,
		`ALTER TABLE request_log ADD COLUMN completion_tokens INTEGER`,
		`ALTER TABLE request_log ADD COLUMN total_tokens INTEGER`,
		`ALTER TABLE request_log ADD COLUMN cached_prompt_tokens_saved INTEGER`,
		`ALTER TABLE request_log ADD COLUMN cached_completion_tokens_saved INTEGER`,
		`ALTER TABLE request_log ADD COLUMN upstream_cost_usd REAL`,
		`ALTER TABLE request_log ADD COLUMN cached_cost_usd_saved REAL`,
		`ALTER TABLE request_log ADD COLUMN embedding_cost_usd REAL`,
		`ALTER TABLE request_log ADD COLUMN judge_cost_usd REAL`,
		`ALTER TABLE request_log ADD COLUMN net_cost_saved_usd REAL`,
		`ALTER TABLE request_log ADD COLUMN token_estimation_mode TEXT`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	return nil
}

func (s *RequestLogStore) Write(ctx context.Context, input RequestLogWrite) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("request log store is nil")
	}
	run := input.Run
	if strings.TrimSpace(run.RunID) == "" {
		return fmt.Errorf("run_id is required")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	if run.FinishedAt.IsZero() {
		run.FinishedAt = time.Now().UTC()
	}
	metricsPayload, err := json.Marshal(input.Metrics)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM request_log WHERE run_id = ?`, run.RunID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM benchmark_runs WHERE run_id = ?`, run.RunID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO benchmark_runs(
		run_id, dataset, provider_mode, judge_mode, embedding_mode, vector_store, vector_dim, threshold, seed, started_at, finished_at, metrics_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID,
		filepath.Base(run.Dataset),
		run.ProviderMode,
		run.JudgeMode,
		run.EmbeddingMode,
		run.VectorStore,
		run.VectorDim,
		run.Threshold,
		run.Seed,
		run.StartedAt.UTC().Format(time.RFC3339Nano),
		run.FinishedAt.UTC().Format(time.RFC3339Nano),
		string(metricsPayload),
	); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, record := range input.Records {
		var judgeCacheSafe any
		if record.JudgeCacheSafe != nil {
			judgeCacheSafe = boolInt(*record.JudgeCacheSafe)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO request_log(
			run_id, sample_id, category, expected_risk, source_kind, expected_cache_behavior, model_alias, cache_layer,
			semantic_candidate_found, similarity, request_preview, candidate_prompt_preview,
			candidate_answer_preview, fresh_answer_preview, safety_allowed, refused_reason,
			refused_detail, judge_mode, judge_cache_safe, cached_score, fresh_score,
			judge_delta, judge_reason, judge_error, bad_hit, unknown, estimated_upstream_cost,
			safe_cost_saved, prompt_tokens, completion_tokens, total_tokens,
			cached_prompt_tokens_saved, cached_completion_tokens_saved, upstream_cost_usd,
			cached_cost_usd_saved, embedding_cost_usd, judge_cost_usd, net_cost_saved_usd,
			token_estimation_mode, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.RunID,
			record.SampleID,
			record.Category,
			record.ExpectedRisk,
			record.SourceKind,
			record.ExpectedCacheBehavior,
			record.ModelAlias,
			record.CacheLayer,
			boolInt(record.SemanticCandidateFound),
			record.Similarity,
			Preview(record.RequestPreview, 200),
			Preview(record.CandidatePromptPreview, 200),
			Preview(record.CandidateAnswerPreview, 200),
			Preview(record.FreshAnswerPreview, 200),
			boolInt(record.SafetyAllowed),
			record.RefusedReason,
			record.RefusedDetail,
			record.JudgeMode,
			judgeCacheSafe,
			record.CachedScore,
			record.FreshScore,
			record.JudgeDelta,
			record.JudgeReason,
			record.JudgeError,
			boolInt(record.BadHit),
			boolInt(record.JudgeError != ""),
			record.EstimatedUpstreamCost,
			record.SafeCostSaved,
			record.PromptTokens,
			record.CompletionTokens,
			record.TotalTokens,
			record.CachedPromptTokensSaved,
			record.CachedCompletionTokensSaved,
			record.UpstreamCostUSD,
			record.CachedCostUSDSaved,
			record.EmbeddingCostUSD,
			record.JudgeCostUSD,
			record.NetCostSavedUSD,
			record.TokenEstimationMode,
			now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *RequestLogStore) ListRuns(ctx context.Context) ([]BenchmarkRunRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id, dataset, provider_mode, judge_mode, embedding_mode,
		vector_store, vector_dim, threshold, seed, started_at, finished_at, metrics_json
		FROM benchmark_runs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []BenchmarkRunRow{}
	for rows.Next() {
		var row BenchmarkRunRow
		var metricsJSON string
		if err := rows.Scan(&row.RunID, &row.Dataset, &row.ProviderMode, &row.JudgeMode, &row.EmbeddingMode,
			&row.VectorStore, &row.VectorDim, &row.Threshold, &row.Seed, &row.StartedAt, &row.FinishedAt, &metricsJSON); err != nil {
			return nil, err
		}
		row.MetricsJSON = json.RawMessage(metricsJSON)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *RequestLogStore) GetRun(ctx context.Context, runID string) (BenchmarkRunRow, error) {
	rows, err := s.ListRuns(ctx)
	if err != nil {
		return BenchmarkRunRow{}, err
	}
	for _, row := range rows {
		if row.RunID == runID {
			return row, nil
		}
	}
	return BenchmarkRunRow{}, sql.ErrNoRows
}

func (s *RequestLogStore) Summary(ctx context.Context, runID string) (RequestLogSummary, error) {
	summary := RequestLogSummary{RunID: runID, ByCacheLayer: map[string]int{}, ByRefusedReason: map[string]int{}}
	summary.BySourceKind = map[string]int{}
	summary.ByExpectedCacheBehavior = map[string]int{}
	summary.ByCategory = map[string]CategoryMetric{}
	rows, err := s.db.QueryContext(ctx, `SELECT category, source_kind, expected_cache_behavior, cache_layer, semantic_candidate_found, refused_reason,
		judge_cache_safe, bad_hit, unknown, estimated_upstream_cost, safe_cost_saved,
		prompt_tokens, completion_tokens, total_tokens, cached_prompt_tokens_saved,
		cached_completion_tokens_saved, upstream_cost_usd, cached_cost_usd_saved,
		embedding_cost_usd, judge_cost_usd, net_cost_saved_usd
		FROM request_log WHERE run_id = ?`, runID)
	if err != nil {
		return summary, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var category, sourceKind, expectedCacheBehavior, cacheLayer, refusedReason string
		var semanticCandidate, badHit, unknown int
		var promptTokens, completionTokens, totalTokens, cachedPromptSaved, cachedCompletionSaved int
		var judgeCacheSafe sql.NullInt64
		var estimatedCost, saved, upstreamCost, cachedCostSaved, embeddingCost, judgeCost, netSaved float64
		if err := rows.Scan(&category, &sourceKind, &expectedCacheBehavior, &cacheLayer, &semanticCandidate, &refusedReason, &judgeCacheSafe, &badHit, &unknown, &estimatedCost, &saved,
			&promptTokens, &completionTokens, &totalTokens, &cachedPromptSaved, &cachedCompletionSaved,
			&upstreamCost, &cachedCostSaved, &embeddingCost, &judgeCost, &netSaved); err != nil {
			return summary, err
		}
		summary.Total++
		summary.BySourceKind[defaultString(sourceKind, "unknown")]++
		summary.ByExpectedCacheBehavior[defaultString(expectedCacheBehavior, "unspecified")]++
		summary.ByCacheLayer[cacheLayer]++
		categoryMetric := summary.ByCategory[category]
		categoryMetric.Total++
		if semanticCandidate == 1 {
			summary.SemanticCandidates++
			categoryMetric.SemanticCandidates++
		}
		if refusedReason != "" {
			summary.SemanticRefused++
			summary.ByRefusedReason[refusedReason]++
			categoryMetric.SemanticRejectedBySafety++
		}
		if judgeCacheSafe.Valid && judgeCacheSafe.Int64 == 1 && badHit == 0 && cacheLayer == "semantic" {
			summary.SemanticJudgedSafe++
			categoryMetric.SemanticJudgedSafe++
		}
		if badHit == 1 {
			summary.BadHitCount++
			categoryMetric.badHits++
		}
		if unknown == 1 {
			summary.UnknownCount++
		}
		summary.EstimatedUpstreamCost += estimatedCost
		summary.SafeCostSaved += saved
		summary.TotalPromptTokens += promptTokens
		summary.TotalCompletionTokens += completionTokens
		summary.TotalTokens += totalTokens
		summary.CachedTokensSaved += cachedPromptSaved + cachedCompletionSaved
		summary.GrossCostUSD += upstreamCost
		summary.CachedCostUSDSaved += cachedCostSaved
		summary.EmbeddingCostUSD += embeddingCost
		summary.JudgeCostUSD += judgeCost
		summary.NetCostSavedUSD += netSaved
		categoryMetric.SafeCostSaved += saved
		categoryMetric.CachedTokensSaved += cachedPromptSaved + cachedCompletionSaved
		categoryMetric.CachedCostUSDSaved += cachedCostSaved
		categoryMetric.NetCostSavedUSD += netSaved
		summary.ByCategory[category] = categoryMetric
	}
	if err := rows.Err(); err != nil {
		return summary, err
	}
	if summary.Total > 0 {
		summary.BadHitRate = float64(summary.BadHitCount) / float64(summary.Total)
	}
	for category, metric := range summary.ByCategory {
		if metric.Total > 0 {
			metric.BadHitRate = float64(metric.badHits) / float64(metric.Total)
		}
		summary.ByCategory[category] = metric
	}
	return summary, nil
}

func (s *RequestLogStore) BadHits(ctx context.Context, runID string) ([]RequestLogRow, error) {
	return s.queryRows(ctx, runID, `bad_hit = 1`)
}

func (s *RequestLogStore) Refusals(ctx context.Context, runID string) ([]RequestLogRow, error) {
	return s.queryRows(ctx, runID, `refused_reason <> ''`)
}

func (s *RequestLogStore) AllRequestLogs(ctx context.Context, runID string) ([]RequestLogRow, error) {
	return s.queryRows(ctx, runID, `1 = 1`)
}

func (s *RequestLogStore) queryRows(ctx context.Context, runID string, where string) ([]RequestLogRow, error) {
	query := fmt.Sprintf(`SELECT sample_id, category, expected_risk, source_kind, expected_cache_behavior, model_alias, cache_layer,
		semantic_candidate_found, similarity, request_preview, candidate_prompt_preview,
		candidate_answer_preview, fresh_answer_preview, safety_allowed, refused_reason, refused_detail,
		judge_mode, judge_cache_safe, cached_score, fresh_score, judge_delta, judge_reason,
		judge_error, bad_hit,
		unknown, estimated_upstream_cost, safe_cost_saved, prompt_tokens, completion_tokens,
		total_tokens, cached_prompt_tokens_saved, cached_completion_tokens_saved,
		upstream_cost_usd, cached_cost_usd_saved, embedding_cost_usd, judge_cost_usd,
		net_cost_saved_usd, token_estimation_mode
		FROM request_log WHERE run_id = ? AND %s ORDER BY id`, where)
	rows, err := s.db.QueryContext(ctx, query, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []RequestLogRow{}
	for rows.Next() {
		var row RequestLogRow
		var semanticCandidate, safetyAllowed, badHit, unknown int
		var judgeCacheSafe sql.NullInt64
		var requestPreview, candidatePromptPreview, candidateAnswerPreview, freshAnswerPreview sql.NullString
		var refusedReason, refusedDetail, judgeMode, judgeReason, judgeError, tokenMode sql.NullString
		if err := rows.Scan(&row.SampleID, &row.Category, &row.ExpectedRisk, &row.SourceKind, &row.ExpectedCacheBehavior, &row.ModelAlias, &row.CacheLayer,
			&semanticCandidate, &row.Similarity, &requestPreview, &candidatePromptPreview,
			&candidateAnswerPreview, &freshAnswerPreview, &safetyAllowed, &refusedReason, &refusedDetail,
			&judgeMode, &judgeCacheSafe, &row.CachedScore, &row.FreshScore, &row.JudgeDelta,
			&judgeReason, &judgeError, &badHit, &unknown, &row.EstimatedUpstreamCost, &row.SafeCostSaved,
			&row.PromptTokens, &row.CompletionTokens, &row.TotalTokens, &row.CachedPromptTokensSaved,
			&row.CachedCompletionTokensSaved, &row.UpstreamCostUSD, &row.CachedCostUSDSaved,
			&row.EmbeddingCostUSD, &row.JudgeCostUSD, &row.NetCostSavedUSD, &tokenMode); err != nil {
			return nil, err
		}
		row.RequestPreview = requestPreview.String
		row.CandidatePromptPreview = candidatePromptPreview.String
		row.CandidateAnswerPreview = candidateAnswerPreview.String
		row.FreshAnswerPreview = freshAnswerPreview.String
		row.RefusedReason = refusedReason.String
		row.RefusedDetail = refusedDetail.String
		row.JudgeMode = judgeMode.String
		row.JudgeReason = judgeReason.String
		row.JudgeError = judgeError.String
		row.TokenEstimationMode = tokenMode.String
		row.SemanticCandidateFound = semanticCandidate == 1
		row.SafetyAllowed = safetyAllowed == 1
		row.BadHit = badHit == 1
		row.Unknown = unknown == 1
		if judgeCacheSafe.Valid {
			value := judgeCacheSafe.Int64 == 1
			row.JudgeCacheSafe = &value
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func DefaultRunID(dataset string, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	base := strings.TrimSuffix(filepath.Base(dataset), filepath.Ext(dataset))
	if base == "" || base == "." {
		base = "benchmark"
	}
	return now.UTC().Format("20060102T150405Z") + "_" + sanitizeRegressionName(base)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
