package benchmark

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

type ShadowTrafficImportOptions struct {
	InputPath   string
	InputFormat string
	OutputPath  string
	MaxSamples  int
	Redact      bool
	Seed        int64
}

type ShadowTrafficImportSummary struct {
	TotalRead            int            `json:"total_read"`
	Imported             int            `json:"total_imported"`
	RedactedCount        int            `json:"redacted_count"`
	DroppedCount         int            `json:"dropped_count"`
	ByCategory           map[string]int `json:"by_category"`
	ByExpectedRisk       map[string]int `json:"by_expected_risk"`
	ToolCallCount        int            `json:"tool_call_count"`
	TimeSensitiveCount   int            `json:"time_sensitive_count"`
	DomainSensitiveCount int            `json:"domain_sensitive_count"`
	FormatSensitiveCount int            `json:"format_sensitive_count"`
	LongPromptCount      int            `json:"long_prompt_count"`
	SingleRequestCount   int            `json:"single_request_count"`
	ReplayPairCount      int            `json:"replay_pair_count"`
	QualityComplete      int            `json:"quality_complete"`
	QualityTruncated     int            `json:"quality_suspected_truncated"`
	QualityEmpty         int            `json:"quality_empty"`
	QualityTooShort      int            `json:"quality_too_short"`
	QualityError         int            `json:"quality_error"`
	ExcludedFromReplay   int            `json:"excluded_from_replay_count"`
	ExactQualityFailed   int            `json:"exact_quality_failed_count"`
	ReplayPairAfterGate  int            `json:"replay_pair_after_quality_gate"`
	Output               string         `json:"output,omitempty"`
	ReportPath           string         `json:"report_path,omitempty"`
	InputFormat          string         `json:"input_format"`
}

type shadowRawTrafficRecord struct {
	ID               string
	RequestID        string
	Timestamp        string
	Model            string
	Messages         []cachepkg.Message
	Prompt           string
	Response         string
	Category         string
	ExpectedRisk     string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	HasToolCall      bool
	FinishReason     string
	Status           string
	ErrorType        string
}

type redactionResult struct {
	Value   string
	Changed bool
}

