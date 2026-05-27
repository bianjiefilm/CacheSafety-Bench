package benchmark

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

type ImportEvalOptions struct {
	InputPath       string
	InputFormat     string
	OutputPath      string
	DefaultCategory string
	DefaultRisk     string
	DefaultCost     float64
	MaxSamples      int
	PairMode        string
}

type ImportEvalSummary struct {
	Total       int            `json:"total"`
	ByCategory  map[string]int `json:"by_category"`
	ByRisk      map[string]int `json:"by_risk"`
	Output      string         `json:"output,omitempty"`
	InputFormat string         `json:"input_format"`
}

type rawImportRecord struct {
	ID                    string
	Prompt                string
	Answer                string
	OldRequest            string
	OldAnswer             string
	NewRequest            string
	Category              string
	ExpectedRisk          string
	EstimatedUpstreamCost float64
	Request               *cachepkg.Request
}

func ImportEvalDataset(opts ImportEvalOptions) ([]Case, ImportEvalSummary, error) {
	format := strings.ToLower(strings.TrimSpace(opts.InputFormat))
	if format == "" {
		return nil, ImportEvalSummary{}, fmt.Errorf("input format is required")
	}
	raw, err := loadRawImportRecords(opts.InputPath, format)
	if err != nil {
		return nil, ImportEvalSummary{}, err
	}
	cases := make([]Case, 0, len(raw))
	summary := ImportEvalSummary{
		ByCategory:  map[string]int{},
		ByRisk:      map[string]int{},
		InputFormat: format,
		Output:      filepath.Base(opts.OutputPath),
	}
	for i, item := range raw {
		out := normalizeImportRecord(item, i, opts)
		cases = append(cases, out)
		summary.ByCategory[out.Category]++
		summary.ByRisk[out.ExpectedRisk]++
		if opts.MaxSamples > 0 && len(cases) >= opts.MaxSamples {
			break
		}
	}
	summary.Total = len(cases)
	return cases, summary, nil
}

