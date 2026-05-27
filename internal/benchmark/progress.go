package benchmark

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

const (
	ProgressStageEmbedding   = "embedding"
	ProgressStageCacheSearch = "cache_search"
	ProgressStageSafety      = "safety"
	ProgressStageProvider    = "provider"
	ProgressStageJudge       = "judge"
	ProgressStageWriteLog    = "write_log"
	ProgressStageDone        = "done"
	ProgressStageTimeout     = "timeout"
	ProgressStageError       = "error"
)

type ProgressRecord struct {
	SampleIndex  int    `json:"sample_index"`
	SampleID     string `json:"sample_id"`
	Category     string `json:"category,omitempty"`
	ExpectedRisk string `json:"expected_risk,omitempty"`
	Stage        string `json:"stage"`
	ElapsedMS    int64  `json:"elapsed_ms"`
	ErrorSummary string `json:"error_summary,omitempty"`
	WrittenAt    string `json:"written_at,omitempty"`
}

type ProgressSummary struct {
	TotalRecords      int                     `json:"total_records"`
	AvgLatencyByStage map[string]float64      `json:"avg_latency_by_stage_ms,omitempty"`
	TimeoutCount      int                     `json:"timeout_count"`
	ErrorCount        int                     `json:"error_count"`
	SlowestSamples    []ProgressSampleLatency `json:"slowest_samples,omitempty"`
	TimeoutErrors     []ProgressIssueSample   `json:"timeout_error_samples,omitempty"`
	Warnings          []string                `json:"warnings,omitempty"`
}