var (
	emailPattern         = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	phonePattern         = regexp.MustCompile(`(?i)(?:\+?\d[\d\s().-]{7,}\d)`)
	urlPattern           = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+`)
	longNumberPattern    = regexp.MustCompile(`\b\d{8,}\b`)
	bearerPattern        = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	authorizationPattern = regexp.MustCompile(`(?i)\bAuthorization\s*:\s*[^\s]+(?:\s+[^\s]+)?`)
	apiKeyPattern        = regexp.MustCompile(`(?i)\b(?:api[_-]?key|token|secret)\s*[:=]\s*[A-Za-z0-9._~+/=-]+`)
	arkKeyPattern        = regexp.MustCompile(`\bark-[A-Za-z0-9-]{12,}\b`)
	secretMarkerPattern  = regexp.MustCompile(`(?i)\b(?:Authorization|Bearer|api[_-]?key|secret)\b\s*[:=]?`)
	exactWordPattern     = regexp.MustCompile(`(?i)[a-z]{3,}|\d+(?:\.\d+)?`)
	exactNumberPattern   = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	exactEntityPattern   = regexp.MustCompile(`\b(?:[A-Z][a-z]{2,}|[A-Z]{2,}(?:[.-]?[A-Z0-9]+)*)\b`)
)

func ImportShadowTraffic(opts ShadowTrafficImportOptions) ([]Case, ShadowTrafficImportSummary, error) {
	format := strings.ToLower(strings.TrimSpace(opts.InputFormat))
	if format == "" {
		return nil, ShadowTrafficImportSummary{}, fmt.Errorf("input format is required")
	}
	raw, err := loadShadowTrafficRecords(opts.InputPath, format)
	if err != nil {
		return nil, ShadowTrafficImportSummary{}, err
	}
	if opts.Seed != 0 {
		rng := rand.New(rand.NewSource(opts.Seed))
		rng.Shuffle(len(raw), func(i, j int) { raw[i], raw[j] = raw[j], raw[i] })
	}
	summary := ShadowTrafficImportSummary{
		ByCategory:     map[string]int{},
		ByExpectedRisk: map[string]int{},
		InputFormat:    format,
		Output:         filepath.Base(opts.OutputPath),
		ReportPath:     DefaultShadowTrafficImportReportPath(opts.OutputPath),
	}
	cases := make([]Case, 0, len(raw))
	seen := map[string]Case{}
	for i, record := range raw {
		summary.TotalRead++
		item, dropped := shadowTrafficCase(record, i, opts.Redact)
		if dropped {
			summary.DroppedCount++
			continue
		}
		if item.RedactionApplied {
			summary.RedactedCount++
		}
		switch item.ResponseQuality {
		case "complete":
			summary.QualityComplete++
		case "suspected_truncated":
			summary.QualityTruncated++
		case "empty":
			summary.QualityEmpty++
		case "too_short":
			summary.QualityTooShort++
		case "error":
			summary.QualityError++
		}
		if item.ExcludeFromReplay {
			summary.ExcludedFromReplay++
		}
		if item.ExpectedRisk == "tool_call" {
			summary.ToolCallCount++
		}
		switch item.Category {
		case "time_sensitive":
			summary.TimeSensitiveCount++
		case "domain_sensitive":
			summary.DomainSensitiveCount++
		case "format_sensitive":
			summary.FormatSensitiveCount++
		case "long_prompt":
			summary.LongPromptCount++
		}
		pair, exactExcludeReason := maybeShadowReplayPair(item, seen)
		if exactExcludeReason != "" {
			item.ExcludeFromReplay = true
			item.ExcludeReason = exactExcludeReason
			summary.ExcludedFromReplay++
			if exactExcludeReason == "exact_quality_failed" {
				summary.ExactQualityFailed++
			}
		}
		key := shadowDuplicateKey(item)
		if key != "" && shadowTrafficPairEligible(item) {
			if _, ok := seen[key]; !ok {
				seen[key] = item
			}
		}
		if pair != nil {
			item = *pair
			summary.ReplayPairCount++
			summary.ReplayPairAfterGate++
		} else {
			summary.SingleRequestCount++
		}
		cases = append(cases, item)
		summary.ByCategory[item.Category]++
		summary.ByExpectedRisk[item.ExpectedRisk]++
		if opts.MaxSamples > 0 && len(cases) >= opts.MaxSamples {
			break
		}
	}
	summary.Imported = len(cases)
	return cases, summary, nil
}

func WriteShadowTrafficDataset(path string, cases []Case, summary ShadowTrafficImportSummary) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("output path is required")
	}
	if err := WriteImportedEvalDataset(path, cases); err != nil {
		return err
	}
	reportPath := DefaultShadowTrafficImportReportPath(path)
	if err := writeJSONFile(reportPath, summary); err != nil {
		return err
	}
	return nil
}

func DefaultShadowTrafficImportReportPath(outputPath string) string {
	base := strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath))
	if base == "" || base == "." {
		base = "shadow_traffic_v0"
	}
	return filepath.Join("results", base+"_import_report.json")
}

func loadShadowTrafficRecords(path, format string) ([]shadowRawTrafficRecord, error) {
	switch format {
	case "jsonl":
		return loadShadowTrafficJSONL(path)
	case "csv":
		return loadShadowTrafficCSV(path)
	default:
		return nil, fmt.Errorf("unsupported input format %q", format)
	}
}

func loadShadowTrafficJSONL(path string) ([]shadowRawTrafficRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := []shadowRawTrafficRecord{}
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
		record, err := shadowTrafficFromMap(payload)
		if err != nil {
			return nil, fmt.Errorf("jsonl line %d: %w", line, err)
		}
		out = append(out, record)
	}
	return out, scanner.Err()
}

func loadShadowTrafficCSV(path string) ([]shadowRawTrafficRecord, error) {
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
	out := []shadowRawTrafficRecord{}
	for rowNum := 2; ; rowNum++ {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv row %d: %w", rowNum, err)
		}
		record := shadowRawTrafficRecord{
			ID:               csvValue(row, index, "id"),
			RequestID:        csvValue(row, index, "request_id"),
			Model:            csvValue(row, index, "model"),
			Prompt:           csvValue(row, index, "prompt"),
			Response:         firstNonEmpty(csvValue(row, index, "response"), csvValue(row, index, "answer")),
			Category:         csvValue(row, index, "category"),
			ExpectedRisk:     csvValue(row, index, "expected_risk"),
			PromptTokens:     parseIntDefault(csvValue(row, index, "prompt_tokens"), 0),
			CompletionTokens: parseIntDefault(csvValue(row, index, "completion_tokens"), 0),
			FinishReason:     csvValue(row, index, "finish_reason"),
			Status:           csvValue(row, index, "status"),
			ErrorType:        csvValue(row, index, "error_type"),
		}
		record.TotalTokens = record.PromptTokens + record.CompletionTokens
		out = append(out, record)
	}
	return out, nil
}

func shadowTrafficFromMap(payload map[string]any) (shadowRawTrafficRecord, error) {
	record := shadowRawTrafficRecord{
		ID:           stringValue(payload["id"]),
		RequestID:    stringValue(payload["request_id"]),
		Timestamp:    stringValue(payload["timestamp"]),
		Model:        stringValue(payload["model"]),
		Prompt:       stringValue(payload["prompt"]),
		Response:     firstNonEmpty(stringValue(payload["response"]), stringValue(payload["answer"])),
		Category:     stringValue(payload["category"]),
		ExpectedRisk: firstNonEmpty(stringValue(payload["expected_risk"]), stringValue(payload["risk"])),
		FinishReason: stringValue(payload["finish_reason"]),
		Status:       stringValue(payload["status"]),
		ErrorType:    firstNonEmpty(stringValue(payload["error_type"]), stringValue(payload["error"])),
	}
	if value, ok := payload["messages"]; ok {
		messages, err := shadowMessagesFromAny(value)
		if err != nil {
			return record, err
		}
		record.Messages = messages
	}
	if requestValue, ok := payload["request"]; ok {
		if req, err := requestFromAny(requestValue); err == nil {
			if record.Model == "" {
				record.Model = req.Model
			}
			record.Messages = req.Messages
			record.HasToolCall = record.HasToolCall || len(req.Tools) > 0 || hasFunctionCallExtra(req.Extra)
		}
	}
	record.HasToolCall = record.HasToolCall || payload["tools"] != nil || payload["function_call"] != nil || payload["tool_choice"] != nil
	if usage, ok := payload["usage"].(map[string]any); ok {
		record.PromptTokens = intFromAny(usage["prompt_tokens"])
		record.CompletionTokens = intFromAny(usage["completion_tokens"])
		record.TotalTokens = intFromAny(usage["total_tokens"])
	}
	if record.TotalTokens == 0 {
		record.TotalTokens = record.PromptTokens + record.CompletionTokens
	}
	return record, nil
}

func shadowMessagesFromAny(value any) ([]cachepkg.Message, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var raw []map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	out := make([]cachepkg.Message, 0, len(raw))
	for _, item := range raw {
		role := stringValue(item["role"])
		content := stringValue(item["content"])
		if strings.TrimSpace(role) == "" {
			role = "user"
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		out = append(out, cachepkg.Message{Role: role, Content: content})
	}
	return out, nil
}

func shadowTrafficCase(record shadowRawTrafficRecord, index int, redact bool) (Case, bool) {
	messages := record.Messages
	if len(messages) == 0 && strings.TrimSpace(record.Prompt) != "" {
		messages = []cachepkg.Message{{Role: "user", Content: record.Prompt}}
	}
	if len(messages) == 0 {
		return Case{}, true
	}
	redacted := false
	if redact {
		for i := range messages {
			result := redactShadowTrafficText(messages[i].Content)
			messages[i].Content = result.Value
			redacted = redacted || result.Changed
		}
		result := redactShadowTrafficText(record.Response)
		record.Response = result.Value
		redacted = redacted || result.Changed
	}
	modelAlias := ModelAlias(record.Model)
	if modelAlias == "" {
		modelAlias = "model_unknown"
	}
	req := cachepkg.Request{
		Model:    modelAlias,
		Messages: messages,
	}
	if record.HasToolCall {
		req.Extra = map[string]any{"function_call": true}
	}
	prompt := cachepkg.PromptText(req)
	category, risk := classifyShadowTraffic(prompt, record.Category, record.ExpectedRisk, record.HasToolCall)
	quality, exclude, excludeReason := assessShadowResponseQuality(prompt, record.Response, record, category)
	id := strings.TrimSpace(record.ID)
	if id == "" {
		id = fmt.Sprintf("shadow-%06d", index+1)
	}
	if strings.TrimSpace(record.RequestID) != "" {
		redacted = true
	}
	if strings.TrimSpace(record.Timestamp) != "" {
		redacted = true
	}
	return Case{
		ID:                    id,
		Kind:                  "single_request",
		Category:              category,
		ExpectedRisk:          risk,
		Request:               req,
		FreshAnswer:           strings.TrimSpace(record.Response),
		TeacherScore:          10,
		FreshCost:             1,
		CachedCost:            0.1,
		EstimatedUpstreamCost: 1,
		SourceKind:            "shadow_traffic",
		ModelAlias:            modelAlias,
		PromptTokens:          record.PromptTokens,
		CompletionTokens:      record.CompletionTokens,
		TotalTokens:           record.TotalTokens,
		RedactionApplied:      redacted,
		FinishReason:          strings.ToLower(strings.TrimSpace(record.FinishReason)),
		ResponseQuality:       quality,
		ExcludeFromReplay:     exclude,
		ExcludeReason:         excludeReason,
	}, false
}

func maybeShadowReplayPair(item Case, seen map[string]Case) (*Case, string) {
	if !shadowTrafficPairEligible(item) {
		return nil, ""
	}
	key := shadowDuplicateKey(item)
	if key == "" {
		return nil, ""
	}
	old, ok := seen[key]
	if !ok || strings.TrimSpace(old.FreshAnswer) == "" {
		return nil, ""
	}
	if highRiskExactReplayPrompt(cachepkg.PromptText(item.Request)) {
		return nil, "high_risk_exact_replay"
	}
	if exactQualityFailed(old, item) {
		return nil, "exact_quality_failed"
	}
	pair := item
	pair.Kind = "replay_pair"
	pair.OldRequest = cachepkg.PromptText(old.Request)
	pair.OldAnswer = old.FreshAnswer
	pair.ExpectedCacheBehavior = "exact_duplicate"
	pair.SourceSampleID = old.ID
	return &pair, ""
}

func shadowTrafficPairEligible(item Case) bool {
	if item.ExcludeFromReplay {
		return false
	}
	if item.ExpectedRisk != "low" {
		return false
	}
	if item.Category == "format_sensitive" || item.Category == "time_sensitive" || item.Category == "domain_sensitive" || item.Category == "long_prompt" || item.Category == "tool_call" || item.Category == "hard_negative" {
		return false
	}
	if strings.TrimSpace(item.FreshAnswer) == "" {
		return false
	}
	finish := strings.ToLower(strings.TrimSpace(item.FinishReason))
	if finish != "" && finish != "stop" {
		return false
	}
	if len(item.Request.Tools) > 0 || hasFunctionCallExtra(item.Request.Extra) {
		return false
	}
	return true
}

func assessShadowResponseQuality(prompt string, response string, record shadowRawTrafficRecord, category string) (string, bool, string) {
	trimmed := strings.TrimSpace(response)
	status := strings.ToLower(strings.TrimSpace(record.Status))
	if status != "" && status != "success" && status != "ok" {
		return "error", true, "status_not_success"
	}
	if strings.TrimSpace(record.ErrorType) != "" {
		return "error", true, "error_type_present"
	}
	if trimmed == "" {
		return "empty", true, "empty_response"
	}
	if containsShadowTruncationMarker(trimmed) {
		return "suspected_truncated", true, "suspected_low_quality_answer"
	}
	runeLen := len([]rune(trimmed))
	if runeLen < 8 {
		return "too_short", true, "response_too_short"
	}
	finish := strings.ToLower(strings.TrimSpace(record.FinishReason))
	if finish != "" && finish != "stop" {
		return "suspected_truncated", true, "finish_reason_" + finish
	}
	if looksIncompleteShadowResponse(trimmed) {
		return "suspected_truncated", true, "incomplete_ending"
	}
	if likelyTokenResponseMismatch(prompt, category, trimmed, record.CompletionTokens) {
		return "suspected_truncated", true, "completion_tokens_response_mismatch"
	}
	return "complete", false, ""
}

func exactQualityFailed(old Case, current Case) bool {
	if strings.TrimSpace(old.ResponseQuality) != "complete" || strings.TrimSpace(current.ResponseQuality) != "complete" {
		return true
	}
	if containsShadowTruncationMarker(old.FreshAnswer) || containsShadowTruncationMarker(current.FreshAnswer) {
		return true
	}
	if exactAnswerTooShort(old) || exactAnswerTooShort(current) {
		return true
	}
	if exactCompletionMismatch(old) || exactCompletionMismatch(current) {
		return true
	}
	oldLen := len([]rune(strings.TrimSpace(old.FreshAnswer)))
	currentLen := len([]rune(strings.TrimSpace(current.FreshAnswer)))
	if oldLen >= 240 && currentLen >= 240 {
		shorter := minInt(oldLen, currentLen)
		longer := maxInt(oldLen, currentLen)
		if float64(longer)/float64(shorter) >= 2.5 {
			return true
		}
	}
	if exactLongAnswerDiverges(old.FreshAnswer, current.FreshAnswer) {
		return true
	}
	if exactEntityDrift(old.FreshAnswer, current.FreshAnswer) {
		return true
	}
	if exactNumericDrift(old.FreshAnswer, current.FreshAnswer) {
		return true
	}
	if exactClarificationMismatch(old.FreshAnswer, current.FreshAnswer) {
		return true
	}
	return false
}

func exactLongAnswerDiverges(oldAnswer string, currentAnswer string) bool {
	oldAnswer = strings.TrimSpace(oldAnswer)
	currentAnswer = strings.TrimSpace(currentAnswer)
	if len(oldAnswer) < 800 || len(currentAnswer) < 800 {
		return false
	}
	oldWords := exactWordSet(oldAnswer)
	currentWords := exactWordSet(currentAnswer)
	if len(oldWords) < 40 || len(currentWords) < 40 {
		return false
	}
	threshold := 0.22
	if len(oldAnswer) >= 2000 && len(currentAnswer) >= 2000 {
		threshold = 0.34
	}
	return jaccardSimilarity(oldWords, currentWords) < threshold
}

func exactNumericDrift(oldAnswer string, currentAnswer string) bool {
	oldNumbers := exactNumberSet(oldAnswer)
	currentNumbers := exactNumberSet(currentAnswer)
	if len(oldNumbers) < 4 || len(currentNumbers) < 4 {
		return false
	}
	return jaccardSimilarity(oldNumbers, currentNumbers) < 0.5
}

func exactWordSet(value string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range exactWordPattern.FindAllString(strings.ToLower(value), -1) {
		out[token] = struct{}{}
	}
	return out
}

func exactNumberSet(value string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range exactNumberPattern.FindAllString(value, -1) {
		out[token] = struct{}{}
	}
	return out
}

func exactEntitySet(value string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range exactEntityPattern.FindAllString(value, -1) {
		normalized := strings.ToLower(strings.TrimSpace(token))
		if normalized == "" || exactEntityStopword(normalized) {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

func exactEntityStopword(value string) bool {
	switch value {
	case "the", "and", "for", "with", "this", "that", "these", "those", "step", "steps", "note", "notes", "summary", "project", "current", "status":
		return true
	default:
		return false
	}
}

func jaccardSimilarity(left map[string]struct{}, right map[string]struct{}) float64 {
	if len(left) == 0 && len(right) == 0 {
		return 1
	}
	intersection := 0
	union := len(left)
	for key := range right {
		if _, ok := left[key]; ok {
			intersection++
			continue
		}
		union++
	}
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func exactEntityDrift(oldAnswer string, currentAnswer string) bool {
	oldEntities := exactEntitySet(oldAnswer)
	currentEntities := exactEntitySet(currentAnswer)
	if len(oldEntities) < 4 || len(currentEntities) < 4 {
		return false
	}
	return jaccardSimilarity(oldEntities, currentEntities) < 0.34
}

func exactClarificationMismatch(oldAnswer string, currentAnswer string) bool {
	oldLower := strings.ToLower(strings.TrimSpace(oldAnswer))
	currentLower := strings.ToLower(strings.TrimSpace(currentAnswer))
	if !containsAnyLiteralLower(oldLower, []string{
		"could you clarify",
		"please clarify",
		"provide more context",
		"share more context",
		"there might be some confusion",
		"to better assist you",
	}) {
		return false
	}
	if len([]rune(currentLower)) < 600 {
		return false
	}
	return containsAnyLiteralLower(currentLower, []string{
		"possible contexts",
		"possible causes",
		"possible solutions",
		"next steps",
		"step 1",
		"step 2",
		"solutions",
	})
}

func exactAnswerTooShort(item Case) bool {
	answer := strings.TrimSpace(item.FreshAnswer)
	if answer == "" {
		return true
	}
	if isShortAnswerShadowCategory(item.Category, cachepkg.PromptText(item.Request)) {
		return len([]rune(answer)) < 2
	}
	return len([]rune(answer)) < 8
}

func exactCompletionMismatch(item Case) bool {
	return likelyTokenResponseMismatch(cachepkg.PromptText(item.Request), item.Category, item.FreshAnswer, item.CompletionTokens)
}

func containsShadowTruncationMarker(value string) bool {
	return strings.Contains(value, "[TRUNCATED]")
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func looksIncompleteShadowResponse(response string) bool {
	trimmed := strings.TrimSpace(response)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	incompleteSuffixes := []string{" and", " or", " to", " the", " a", " an", " in", " of", " for", " from", " with", " by", " as", " is", " are", " it"}
	for _, suffix := range incompleteSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	if strings.HasSuffix(trimmed, "**") || strings.HasSuffix(trimmed, "`") || strings.HasSuffix(trimmed, "```") {
		return true
	}
	if strings.HasSuffix(trimmed, ":") || strings.HasSuffix(trimmed, ",") || strings.HasSuffix(trimmed, "-") || strings.HasSuffix(trimmed, "—") {
		return true
	}
	if strings.Count(trimmed, "```")%2 == 1 {
		return true
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var payload any
		if json.Unmarshal([]byte(trimmed), &payload) != nil {
			return true
		}
	}
	return false
}