func WriteImportedEvalDataset(path string, cases []Case) error {
	if path == "" {
		return fmt.Errorf("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	encoder := json.NewEncoder(file)
	for _, item := range cases {
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

func loadRawImportRecords(path, format string) ([]rawImportRecord, error) {
	switch format {
	case "jsonl":
		return loadRawImportJSONL(path)
	case "csv":
		return loadRawImportCSV(path)
	case "text":
		return loadRawImportText(path)
	default:
		return nil, fmt.Errorf("unsupported input format %q", format)
	}
}

func loadRawImportJSONL(path string) ([]rawImportRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	out := []rawImportRecord{}
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
			return nil, fmt.Errorf("decode jsonl line %d: %w", line, err)
		}
		item, err := rawImportFromMap(payload)
		if err != nil {
			return nil, fmt.Errorf("jsonl line %d: %w", line, err)
		}
		out = append(out, item)
	}
	return out, scanner.Err()
}

func loadRawImportCSV(path string) ([]rawImportRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	index := map[string]int{}
	for i, name := range header {
		index[strings.ToLower(strings.TrimSpace(name))] = i
	}
	out := []rawImportRecord{}
	for rowNum := 2; ; rowNum++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv row %d: %w", rowNum, err)
		}
		item := rawImportRecord{
			ID:           csvValue(record, index, "id"),
			Prompt:       csvValue(record, index, "prompt"),
			Answer:       csvValue(record, index, "answer"),
			OldRequest:   csvValue(record, index, "old_request"),
			OldAnswer:    csvValue(record, index, "old_answer"),
			NewRequest:   csvValue(record, index, "new_request"),
			Category:     csvValue(record, index, "category"),
			ExpectedRisk: csvValue(record, index, "expected_risk"),
		}
		if value := csvValue(record, index, "estimated_upstream_cost"); value != "" {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				item.EstimatedUpstreamCost = parsed
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func loadRawImportText(path string) ([]rawImportRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	out := []rawImportRecord{}
	for line := 1; scanner.Scan(); line++ {
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		out = append(out, rawImportRecord{
			ID:     fmt.Sprintf("text-%03d", line),
			Prompt: prompt,
		})
	}
	return out, scanner.Err()
}

func rawImportFromMap(payload map[string]any) (rawImportRecord, error) {
	item := rawImportRecord{
		ID:           stringValue(payload["id"]),
		Prompt:       stringValue(payload["prompt"]),
		Answer:       firstNonEmpty(stringValue(payload["fresh_answer"]), stringValue(payload["answer"])),
		OldRequest:   stringValue(payload["old_request"]),
		OldAnswer:    stringValue(payload["old_answer"]),
		NewRequest:   stringValue(payload["new_request"]),
		Category:     stringValue(payload["category"]),
		ExpectedRisk: firstNonEmpty(stringValue(payload["expected_risk"]), stringValue(payload["risk"])),
	}
	if value, ok := floatValue(payload["estimated_upstream_cost"]); ok {
		item.EstimatedUpstreamCost = value
	}
	if value, ok := payload["request"]; ok {
		req, err := requestFromAny(value)
		if err != nil {
			return item, err
		}
		item.Request = &req
	}
	if item.Request == nil {
		if value, ok := payload["messages"]; ok {
			req, err := requestFromMessages(value)
			if err != nil {
				return item, err
			}
			item.Request = &req
		}
	}
	return item, nil
}

func normalizeImportRecord(item rawImportRecord, index int, opts ImportEvalOptions) Case {
	if strings.TrimSpace(item.ID) == "" {
		item.ID = fmt.Sprintf("imported-%03d", index+1)
	}
	prompt := firstNonEmpty(item.NewRequest, item.Prompt)
	request := item.Request
	if request == nil {
		req := cachepkg.Request{
			Model:    "benchmark-provider-model",
			Messages: []cachepkg.Message{{Role: "user", Content: strings.TrimSpace(prompt)}},
		}
		request = &req
	}
	category, risk := inferCategoryAndRisk(item, opts)
	cost := item.EstimatedUpstreamCost
	if cost <= 0 {
		cost = opts.DefaultCost
	}
	if cost <= 0 {
		cost = 1
	}
	out := Case{
		ID:                    item.ID,
		Kind:                  "single_request",
		Category:              category,
		ExpectedRisk:          risk,
		Request:               *request,
		FreshAnswer:           strings.TrimSpace(item.Answer),
		TeacherScore:          10,
		FreshCost:             cost,
		CachedCost:            0.1,
		EstimatedUpstreamCost: cost,
	}
	switch strings.ToLower(strings.TrimSpace(opts.PairMode)) {
	case "exact_duplicate":
		out.Kind = "exact_duplicate"
		out.OldRequest = cachepkg.PromptText(out.Request)
		out.OldAnswer = out.FreshAnswer
		out.ExpectedCacheBehavior = "exact_duplicate"
	case "paraphrase_stub":
		out.Kind = "paraphrase_stub"
		out.ExpectedCacheBehavior = "paraphrase_stub"
		out.OldRequest = strings.TrimSpace(item.OldRequest)
		out.OldAnswer = strings.TrimSpace(item.OldAnswer)
	default:
		if strings.TrimSpace(item.OldRequest) != "" || strings.TrimSpace(item.OldAnswer) != "" {
			out.Kind = "imported_pair"
			out.OldRequest = strings.TrimSpace(item.OldRequest)
			out.OldAnswer = strings.TrimSpace(item.OldAnswer)
			out.ExpectedCacheBehavior = strings.TrimSpace(itemBehaviorHint(item))
		}
	}
	return out
}

func itemBehaviorHint(item rawImportRecord) string {
	if strings.TrimSpace(item.OldRequest) == "" {
		return ""
	}
	if strings.TrimSpace(item.NewRequest) != "" && strings.TrimSpace(item.OldRequest) == strings.TrimSpace(item.NewRequest) {
		return "exact_duplicate"
	}
	return "imported_pair"
}

func inferCategoryAndRisk(item rawImportRecord, opts ImportEvalOptions) (string, string) {
	prompt := strings.ToLower(firstNonEmpty(item.NewRequest, item.Prompt, cachePrompt(item.Request)))
	if strings.TrimSpace(item.Category) != "" {
		category := item.Category
		risk := item.ExpectedRisk
		if strings.TrimSpace(risk) == "" {
			risk = defaultRiskForCategory(category)
		}
		return category, risk
	}
	if strings.TrimSpace(opts.DefaultCategory) != "" && strings.TrimSpace(opts.DefaultRisk) != "" {
		return opts.DefaultCategory, opts.DefaultRisk
	}
	switch {
	case containsAnyLiteralLower(prompt, []string{"medical", "medicine", "dose", "dosage", "symptom", "diagnosis", "legal", "lawsuit", "contract", "financial", "investment", "stock", "today", "latest", "current", "now", "医疗", "法律", "金融", "今天", "最新", "当前", "现在"}):
		return "hard_negative", "high"
	case containsAnyLiteralLower(prompt, []string{"translate", "translation", "翻译"}):
		return "loose_translation", "low"
	case containsAnyLiteralLower(prompt, []string{"rewrite", "polish", "rephrase", "润色", "改写"}):
		return "rewrite_polish", "low"
	case containsAnyLiteralLower(prompt, []string{"password", "subscription", "account", "订阅", "密码", "账号", "billing"}):
		return "faq_generalization", "low"
	case containsAnyLiteralLower(prompt, []string{"what is", "what's", "explain", "解释", "什么是"}):
		return "general_explanation", "low"
	case containsAnyLiteralLower(prompt, []string{"how to", "how do i", "how should i", "how can i", "如何"}):
		return "howto_guidance", "low"
	}
	category := opts.DefaultCategory
	if strings.TrimSpace(category) == "" {
		category = "imported_general"
	}
	risk := opts.DefaultRisk
	if strings.TrimSpace(risk) == "" {
		risk = "medium"
	}
	return category, risk
}

func defaultRiskForCategory(category string) string {
	switch category {
	case "hard_negative", "safety_refusal":
		return "high"
	default:
		return "low"
	}
}

func cachePrompt(req *cachepkg.Request) string {
	if req == nil {
		return ""
	}
	return cachepkg.PromptText(*req)
}

func requestFromAny(value any) (cachepkg.Request, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return cachepkg.Request{}, err
	}
	var req cachepkg.Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return cachepkg.Request{}, err
	}
	if req.Model == "" {
		req.Model = "benchmark-provider-model"
	}
	return req, nil
}

func requestFromMessages(value any) (cachepkg.Request, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return cachepkg.Request{}, err
	}
	var messages []cachepkg.Message
	if err := json.Unmarshal(payload, &messages); err != nil {
		return cachepkg.Request{}, err
	}
	return cachepkg.Request{
		Model:    "benchmark-provider-model",
		Messages: messages,
	}, nil
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func csvValue(row []string, index map[string]int, key string) string {
	pos, ok := index[key]
	if !ok || pos >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[pos])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func containsAnyLiteralLower(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}