type ProgressSampleLatency struct {
	SampleID  string `json:"sample_id"`
	Category  string `json:"category,omitempty"`
	Stage     string `json:"stage"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

type ProgressIssueSample struct {
	SampleID     string `json:"sample_id"`
	Category     string `json:"category,omitempty"`
	Stage        string `json:"stage"`
	ElapsedMS    int64  `json:"elapsed_ms"`
	ErrorSummary string `json:"error_summary,omitempty"`
}

type ProgressLogger struct {
	mu             sync.Mutex
	file           *os.File
	encoder        *json.Encoder
	checkpointPath string
}

func OpenProgressLogger(progressPath string, checkpointPath string) (*ProgressLogger, error) {
	logger := &ProgressLogger{checkpointPath: strings.TrimSpace(checkpointPath)}
	progressPath = strings.TrimSpace(progressPath)
	if progressPath == "" {
		return logger, nil
	}
	if err := mkdirParent(progressPath); err != nil {
		return nil, err
	}
	file, err := os.Create(progressPath)
	if err != nil {
		return nil, err
	}
	logger.file = file
	logger.encoder = json.NewEncoder(file)
	return logger, nil
}

func (l *ProgressLogger) Log(item Case, stage string, elapsed time.Duration, err error) {
	if l == nil {
		return
	}
	record := ProgressRecord{
		SampleIndex:  item.SampleIndex,
		SampleID:     item.ID,
		Category:     caseCategory(item),
		ExpectedRisk: item.ExpectedRisk,
		Stage:        stage,
		ElapsedMS:    elapsed.Milliseconds(),
		ErrorSummary: SanitizeErrorSummary(err),
		WrittenAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.encoder != nil {
		_ = l.encoder.Encode(record)
	}
	if l.checkpointPath != "" && (stage == ProgressStageDone || stage == ProgressStageError || stage == ProgressStageTimeout) {
		_ = writeCheckpoint(l.checkpointPath, record)
	}
}

func (l *ProgressLogger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func BuildProgressSummary(paths []string) (ProgressSummary, error) {
	paths = nonEmptyStrings(paths)
	summary := ProgressSummary{AvgLatencyByStage: map[string]float64{}}
	if len(paths) == 0 {
		return summary, nil
	}
	stageTotals := map[string]int64{}
	stageCounts := map[string]int{}
	var slowest []ProgressSampleLatency
	for _, path := range paths {
		records, warnings, err := loadProgressRecords(path)
		if err != nil {
			return summary, err
		}
		summary.Warnings = append(summary.Warnings, warnings...)
		for _, record := range records {
			summary.TotalRecords++
			stage := strings.TrimSpace(record.Stage)
			if stage == "" {
				stage = "unknown"
			}
			stageTotals[stage] += record.ElapsedMS
			stageCounts[stage]++
			switch stage {
			case ProgressStageDone:
				slowest = append(slowest, ProgressSampleLatency{
					SampleID:  record.SampleID,
					Category:  record.Category,
					Stage:     stage,
					ElapsedMS: record.ElapsedMS,
				})
			case ProgressStageTimeout:
				summary.TimeoutCount++
				summary.TimeoutErrors = append(summary.TimeoutErrors, ProgressIssueSample{
					SampleID:     record.SampleID,
					Category:     record.Category,
					Stage:        stage,
					ElapsedMS:    record.ElapsedMS,
					ErrorSummary: record.ErrorSummary,
				})
			case ProgressStageError:
				summary.ErrorCount++
				summary.TimeoutErrors = append(summary.TimeoutErrors, ProgressIssueSample{
					SampleID:     record.SampleID,
					Category:     record.Category,
					Stage:        stage,
					ElapsedMS:    record.ElapsedMS,
					ErrorSummary: record.ErrorSummary,
				})
			}
		}
	}
	for stage, total := range stageTotals {
		count := stageCounts[stage]
		if count > 0 {
			summary.AvgLatencyByStage[stage] = float64(total) / float64(count)
		}
	}
	sort.SliceStable(slowest, func(i, j int) bool {
		if slowest[i].ElapsedMS == slowest[j].ElapsedMS {
			return slowest[i].SampleID < slowest[j].SampleID
		}
		return slowest[i].ElapsedMS > slowest[j].ElapsedMS
	})
	if len(slowest) > 5 {
		slowest = slowest[:5]
	}
	summary.SlowestSamples = slowest
	return summary, nil
}

func loadProgressRecords(path string) ([]ProgressRecord, []string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var records []ProgressRecord
	var warnings []string
	line := 0
	for scanner.Scan() {
		raw := bytes.Trim(scanner.Bytes(), "\x00 \t\r\n")
		if len(raw) == 0 {
			continue
		}
		line++
		var record ProgressRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped invalid progress JSON in %s line %d", filepath.Base(path), line))
			continue
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, warnings, err
	}
	return records, warnings, nil
}

func writeCheckpoint(path string, record ProgressRecord) error {
	if err := mkdirParent(path); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func mkdirParent(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

var sensitiveProgressPatterns = []*regexp.Regexp{
	regexp.MustCompile(`ark-[0-9A-Za-z-]+`),
	regexp.MustCompile(`apikey-[0-9A-Za-z-]+`),
	regexp.MustCompile(`doubao-seed-[0-9A-Za-z-]+`),
	regexp.MustCompile(`glm-[0-9A-Za-z-]+`),
	regexp.MustCompile(`doubao-embedding-[0-9A-Za-z-]+`),
	regexp.MustCompile(`Request id: [A-Za-z0-9_-]+`),
}

func SanitizeErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	for _, pattern := range sensitiveProgressPatterns {
		value = pattern.ReplaceAllString(value, "[redacted]")
	}
	if len(value) > 300 {
		value = value[:300]
	}
	return value
}

func isTimeoutError(err error, ctx context.Context) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded))
}

func timeoutDecisionRecord(cfg Config, item Case, vectorStoreMode string, embeddingMode string, judgeMode string, err error) DecisionRecord {
	return DecisionRecord{
		SampleID:              item.ID,
		Dataset:               filepath.Base(cfg.Dataset),
		Category:              caseCategory(item),
		ExpectedRisk:          item.ExpectedRisk,
		SourceKind:            item.SourceKind,
		ExpectedCacheBehavior: item.ExpectedCacheBehavior,
		ModelAlias:            ModelAlias(item.Request.Model),
		VectorStore:           vectorStoreMode,
		EmbeddingMode:         embeddingMode,
		Threshold:             cfg.SemanticThreshold,
		CacheLayer:            string(cachepkg.LayerMiss),
		RequestPreview:        Preview(cachepkg.PromptText(item.Request), 200),
		SafetyAllowed:         false,
		JudgeMode:             judgeMode,
		JudgeError:            SanitizeErrorSummary(err),
		BadHit:                false,
		EstimatedUpstreamCost: item.FreshCost,
	}
}

func (e hitEvaluation) recordError() error {
	if strings.TrimSpace(e.record.Error) == "" {
		return nil
	}
	return errors.New(e.record.Error)
}