func likelyTokenResponseMismatch(prompt string, category string, response string, completionTokens int) bool {
	if completionTokens < 200 {
		return false
	}
	if isShortAnswerShadowCategory(category, prompt) {
		return false
	}
	runeLen := len([]rune(strings.TrimSpace(response)))
	if runeLen < 240 {
		return true
	}
	if completionTokens >= 800 && runeLen < completionTokens/3 {
		return true
	}
	return false
}

func isShortAnswerShadowCategory(category string, prompt string) bool {
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "loose_translation" || category == "format_sensitive" {
		return true
	}
	prompt = strings.ToLower(prompt)
	return strings.Contains(prompt, "translate") || strings.Contains(prompt, "翻译")
}

func shadowDuplicateKey(item Case) string {
	prompt := normalizeShadowTrafficPrompt(cachepkg.PromptText(item.Request))
	if prompt == "" {
		return ""
	}
	return strings.Join([]string{item.Request.Model, item.Category, item.ExpectedRisk, prompt}, "\x00")
}

func normalizeShadowTrafficPrompt(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimRight(value, ".!?。！？")
	return value
}

func classifyShadowTraffic(prompt string, category string, risk string, hasToolCall bool) (string, string) {
	promptLower := strings.ToLower(prompt)
	if hasToolCall {
		return "tool_call", "tool_call"
	}
	if len([]rune(prompt)) > 512 {
		return "long_prompt", "long_prompt"
	}
	if isShadowTimeSensitive(promptLower) {
		return "time_sensitive", "time_sensitive"
	}
	if isShadowDomainSensitive(promptLower) {
		return "domain_sensitive", "high"
	}
	if isShadowFormatSensitive(promptLower) {
		return "format_sensitive", "format_sensitive"
	}
	if strings.TrimSpace(category) != "" {
		if strings.TrimSpace(risk) == "" {
			risk = defaultRiskForCategory(category)
		}
		return category, risk
	}
	raw := rawImportRecord{Prompt: prompt}
	inferredCategory, inferredRisk := inferCategoryAndRisk(raw, ImportEvalOptions{})
	return inferredCategory, inferredRisk
}

