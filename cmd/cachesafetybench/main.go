package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/bianjiefilm/CacheSafety-Bench/internal/benchmark"
	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/embedding"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/provider"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/teacher"
	"gopkg.in/yaml.v3"
)

type benchConfig struct {
	Strategy                  string   `yaml:"strategy"`
	SemanticThreshold         float64  `yaml:"semantic_threshold"`
	MaxPromptCharsForSemantic int      `yaml:"max_prompt_chars_for_semantic"`
	RefuseDomains             []string `yaml:"refuse_domains"`
	OutputFormats             []string `yaml:"output_formats"`
}

type report struct {
	Name                      string                    `json:"name"`
	Tagline                   string                    `json:"tagline"`
	Dataset                   string                    `json:"dataset"`
	Strategy                  string                    `json:"strategy"`
	CacheSource               string                    `json:"cache_source,omitempty"`
	TotalPairs                int                       `json:"total_pairs"`
	SafeHitRate               float64                   `json:"safe_hit_rate"`
	BadHitRate                float64                   `json:"bad_hit_rate"`
	SevereBadHitCount         int                       `json:"severe_bad_hit_count"`
	CostSavedPer1KRequestsUSD float64                   `json:"cost_saved_per_1k_requests_usd"`
	NetSavingRate             float64                   `json:"net_saving_rate"`
	SemanticTrapFailureRate   float64                   `json:"semantic_trap_failure_rate"`
	CacheLayerContribution    map[string]int            `json:"cache_layer_contribution"`
	JudgeDelta                float64                   `json:"judge_delta"`
	RegressionEscapeRate      float64                   `json:"regression_escape_rate"`
	BestPolicy                string                    `json:"best_policy"`
	SemanticCacheRecommended  bool                      `json:"semantic_cache_recommended"`
	BadHits                   []reportBadHit            `json:"bad_hits"`
	Summary                   benchmark.DecisionSummary `json:"summary"`
	Config                    reportConfig              `json:"config"`
}

type reportConfig struct {
	MaxPromptCharsForSemantic int      `json:"max_prompt_chars_for_semantic"`
	RefuseDomains             []string `json:"refuse_domains"`
	OutputFormats             []string `json:"output_formats"`
}

type reportBadHit struct {
	ID           string  `json:"id"`
	Category     string  `json:"category,omitempty"`
	CacheLayer   string  `json:"cache_layer"`
	TeacherDelta float64 `json:"teacher_delta"`
	Reason       string  `json:"reason,omitempty"`
	OldRequest   string  `json:"old_request,omitempty"`
	NewRequest   string  `json:"new_request,omitempty"`
}

type supportPair struct {
	ID                       string          `json:"id"`
	Category                 string          `json:"category"`
	OldRequest               json.RawMessage `json:"old_request"`
	OldAnswer                json.RawMessage `json:"old_answer"`
	NewRequest               json.RawMessage `json:"new_request"`
	FreshAnswer              string          `json:"fresh_answer,omitempty"`
	ReferenceAnswer          string          `json:"reference_answer,omitempty"`
	ExpectedRisk             string          `json:"expected_risk"`
	EstimatedUpstreamCostUSD float64         `json:"estimated_upstream_cost_usd"`
	Notes                    string          `json:"notes,omitempty"`
}

type answerEnvelope struct {
	Text    string `json:"text"`
	Content string `json:"content"`
}

