package benchmark

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type BaselineManifestInput struct {
	MetricsPath       string
	DecisionLogPath   string
	RequestLogDB      string
	ReportPath        string
	CompareReportPath string
	RunID             string
	Dataset           string
	SamplingMode      string
	RepoRoot          string
	CreatedAt         time.Time
}

type BaselineManifest struct {
	RunID              string         `json:"run_id"`
	Dataset            string         `json:"dataset"`
	SamplingMode       string         `json:"sampling_mode"`
	SampleCount        int            `json:"sample_count"`
	StrataCount        int            `json:"strata_count"`
	SampledByStratum   map[string]int `json:"sampled_by_stratum,omitempty"`
	Threshold          float64        `json:"threshold,omitempty"`
	VectorStore        string         `json:"vector_store,omitempty"`
	SemanticRerank     string         `json:"semantic_rerank,omitempty"`
	TopKTrace          int            `json:"top_k_trace,omitempty"`
	MetricsPath        string         `json:"metrics_path"`
	DecisionLogPath    string         `json:"decision_log_path"`
	RequestLogDBPath   string         `json:"request_log_db_path,omitempty"`
	BaselineReportPath string         `json:"baseline_report_path,omitempty"`
	CompareReportPath  string         `json:"compare_report_path,omitempty"`
	CreatedAt          string         `json:"created_at"`
	GitDirty           bool           `json:"git_dirty"`
	GitStatus          []string       `json:"git_status,omitempty"`
}

func BuildBaselineManifest(ctx context.Context, input BaselineManifestInput) (BaselineManifest, error) {
	metrics, err := loadMetricsFile(input.MetricsPath)
	if err != nil {
		return BaselineManifest{}, err
	}
	records, err := LoadDecisionLog(input.DecisionLogPath)
	if err != nil {
		return BaselineManifest{}, err
	}
	runID := strings.TrimSpace(input.RunID)
	dataset := ""
	if len(records) > 0 {
		dataset = filepath.Base(records[0].Dataset)
	}
	if strings.TrimSpace(input.RequestLogDB) != "" && runID != "" {
		if _, err := os.Stat(input.RequestLogDB); err == nil {
			store, openErr := OpenRequestLogStore(ctx, input.RequestLogDB)
			if openErr == nil {
				defer func() { _ = store.Close() }()
				if run, getErr := store.GetRun(ctx, runID); getErr == nil {
					if dataset == "" {
						dataset = filepath.Base(run.Dataset)
					}
				}
			}
		}
	}
	if dataset == "" {
		dataset = "unknown"
	}
	if strings.TrimSpace(input.Dataset) != "" {
		dataset = filepath.Base(input.Dataset)
	}
	samplingMode := defaultString(metrics.SamplingMode, "unknown")
	if strings.TrimSpace(input.SamplingMode) != "" {
		samplingMode = strings.TrimSpace(input.SamplingMode)
	}
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	repoRoot := strings.TrimSpace(input.RepoRoot)
	if repoRoot == "" {
		repoRoot = "."
	}
	gitDirty, gitStatus := gitDirtyStatus(ctx, repoRoot)
	return BaselineManifest{
		RunID:              runID,
		Dataset:            dataset,
		SamplingMode:       samplingMode,
		SampleCount:        maxManifestInt(metrics.Total, len(records)),
		StrataCount:        metrics.StrataCount,
		SampledByStratum:   cloneIntMap(metrics.SampledByStratum),
		Threshold:          metrics.Threshold,
		VectorStore:        metrics.VectorStore,
		SemanticRerank:     metrics.SemanticRerank,
		TopKTrace:          metrics.TopKTrace,
		MetricsPath:        input.MetricsPath,
		DecisionLogPath:    input.DecisionLogPath,
		RequestLogDBPath:   input.RequestLogDB,
		BaselineReportPath: input.ReportPath,
		CompareReportPath:  input.CompareReportPath,
		CreatedAt:          createdAt.Format(time.RFC3339Nano),
		GitDirty:           gitDirty,
		GitStatus:          gitStatus,
	}, nil
}

func WriteBaselineManifest(path string, manifest BaselineManifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func LoadBaselineManifest(path string) (BaselineManifest, error) {
	var manifest BaselineManifest
	bytes, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if len(strings.TrimSpace(string(bytes))) == 0 {
		return manifest, nil
	}
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func gitDirtyStatus(ctx context.Context, repoRoot string) (bool, []string) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "status", "--short")
	output, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return len(out) > 0, out
}

func cloneIntMap(input map[string]int) map[string]int {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]int, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func maxManifestInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