func isShadowTimeSensitive(prompt string) bool {
	return containsAnyLiteralLower(prompt, []string{"today", "latest", "current", "now", "今天", "最新", "当前", "现在"})
}

func isShadowDomainSensitive(prompt string) bool {
	return containsAnyLiteralLower(prompt, []string{
		"medical", "medicine", "dose", "dosage", "symptom", "diagnosis", "doctor", "legal", "lawsuit", "contract", "attorney", "financial", "investment", "stock", "portfolio",
		"retention rule", "retention rules", "retention period", "regulation", "regulatory", "compliance", "tax record", "tax records",
		"policy", "court deadline", "refund claim", "account permission", "security setting", "billing", "subscription",
		"医疗", "法律", "金融", "剂量", "诊断", "律师", "投资", "股票", "合规", "监管", "税务", "权限", "账单", "订阅",
	})
}

func highRiskExactReplayPrompt(prompt string) bool {
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if prompt == "" {
		return false
	}
	if containsAnyLiteralLower(prompt, []string{
		"policy", "policies",
		"compliance",
		"retention",
		"regulation", "regulatory",
		"tax",
		"security setting", "security settings",
		"billing",
		"subscription",
		"refund claim",
	}) {
		return true
	}
	if strings.Contains(prompt, "account") && containsAnyLiteralLower(prompt, []string{
		"permission", "permissions", "access", "cannot", "can't", "unable", "restriction", "restricted", "exports",
	}) {
		return true
	}
	if strings.Contains(prompt, "court") && strings.Contains(prompt, "deadline") {
		return true
	}
	return false
}