func main() {
	if len(os.Args) < 2 || strings.TrimSpace(os.Args[1]) == "" {
		printUsage()
		os.Exit(2)
	}

	switch strings.TrimSpace(os.Args[1]) {
	case "run":
		if err := run(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "cachesafetybench run: %v\n", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		printUsage()
	default:
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Print(`CacheSafety Bench

Usage:
  cachesafetybench run --dataset examples/support_pairs.jsonl --config configs/default.yaml --output reports/example-report.html

  cachesafetybench run --dataset examples/support_pairs.jsonl --observe --model your-model
`)
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	dataset := fs.String("dataset", "examples/support_pairs.jsonl", "JSONL dataset path")
	configPath := fs.String("config", "configs/default.yaml", "YAML config path")
	output := fs.String("output", "reports/example-report.html", "report output path (.json or .html)")
	providerName := fs.String("provider", "fake", "fresh-answer provider: fake or openai")
	model := fs.String("model", "", "model id for provider=openai; defaults to OPENAI_MODEL")
	observe := fs.Bool("observe", false, "score cache layers from gateway serve-mode headers instead of the in-process pipeline")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	pairs, err := loadSupportPairs(*dataset)
	if err != nil {
		return err
	}
	cases, err := buildCases(pairs)
	if err != nil {
		return err
	}

	embedder := selectEmbedder(cfg)
	fresh, cacheSource, err := publicFreshAnswer(*providerName, *model, *observe)
	if err != nil {
		return err
	}
	if cacheSource == "observed" {
		embedder = nil
	}
	metrics, err := benchmark.RunWithEmbedding(context.Background(), cases, benchmark.Config{
		SemanticThreshold: normalizedThreshold(cfg.SemanticThreshold),
		EmbeddingMode:     embeddingModeName(embedder),
		VectorStoreMode:   "memory",
		Dataset:           *dataset,
		CacheSource:       cacheSource,
	}, fresh, teacher.FakeJudge{}, embedder)
	if err != nil {
		return err
	}

	rep := buildReport(metrics, cfg, *dataset)
	if err := writeOutputs(*output, cfg.OutputFormats, rep); err != nil {
		return err
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(rep)
}

func loadConfig(path string) (benchConfig, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return benchConfig{}, err
	}
	var cfg benchConfig
	if err := yaml.Unmarshal(bytes, &cfg); err != nil {
		return benchConfig{}, err
	}
	if strings.TrimSpace(cfg.Strategy) == "" {
		cfg.Strategy = "exact+canonical"
	}
	if cfg.SemanticThreshold <= 0 {
		cfg.SemanticThreshold = 0.95
	}
	if cfg.MaxPromptCharsForSemantic <= 0 {
		cfg.MaxPromptCharsForSemantic = 512
	}
	if len(cfg.OutputFormats) == 0 {
		cfg.OutputFormats = []string{"json"}
	}
	return cfg, nil
}

func loadSupportPairs(path string) ([]supportPair, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	pairs := []supportPair{}
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var item supportPair
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("decode dataset line %d: %w", line, err)
		}
		pairs = append(pairs, item)
	}
	return pairs, scanner.Err()
}

func buildCases(pairs []supportPair) ([]benchmark.Case, error) {
	cases := make([]benchmark.Case, 0, len(pairs))
	for index, item := range pairs {
		newReq, err := parseRequestField(item.NewRequest)
		if err != nil {
			return nil, fmt.Errorf("%s new_request: %w", pairID(item, index), err)
		}
		oldReq, err := parseRequestField(item.OldRequest)
		if err != nil {
			return nil, fmt.Errorf("%s old_request: %w", pairID(item, index), err)
		}
		oldAnswer, err := parseAnswerField(item.OldAnswer)
		if err != nil {
			return nil, fmt.Errorf("%s old_answer: %w", pairID(item, index), err)
		}

		freshAnswer := firstNonEmpty(strings.TrimSpace(item.FreshAnswer), strings.TrimSpace(item.ReferenceAnswer))
		if freshAnswer == "" && cachepkg.PromptText(oldReq) == cachepkg.PromptText(newReq) {
			freshAnswer = oldAnswer
		}
		if freshAnswer == "" {
			return nil, fmt.Errorf("%s requires fresh_answer or reference_answer", pairID(item, index))
		}

		behavior := inferBehavior(item, oldReq, newReq)
		cacheSafe := expectedSafe(behavior)
		cases = append(cases, benchmark.Case{
			ID:                    pairID(item, index),
			Kind:                  "replay_pair",
			Category:              defaultString(strings.TrimSpace(item.Category), "support"),
			ExpectedRisk:          defaultString(strings.TrimSpace(item.ExpectedRisk), "low"),
			Request:               ensureModel(newReq),
			FreshAnswer:           freshAnswer,
			TeacherScore:          10,
			FreshCost:             defaultCost(item.EstimatedUpstreamCostUSD),
			CachedCost:            0,
			EstimatedUpstreamCost: defaultCost(item.EstimatedUpstreamCostUSD),
			OldRequest:            cachepkg.PromptText(oldReq),
			OldAnswer:             oldAnswer,
			ExpectedCacheBehavior: behavior,
			PairGenerationMode:    "public_import",
			SourceKind:            "toy_examples",
			SourceJudgeCacheSafe:  &cacheSafe,
			ExpectedBadHit:        !cacheSafe,
			ParaphraseNotes:       item.Notes,
		})
	}
	return cases, nil
}

