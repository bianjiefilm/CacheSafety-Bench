package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bianjiefilm/CacheSafety-Bench/internal/benchmark"
	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/embedding"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/provider"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/teacher"
)

func main() {
	dataset := flag.String("dataset", "testdata/benchmark/synthetic_v0.jsonl", "JSONL benchmark dataset path")
	seed := flag.Int64("seed", 42, "reproducibility seed")
	legacyThreshold := flag.Float64("threshold", -1, "deprecated alias for -semantic-threshold")
	semanticThreshold := flag.Float64("semantic-threshold", 0.95, "semantic cache threshold")
	semanticRerank := flag.String("semantic-rerank", "none", "semantic candidate rerank mode: none, answer_quality, or same_prompt_answer_quality")
	topKTrace := flag.Int("top-k-trace", 0, "record semantic topK candidates in decision log; 0 disables")
	thresholdScan := flag.String("threshold-scan", "", "comma-separated semantic thresholds to scan, for example 0.95,0.90,0.85")
	shuffle := flag.Bool("shuffle", false, "shuffle cases deterministically")
	providerName := flag.String("provider", "fake", "fresh-answer provider: fake, volcengine, or openai")
	model := flag.String("model", "", "model id for provider=volcengine or provider=openai")
	observe := flag.Bool("observe", false, "score cache layers from gateway serve-mode headers instead of the in-process pipeline")
	scorecardName := flag.String("scorecard", "lab", "scorecard: lab (default in-process) or publication (NextModel hosted formula)")
	promptsetPath := flag.String("promptset", benchmark.DefaultPromptSet, "publication promptset JSON path")
	maxSamples := flag.Int("max-samples", 0, "maximum number of dataset rows to run; 0 means all")
	startIndex := flag.Int("start-index", 0, "zero-based inclusive dataset start index for chunked runs")
	endIndex := flag.Int("end-index", 0, "zero-based exclusive dataset end index for chunked runs; 0 means dataset end")
	samplingMode := flag.String("sampling", "", "sampling mode: head, all, or stratified")
	stratified := flag.Bool("stratified", false, "use stratified sampling instead of simple head truncation")
	stratifyBy := flag.String("stratify-by", "", "comma-separated stratification fields: category,source_kind,expected_cache_behavior")
	maxPerStratum := flag.Int("max-per-stratum", 0, "maximum rows per stratum when -stratified is enabled")
	minPerStratum := flag.Int("min-per-stratum", 0, "minimum rows per stratum when -stratified is enabled")
	output := flag.String("output", "", "optional JSON output path")
	decisionLog := flag.String("decision-log", "", "optional JSONL decision log path")
	requestLogDB := flag.String("request-log-db", "", "optional SQLite request log database path")
	progressLog := flag.String("progress-log", "", "optional JSONL progress log path")
	checkpoint := flag.String("checkpoint", "", "optional checkpoint JSON path updated after each sample")
	timeoutPerSample := flag.Duration("timeout-per-sample", 0, "optional per-sample timeout, for example 120s")
	continueOnTimeout := flag.Bool("continue-on-timeout", false, "continue after per-sample timeout by recording an unknown miss")
	onlyReplayPairs := flag.Bool("only-replay-pairs", false, "only run replay_pair cases")
	sampleIDs := flag.String("sample-ids", "", "optional comma-separated sample ids to run")
	runID := flag.String("run-id", "", "optional request log run id")
	judgeName := flag.String("judge", "fake", "cache-hit judge: fake or volcengine")
	judgeModel := flag.String("judge-model", "", "model id for judge=volcengine; defaults to VOLCENGINE_JUDGE_MODEL or VOLCENGINE_MODEL_1")
	judgeOutput := flag.String("judge-output", "", "optional JSONL judge output path")
	embeddingName := flag.String("embedding", "fake", "semantic embedding mode: fake, hash, or volcengine")
	embeddingModel := flag.String("embedding-model", "", "model id for embedding=volcengine; defaults to VOLCENGINE_EMBEDDING_MODEL")
	vectorStore := flag.String("vector-store", "memory", "semantic vector store: memory, sqlite, or sqlite-int8")
	sqlitePath := flag.String("sqlite-path", "", "SQLite vector store path for -vector-store sqlite/sqlite-int8; defaults to a temp file")
	pricingPath := flag.String("pricing", "", "optional model pricing JSON path")
	inputPricePer1M := flag.Float64("input-price-per-1m", -1, "override default input price per 1M tokens")
	outputPricePer1M := flag.Float64("output-price-per-1m", -1, "override default output price per 1M tokens")
	judgePricePer1M := flag.Float64("judge-price-per-1m", -1, "override judge input/output price per 1M tokens")
	embeddingPricePer1M := flag.Float64("embedding-price-per-1m", -1, "override embedding price per 1M tokens")
	flag.Parse()
	if *legacyThreshold > 0 {
		*semanticThreshold = *legacyThreshold
	}
	scorecard, err := benchmark.NormalizeScorecard(*scorecardName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	switch strings.ToLower(strings.TrimSpace(*samplingMode)) {
	case "", "head", "all":
	case "stratified":
		*stratified = true
	default:
		fmt.Fprintf(os.Stderr, "unsupported sampling mode %q\n", *samplingMode)
		os.Exit(1)
	}

	if scorecard == benchmark.ScorecardPublication {
		if !*observe {
			fmt.Fprintf(os.Stderr, "scorecard=publication requires -observe; refusing to score the lab pipeline as publication\n")
			os.Exit(1)
		}
		if strings.TrimSpace(*thresholdScan) != "" {
			fmt.Fprintf(os.Stderr, "scorecard=publication does not support -threshold-scan\n")
			os.Exit(1)
		}
	}

	if *observe {
		if strings.TrimSpace(*providerName) == "" || strings.EqualFold(strings.TrimSpace(*providerName), "fake") {
			*providerName = "openai"
		}
		if !strings.EqualFold(strings.TrimSpace(*providerName), "openai") {
			fmt.Fprintf(os.Stderr, "observe requires provider=openai\n")
			os.Exit(1)
		}
	}

	if scorecard == benchmark.ScorecardPublication {
		fresh, err := freshAnswerFunc(*providerName, *model)
		if err != nil {
			fmt.Fprintf(os.Stderr, "configure provider: %v\n", err)
			os.Exit(1)
		}
		set, err := benchmark.LoadPromptSet(*promptsetPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load promptset: %v\n", err)
			os.Exit(1)
		}
		publicationPrice := 0.0
		if *inputPricePer1M > 0 {
			publicationPrice = *inputPricePer1M
		}
		score, decisionRecords, err := benchmark.RunPublication(context.Background(), set, benchmark.Config{
			Dataset:                    *promptsetPath,
			CacheSource:                "observed",
			PublicationInputPricePer1M: publicationPrice,
		}, fresh)
		if err != nil {
			fmt.Fprintf(os.Stderr, "run publication scorecard: %v\n", err)
			os.Exit(1)
		}
		if err := benchmark.WriteDecisionLog(*decisionLog, decisionRecords); err != nil {
			fmt.Fprintf(os.Stderr, "write decision log: %v\n", err)
			os.Exit(1)
		}
		writeEncodedOutput(*output, score)
		return
	}

	cases, err := benchmark.LoadJSONL(*dataset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load dataset: %v\n", err)
		os.Exit(1)
	}

	fresh, err := freshAnswerFunc(*providerName, *model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure provider: %v\n", err)
		os.Exit(1)
	}
	judge, judgeMode, err := judgeFunc(*judgeName, *judgeModel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure judge: %v\n", err)
		os.Exit(1)
	}
	embedder, embeddingMode, err := embeddingFunc(*embeddingName, *embeddingModel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure embedding: %v\n", err)
		os.Exit(1)
	}
	pricing, err := benchmark.LoadPricingConfig(*pricingPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load pricing: %v\n", err)
		os.Exit(1)
	}
	pricing.ApplyOverrides(*inputPricePer1M, *outputPricePer1M, *judgePricePer1M, *embeddingPricePer1M)

	cfg := benchmark.Config{
		Seed:              *seed,
		SemanticThreshold: *semanticThreshold,
		SemanticRerank:    *semanticRerank,
		TopKTrace:         *topKTrace,
		Shuffle:           *shuffle,
		MaxSamples:        *maxSamples,
		StartIndex:        *startIndex,
		EndIndex:          *endIndex,
		Stratified:        *stratified,
		StratifyBy:        splitCSV(*stratifyBy),
		MaxPerStratum:     *maxPerStratum,
		MinPerStratum:     *minPerStratum,
		ProgressLogPath:   *progressLog,
		CheckpointPath:    *checkpoint,
		TimeoutPerSample:  *timeoutPerSample,
		ContinueOnTimeout: *continueOnTimeout,
		OnlyReplayPairs:   *onlyReplayPairs,
		SampleIDs:         splitCSV(*sampleIDs),
		JudgeMode:         judgeMode,
		EmbeddingMode:     embeddingMode,
		EmbeddingModel:    *embeddingModel,
		VectorStoreMode:   strings.ToLower(strings.TrimSpace(*vectorStore)),
		SQLitePath:        *sqlitePath,
		Dataset:           *dataset,
		Pricing:           pricing,
		CacheSource:       cacheSource(*observe),
	}
	startedAt := time.Now().UTC()
	selectedRunID := strings.TrimSpace(*runID)
	if selectedRunID == "" {
		selectedRunID = benchmark.DefaultRunID(*dataset, startedAt)
	}
	var outputValue any
	var judgeRecords []teacher.Record
	var decisionRecords []benchmark.DecisionRecord
	if strings.TrimSpace(*thresholdScan) != "" {
		thresholds, err := parseThresholds(*thresholdScan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse threshold scan: %v\n", err)
			os.Exit(1)
		}
		fresh = cachedFreshAnswer(fresh)
		judge = cachedJudge(judge)
		embedder = cachedEmbedder(embedder)
		scan, err := benchmark.RunThresholdScan(context.Background(), cases, cfg, thresholds, fresh, judge, embedder)
		if err != nil {
			fmt.Fprintf(os.Stderr, "run benchmark: %v\n", err)
			os.Exit(1)
		}
		outputValue = scan
		for _, metrics := range scan.Results {
			judgeRecords = append(judgeRecords, metrics.JudgeRecords...)
			decisionRecords = append(decisionRecords, metrics.DecisionRecords...)
			if err := writeBadHits(*output, metrics); err != nil {
				fmt.Fprintf(os.Stderr, "write bad hit examples: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		metrics, err := benchmark.RunWithEmbedding(context.Background(), cases, cfg, fresh, judge, embedder)
		if err != nil {
			fmt.Fprintf(os.Stderr, "run benchmark: %v\n", err)
			os.Exit(1)
		}
		outputValue = metrics
		judgeRecords = metrics.JudgeRecords
		decisionRecords = metrics.DecisionRecords
		if err := writeBadHits(*output, metrics); err != nil {
			fmt.Fprintf(os.Stderr, "write bad hit examples: %v\n", err)
			os.Exit(1)
		}
	}
	if err := benchmark.WriteJudgeOutput(*judgeOutput, judgeRecords); err != nil {
		fmt.Fprintf(os.Stderr, "write judge output: %v\n", err)
		os.Exit(1)
	}
	if err := benchmark.WriteDecisionLog(*decisionLog, decisionRecords); err != nil {
		fmt.Fprintf(os.Stderr, "write decision log: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(*requestLogDB) != "" {
		if err := writeRequestLog(*requestLogDB, benchmark.BenchmarkRunLog{
			RunID:         selectedRunID,
			Dataset:       *dataset,
			ProviderMode:  strings.ToLower(strings.TrimSpace(*providerName)),
			JudgeMode:     judgeMode,
			EmbeddingMode: embeddingMode,
			VectorStore:   strings.ToLower(strings.TrimSpace(*vectorStore)),
			VectorDim:     outputVectorDim(outputValue),
			Threshold:     outputThreshold(outputValue, *semanticThreshold),
			Seed:          *seed,
			StartedAt:     startedAt,
			FinishedAt:    time.Now().UTC(),
		}, outputValue, decisionRecords); err != nil {
			fmt.Fprintf(os.Stderr, "write request log: %v\n", err)
			os.Exit(1)
		}
	}

	writeEncodedOutput(*output, outputValue)
}

func writeEncodedOutput(output string, value any) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintf(os.Stderr, "encode metrics: %v\n", err)
		os.Exit(1)
	}
	if output != "" {
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(output, buf.Bytes(), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write output: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Print(buf.String())
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func writeRequestLog(path string, run benchmark.BenchmarkRunLog, metrics any, records []benchmark.DecisionRecord) error {
	store, err := benchmark.OpenRequestLogStore(context.Background(), path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	return store.Write(context.Background(), benchmark.RequestLogWrite{
		Run:     run,
		Metrics: metrics,
		Records: records,
	})
}

func outputVectorDim(value any) int {
	switch typed := value.(type) {
	case benchmark.Metrics:
		return typed.VectorDim
	case benchmark.ThresholdScan:
		if len(typed.Results) > 0 {
			return typed.Results[0].VectorDim
		}
	}
	return 0
}

func outputThreshold(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case benchmark.Metrics:
		return typed.Threshold
	case benchmark.ThresholdScan:
		if len(typed.Results) > 0 {
			return typed.Results[0].Threshold
		}
	}
	return fallback
}

func parseThresholds(value string) ([]float64, error) {
	parts := strings.Split(value, ",")
	thresholds := make([]float64, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		threshold, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid threshold %q: %w", trimmed, err)
		}
		if threshold <= 0 || threshold > 1 {
			return nil, fmt.Errorf("threshold must be in (0,1]: %g", threshold)
		}
		thresholds = append(thresholds, threshold)
	}
	if len(thresholds) == 0 {
		return nil, fmt.Errorf("no thresholds provided")
	}
	return thresholds, nil
}

func writeBadHits(output string, metrics benchmark.Metrics) error {
	dir := filepath.Join("testdata", "regression", "bad_hits")
	if strings.TrimSpace(output) != "" {
		dir = filepath.Join(filepath.Dir(output), "bad_hits")
	}
	if len(metrics.BadHits) == 0 {
		path := filepath.Join(dir, fmt.Sprintf("threshold_%.2f.jsonl", metrics.Threshold))
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return benchmark.WriteBadHitExamples(dir, metrics.Threshold, metrics.BadHits)
}

func cachedFreshAnswer(inner benchmark.FreshAnswerFunc) benchmark.FreshAnswerFunc {
	if inner == nil {
		return nil
	}
	type cachedResult struct {
		resp cachepkg.Response
		err  error
	}
	cache := make(map[string]cachedResult)
	return func(ctx context.Context, item benchmark.Case) (cachepkg.Response, error) {
		key := item.ID + "\n" + cachepkg.PromptText(item.Request)
		if result, ok := cache[key]; ok {
			return result.resp, result.err
		}
		resp, err := inner(ctx, item)
		cache[key] = cachedResult{resp: resp, err: err}
		return resp, err
	}
}

func cachedJudge(inner teacher.Judge) teacher.Judge {
	if inner == nil {
		return nil
	}
	return &judgeCache{inner: inner, results: make(map[string]judgeCacheResult)}
}

type judgeCache struct {
	inner   teacher.Judge
	results map[string]judgeCacheResult
}

type judgeCacheResult struct {
	result teacher.JudgeResult
	err    error
}

func (j *judgeCache) Score(ctx context.Context, input teacher.JudgeInput) (teacher.JudgeResult, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return teacher.JudgeResult{}, err
	}
	key := string(payload)
	if cached, ok := j.results[key]; ok {
		return cached.result, cached.err
	}
	result, err := j.inner.Score(ctx, input)
	j.results[key] = judgeCacheResult{result: result, err: err}
	return result, err
}

func cachedEmbedder(inner embedding.Embedder) embedding.Embedder {
	if inner == nil {
		return nil
	}
	return &embedderCache{inner: inner, results: make(map[string]embedderCacheResult)}
}

type embedderCache struct {
	inner   embedding.Embedder
	results map[string]embedderCacheResult
}

type embedderCacheResult struct {
	result embedding.EmbedResult
	err    error
}

func (e *embedderCache) Embed(ctx context.Context, req embedding.EmbedRequest) (embedding.EmbedResult, error) {
	key := req.Model + "\n" + req.Text
	if cached, ok := e.results[key]; ok {
		return cached.result, cached.err
	}
	result, err := e.inner.Embed(ctx, req)
	e.results[key] = embedderCacheResult{result: result, err: err}
	return result, err
}

func embeddingFunc(embeddingName string, model string) (embedding.Embedder, string, error) {
	switch strings.ToLower(strings.TrimSpace(embeddingName)) {
	case "", "fake":
		return embedding.FakeEmbedder{}, "fake", nil
	case "hash":
		return embedding.LocalHashEmbedder{}, "hash", nil
	case "volcengine":
		embedder, enabled, err := embedding.NewVolcengineEmbedderFromEnv(model)
		if err != nil {
			return nil, "", err
		}
		if !enabled {
			return nil, "fixture", nil
		}
		return embedder, "volcengine", nil
	default:
		return nil, "", fmt.Errorf("unsupported embedding %q", embeddingName)
	}
}

func judgeFunc(judgeName string, model string) (teacher.Judge, string, error) {
	switch strings.ToLower(strings.TrimSpace(judgeName)) {
	case "", "fake":
		return teacher.FakeJudge{}, "fake", nil
	case "volcengine":
		judge, enabled, err := teacher.NewVolcengineJudgeFromEnv(model)
		if err != nil {
			return nil, "", err
		}
		if !enabled {
			return nil, "fixture", nil
		}
		return judge, "volcengine", nil
	default:
		return nil, "", fmt.Errorf("unsupported judge %q", judgeName)
	}
}

func cacheSource(observe bool) string {
	if observe {
		return "observed"
	}
	return ""
}

func providerToCacheResponse(item benchmark.Case, result provider.Result) cachepkg.Response {
	return cachepkg.Response{
		Text:             result.Response.Content,
		TeacherScore:     item.TeacherScore,
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
		CostUSD:          result.Cost,
		LatencyMS:        result.Response.LatencyMS,
		Observation:      result.Observation,
	}
}

func openaiFreshAnswer(model string) (benchmark.FreshAnswerFunc, error) {
	cfg := provider.LoadOpenAIConfigFromEnv()
	selectedModel := strings.TrimSpace(model)
	if selectedModel == "" {
		selectedModel = cfg.Model
	}
	if selectedModel == "" {
		return nil, fmt.Errorf("provider=openai requires -model or OPENAI_MODEL")
	}
	client, err := provider.NewOpenAIProvider(cfg, nil)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return func(ctx context.Context, item benchmark.Case) (cachepkg.Response, error) {
		req := item.Request
		req.Model = selectedModel
		if req.MaxTokens == nil {
			maxTokens := 64
			req.MaxTokens = &maxTokens
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		result, err := client.Complete(ctx, req)
		if err != nil {
			return cachepkg.Response{}, err
		}
		return providerToCacheResponse(item, result), nil
	}, nil
}

func freshAnswerFunc(providerName string, model string) (benchmark.FreshAnswerFunc, error) {
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "", "fake":
		return nil, nil
	case "openai":
		return openaiFreshAnswer(model)
	case "volcengine":
		cfg := provider.LoadVolcengineConfigFromEnv()
		selectedModel := strings.TrimSpace(model)
		if selectedModel == "" && len(cfg.Models) > 0 {
			selectedModel = cfg.Models[0]
		}
		if selectedModel == "" {
			return nil, fmt.Errorf("provider=volcengine requires -model or VOLCENGINE_MODEL_1")
		}
		volc, err := provider.NewVolcengineProvider(cfg)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, item benchmark.Case) (cachepkg.Response, error) {
			req := item.Request
			req.Model = selectedModel
			if req.MaxTokens == nil {
				maxTokens := 64
				req.MaxTokens = &maxTokens
			}
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			result, err := volc.Complete(ctx, req)
			if err != nil {
				return cachepkg.Response{}, err
			}
			return providerToCacheResponse(item, result), nil
		}, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", providerName)
	}
}