func isShadowFormatSensitive(prompt string) bool {
	return containsAnyLiteralLower(prompt, []string{
		"reply exactly", "respond exactly", "output exactly", "must be exactly", "one word", "single word", "answer with one word", "json only", "valid json", "output json", "no markdown", "yes or no", "valid / invalid", "safe / unsafe",
		"只回复", "严格回复", "必须回复", "精确输出", "只用一个词", "只回答一个词", "只输出 json", "不要 markdown", "不要解释",
	})
}

func redactShadowTrafficText(value string) redactionResult {
	redacted := value
	patterns := []struct {
		pattern     *regexp.Regexp
		replacement string
	}{
		{authorizationPattern, "Authorization: [REDACTED]"},
		{bearerPattern, "Bearer [REDACTED]"},
		{apiKeyPattern, "api_key=[REDACTED]"},
		{arkKeyPattern, "[REDACTED_API_KEY]"},
		{secretMarkerPattern, "[REDACTED_SECRET]"},
		{emailPattern, "[REDACTED_EMAIL]"},
		{urlPattern, "[REDACTED_URL]"},
		{phonePattern, "[REDACTED_PHONE]"},
		{longNumberPattern, "[REDACTED_NUMBER]"},
	}
	for _, entry := range patterns {
		redacted = entry.pattern.ReplaceAllString(redacted, entry.replacement)
	}
	if len([]rune(redacted)) > 4000 {
		runes := []rune(redacted)
		redacted = string(runes[:4000]) + " [TRUNCATED]"
	}
	return redactionResult{Value: redacted, Changed: redacted != value}
}

func hasFunctionCallExtra(extra map[string]any) bool {
	if len(extra) == 0 {
		return false
	}
	for _, key := range []string{"function_call", "tool_choice"} {
		if value, ok := extra[key]; ok && value != nil {
			return true
		}
	}
	return false
}

func parseIntDefault(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		return parseIntDefault(typed, 0)
	default:
		return 0
	}
}