func parseRequestField(raw json.RawMessage) (cachepkg.Request, error) {
	if len(raw) == 0 {
		return cachepkg.Request{}, errors.New("request is required")
	}
	if raw[0] == '"' {
		var prompt string
		if err := json.Unmarshal(raw, &prompt); err != nil {
			return cachepkg.Request{}, err
		}
		return cachepkg.Request{
			Model:    "benchmark-provider-model",
			Messages: []cachepkg.Message{{Role: "user", Content: strings.TrimSpace(prompt)}},
		}, nil
	}

	var req cachepkg.Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return cachepkg.Request{}, err
	}
	if len(req.Messages) == 0 {
		return cachepkg.Request{}, errors.New("request.messages must not be empty")
	}
	return ensureModel(req), nil
}

func parseAnswerField(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("answer is required")
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
		return strings.TrimSpace(text), nil
	}
	var env answerEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", err
	}
	answer := firstNonEmpty(strings.TrimSpace(env.Text), strings.TrimSpace(env.Content))
	if answer == "" {
		return "", errors.New("answer.text or answer.content is required")
	}
	return answer, nil
}

func inferBehavior(item supportPair, oldReq cachepkg.Request, newReq cachepkg.Request) string {
	oldPrompt := cachepkg.PromptText(oldReq)
	newPrompt := cachepkg.PromptText(newReq)

	switch {
	case strings.EqualFold(strings.TrimSpace(oldPrompt), strings.TrimSpace(newPrompt)):
		return "exact_duplicate"
	case normalizePrompt(oldPrompt) == normalizePrompt(newPrompt):
		return "canonical_duplicate"
	case strings.EqualFold(strings.TrimSpace(item.Category), "semantic_trap"):
		return "semantic_trap"
	case strings.EqualFold(strings.TrimSpace(item.ExpectedRisk), "high"):
		return "expected_refusal"
	default:
		return "semantic_safe"
	}
}

func expectedSafe(behavior string) bool {
	switch behavior {
	case "semantic_trap", "expected_refusal":
		return false
	default:
		return true
	}
}

func selectEmbedder(cfg benchConfig) embedding.Embedder {
	if strings.Contains(strings.ToLower(cfg.Strategy), "semantic") {
		return embedding.LocalHashEmbedder{Dim: 64}
	}
	return nil
}

func embeddingModeName(embedder any) string {
	if embedder == nil {
		return "disabled"
	}
	return "local-hash"
}

func normalizedThreshold(value float64) float64 {
	if value <= 0 {
		return 0.95
	}
	return value
}

func buildReport(metrics benchmark.Metrics, cfg benchConfig, dataset string) report {
	summary := benchmark.AnalyzeDecisionRecords(metrics.DecisionRecords)
	badHits := make([]reportBadHit, 0, len(metrics.BadHits))
	severeBadHitCount := 0
	semanticTrapTotal := 0
	semanticTrapFailures := 0
	var deltaTotal float64
	var deltaCount int

	for _, record := range metrics.DecisionRecords {
		if record.Category == "semantic_trap" {
			semanticTrapTotal++
			if record.BadHit {
				semanticTrapFailures++
			}
		}
		if record.JudgeCacheSafe != nil || record.JudgeMode != "" {
			deltaTotal += record.JudgeDelta
			deltaCount++
		}
	}
	for _, item := range metrics.BadHits {
		if strings.EqualFold(item.ReplayFixture.ExpectedRisk, "high") {
			severeBadHitCount++
		}
		badHits = append(badHits, reportBadHit{
			ID:           item.ID,
			Category:     item.Category,
			CacheLayer:   item.CacheLayer,
			TeacherDelta: item.TeacherDelta,
			Reason:       item.JudgeReason,
			OldRequest:   item.OldRequest,
			NewRequest:   cachepkg.PromptText(item.Request),
		})
	}

	semanticTrapFailureRate := 0.0
	if semanticTrapTotal > 0 {
		semanticTrapFailureRate = float64(semanticTrapFailures) / float64(semanticTrapTotal)
	}

	judgeDelta := 0.0
	if deltaCount > 0 {
		judgeDelta = deltaTotal / float64(deltaCount)
	}

	costSavedPer1K := 0.0
	if metrics.Total > 0 {
		costSavedPer1K = (metrics.SafeCostSaved / float64(metrics.Total)) * 1000
	}

	semanticRecommended := metrics.LayerContribution["semantic"] > 0 && metrics.BadHitRate == 0 && semanticTrapFailureRate == 0
	bestPolicy := "exact+canonical"
	if semanticRecommended {
		bestPolicy = "exact+canonical+semantic"
	}

	layerContribution := map[string]int{
		"exact":     metrics.LayerContribution["exact"],
		"canonical": metrics.LayerContribution["canonical"],
		"semantic":  metrics.LayerContribution["semantic"],
	}
	if metrics.CacheSource == "observed" || metrics.LayerContribution["ln_beta"] > 0 {
		layerContribution["ln_beta"] = metrics.LayerContribution["ln_beta"]
	}

	return report{
		Name:                      "CacheSafety Bench",
		Tagline:                   "A benchmark for safe LLM response reuse.",
		Dataset:                   filepath.Base(dataset),
		Strategy:                  cfg.Strategy,
		CacheSource:               metrics.CacheSource,
		TotalPairs:                metrics.Total,
		SafeHitRate:               metrics.SafeHitRate,
		BadHitRate:                metrics.BadHitRate,
		SevereBadHitCount:         severeBadHitCount,
		CostSavedPer1KRequestsUSD: costSavedPer1K,
		NetSavingRate:             metrics.NetSavingRate,
		SemanticTrapFailureRate:   semanticTrapFailureRate,
		CacheLayerContribution:    layerContribution,
		JudgeDelta:                judgeDelta,
		RegressionEscapeRate:      0,
		BestPolicy:                bestPolicy,
		SemanticCacheRecommended:  semanticRecommended,
		BadHits:                   badHits,
		Summary:                   summary,
		Config: reportConfig{
			MaxPromptCharsForSemantic: cfg.MaxPromptCharsForSemantic,
			RefuseDomains:             append([]string(nil), cfg.RefuseDomains...),
			OutputFormats:             append([]string(nil), cfg.OutputFormats...),
		},
	}
}

func writeOutputs(target string, formats []string, rep report) error {
	normalized := normalizedFormats(target, formats)
	for format, path := range normalized {
		switch format {
		case "json":
			if err := writeJSON(path, rep); err != nil {
				return err
			}
		case "html":
			if err := writeHTML(path, rep); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizedFormats(target string, formats []string) map[string]string {
	if len(formats) == 0 {
		formats = []string{"json"}
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(target), "."))
	base := strings.TrimSuffix(target, filepath.Ext(target))
	out := map[string]string{}
	for _, format := range formats {
		normalized := strings.ToLower(strings.TrimSpace(format))
		if normalized == "" {
			continue
		}
		switch normalized {
		case "json", "html":
			out[normalized] = base + "." + normalized
		}
	}
	if ext == "json" || ext == "html" {
		out[ext] = target
	}
	if len(out) == 0 {
		out["json"] = target
	}
	return out
}

func writeJSON(path string, rep report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func writeHTML(path string, rep report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tpl := template.Must(template.New("report").Funcs(template.FuncMap{
		"json": func(value any) string {
			payload, _ := json.MarshalIndent(value, "", "  ")
			return string(payload)
		},
		"mul100": func(value float64) float64 { return value * 100 },
	}).Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>{{.Name}} report</title>
    <style>
      body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 0; background: #f4f6f8; color: #112033; }
      main { max-width: 1040px; margin: 0 auto; padding: 48px 24px 64px; }
      h1, h2 { margin: 0 0 12px; }
      p { line-height: 1.6; }
      .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 16px; margin: 24px 0 40px; }
      .card { background: #fff; border: 1px solid #d9e2ec; border-radius: 16px; padding: 18px; box-shadow: 0 10px 30px rgba(17, 32, 51, 0.06); }
      .eyebrow { text-transform: uppercase; letter-spacing: .08em; font-size: 12px; color: #5f6f82; }
      strong.metric { display: block; font-size: 28px; margin-top: 8px; }
      code, pre { font-family: "SFMono-Regular", SFMono-Regular, Consolas, monospace; }
      pre { background: #0f1720; color: #e7edf5; padding: 16px; border-radius: 14px; overflow: auto; }
      ul { padding-left: 18px; }
      table { width: 100%; border-collapse: collapse; background: #fff; border-radius: 16px; overflow: hidden; }
      th, td { padding: 12px 14px; border-bottom: 1px solid #e5ebf1; text-align: left; }
      .badge { display: inline-block; background: #e9f7ef; color: #18663b; border-radius: 999px; padding: 6px 10px; font-size: 12px; }
    </style>
  </head>
  <body>
    <main>
      <span class="badge">{{.Tagline}}</span>
      <h1>{{.Name}}</h1>
      <p>Dataset: <code>{{.Dataset}}</code> · Strategy: <code>{{.Strategy}}</code>{{if .CacheSource}} · Cache source: <code>{{.CacheSource}}</code>{{end}}</p>
      <div class="grid">
        <section class="card"><span class="eyebrow">Total pairs</span><strong class="metric">{{.TotalPairs}}</strong></section>
        <section class="card"><span class="eyebrow">Safe Hit Rate</span><strong class="metric">{{printf "%.1f%%" (mul100 .SafeHitRate)}}</strong></section>
        <section class="card"><span class="eyebrow">Bad Hit Rate</span><strong class="metric">{{printf "%.1f%%" (mul100 .BadHitRate)}}</strong></section>
        <section class="card"><span class="eyebrow">Cost Saved / 1K</span><strong class="metric">${{printf "%.2f" .CostSavedPer1KRequestsUSD}}</strong></section>
      </div>
      <div class="grid">
        <section class="card"><span class="eyebrow">Best policy</span><strong class="metric">{{.BestPolicy}}</strong></section>
        <section class="card"><span class="eyebrow">Semantic trap failure</span><strong class="metric">{{printf "%.1f%%" (mul100 .SemanticTrapFailureRate)}}</strong></section>
        <section class="card"><span class="eyebrow">Severe bad hits</span><strong class="metric">{{.SevereBadHitCount}}</strong></section>
        <section class="card"><span class="eyebrow">Semantic cache</span><strong class="metric">{{if .SemanticCacheRecommended}}Considered{{else}}Not recommended yet{{end}}</strong></section>
      </div>
      <h2>Cache Layer Contribution</h2>
      <table>
        <thead><tr><th>Layer</th><th>Count</th></tr></thead>
        <tbody>
          <tr><td>Exact</td><td>{{index .CacheLayerContribution "exact"}}</td></tr>
          <tr><td>Canonical</td><td>{{index .CacheLayerContribution "canonical"}}</td></tr>
          <tr><td>Semantic</td><td>{{index .CacheLayerContribution "semantic"}}</td></tr>
          {{if eq .CacheSource "observed"}}<tr><td>LN beta</td><td>{{index .CacheLayerContribution "ln_beta"}}</td></tr>{{end}}
        </tbody>
      </table>
      <h2>Bad Hits</h2>
      {{if .BadHits}}
      <ul>
        {{range .BadHits}}
        <li><code>{{.ID}}</code> · {{.Category}} · {{.CacheLayer}} · delta {{printf "%.1f" .TeacherDelta}}</li>
        {{end}}
      </ul>
      {{else}}
      <p>No bad hits in this report.</p>
      {{end}}
      <h2>JSON Summary</h2>
      <pre>{{json .}}</pre>
    </main>
  </body>
</html>`))
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return tpl.Execute(file, rep)
}

func pairID(item supportPair, index int) string {
	if strings.TrimSpace(item.ID) != "" {
		return strings.TrimSpace(item.ID)
	}
	return fmt.Sprintf("sample_%03d", index+1)
}

func defaultCost(value float64) float64 {
	if value > 0 {
		return value
	}
	return 0.002
}

func ensureModel(req cachepkg.Request) cachepkg.Request {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "benchmark-provider-model"
	}
	return req
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizePrompt(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func publicFreshAnswer(providerName string, model string, observe bool) (benchmark.FreshAnswerFunc, string, error) {
	name := strings.ToLower(strings.TrimSpace(providerName))
	if observe {
		if name == "" || name == "fake" {
			name = "openai"
		}
		if name != "openai" {
			return nil, "", fmt.Errorf("observe requires --provider openai")
		}
	}
	switch name {
	case "", "fake":
		return nil, "", nil
	case "openai":
		cfg := provider.LoadOpenAIConfigFromEnv()
		selectedModel := strings.TrimSpace(model)
		if selectedModel == "" {
			selectedModel = cfg.Model
		}
		if selectedModel == "" {
			return nil, "", fmt.Errorf("provider=openai requires --model or OPENAI_MODEL")
		}
		client, err := provider.NewOpenAIProvider(cfg, nil)
		if err != nil {
			return nil, "", err
		}
		cacheSource := ""
		if observe {
			cacheSource = "observed"
		}
		return func(ctx context.Context, item benchmark.Case) (cachepkg.Response, error) {
			req := item.Request
			req.Model = selectedModel
			if req.MaxTokens == nil {
				maxTokens := 64
				req.MaxTokens = &maxTokens
			}
			result, err := client.Complete(ctx, req)
			if err != nil {
				return cachepkg.Response{}, err
			}
			return cachepkg.Response{
				Text:             result.Response.Content,
				TeacherScore:     item.TeacherScore,
				PromptTokens:     result.Usage.PromptTokens,
				CompletionTokens: result.Usage.CompletionTokens,
				TotalTokens:      result.Usage.TotalTokens,
				CostUSD:          result.Cost,
				LatencyMS:        result.Response.LatencyMS,
				Observation:      result.Observation,
			}, nil
		}, cacheSource, nil
	default:
		return nil, "", fmt.Errorf("unsupported provider %q", providerName)
	}
}
