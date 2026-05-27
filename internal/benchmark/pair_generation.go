package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/provider"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/safety"
)

const EnvRunPairGeneration = "RUN_PAIR_GENERATION"

type PairGenerationOptions struct {
	Mode       string
	Provider   string
	Model      string
	MaxSamples int
	Seed       int64
}

type PairGenerationSummary struct {
	Total         int            `json:"total"`
	SkippedCount  int            `json:"skipped_count"`
	ByCategory    map[string]int `json:"by_category"`
	ByRisk        map[string]int `json:"by_risk"`
	SkippedByRule map[string]int `json:"skipped_by_rule,omitempty"`
}

type completionProvider interface {
	Complete(ctx context.Context, req cachepkg.Request) (provider.Result, error)
}

type paraphrasePayload struct {
	Paraphrase         string `json:"paraphrase"`
	Risk               string `json:"risk"`
	ChangedConstraints bool   `json:"changed_constraints"`
	Notes              string `json:"notes"`
}

func GeneratePairs(ctx context.Context, cases []Case, opts PairGenerationOptions) ([]Case, PairGenerationSummary, error) {
	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	if mode == "" {
		mode = "rule_based"
	}
	if mode == "llm_paraphrase" {
		if strings.ToLower(strings.TrimSpace(opts.Provider)) != "volcengine" || os.Getenv(EnvRunPairGeneration) != "1" {
			return nil, PairGenerationSummary{}, fmt.Errorf("llm_paraphrase requires -provider volcengine and RUN_PAIR_GENERATION=1")
		}
	}
	rng := rand.New(rand.NewSource(opts.Seed))
	out := make([]Case, 0, len(cases))
	summary := PairGenerationSummary{ByCategory: map[string]int{}, ByRisk: map[string]int{}}
	var paraphraser completionProvider
	var answerProvider completionProvider
	checker := safety.DefaultChecker()
	for _, item := range cases {
		if mode == "llm_paraphrase" && eligibleForLLMParaphrase(item) && paraphraser == nil {
			cfg := provider.LoadVolcengineConfigFromEnv()
			volc, err := provider.NewVolcengineProvider(cfg)
			if err != nil {
				return nil, summary, err
			}
			paraphraser = volc
			answerProvider = volc
		}
		pair, ok, skipReason, err := generatePair(ctx, item, paraphraser, answerProvider, checker, opts, rng)
		if err != nil {
			return nil, summary, err
		}
		if !ok {
			summary.SkippedCount++
			if skipReason != "" {
				if summary.SkippedByRule == nil {
					summary.SkippedByRule = map[string]int{}
				}
				summary.SkippedByRule[skipReason]++
			}
			continue
		}
		out = append(out, pair)
		summary.ByCategory[pair.Category]++
		summary.ByRisk[pair.ExpectedRisk]++
		if opts.MaxSamples > 0 && len(out) >= opts.MaxSamples {
			break
		}
	}
	summary.Total = len(out)
	return out, summary, nil
}

func generatePair(ctx context.Context, item Case, paraphraser completionProvider, answerProvider completionProvider, checker safety.Checker, opts PairGenerationOptions, rng *rand.Rand) (Case, bool, string, error) {
	oldRequest := strings.TrimSpace(cachepkg.PromptText(item.Request))
	if oldRequest == "" {
		return Case{}, false, "empty_prompt", nil
	}
	if !eligibleForLLMParaphrase(item) && strings.EqualFold(strings.TrimSpace(opts.Mode), "llm_paraphrase") {
		return Case{}, false, "ineligible_category", nil
	}
	out := item
	out.SourceSampleID = item.ID
	out.PairGenerationMode = strings.ToLower(strings.TrimSpace(opts.Mode))
	out.OldRequest = oldRequest
	out.ID = item.ID
	newRequest := ""
	notes := ""
	switch out.PairGenerationMode {
	case "rule_based", "":
		newRequest = ruleBasedParaphrase(item, rng)
	case "llm_paraphrase":
		payload, err := llmParaphrase(ctx, paraphraser, opts.Model, item)
		if err != nil {
			return Case{}, false, "", err
		}
		if payload.ChangedConstraints {
			return Case{}, false, "changed_constraints", nil
		}
		if strings.TrimSpace(payload.Risk) != "" && !strings.EqualFold(payload.Risk, "low") {
			return Case{}, false, "non_low_risk", nil
		}
		newRequest = payload.Paraphrase
		notes = payload.Notes
	default:
		return Case{}, false, "", fmt.Errorf("unsupported pair generation mode %q", opts.Mode)
	}
	if strings.TrimSpace(newRequest) == "" {
		return Case{}, false, "empty_paraphrase", nil
	}
	if out.PairGenerationMode == "llm_paraphrase" {
		decision := checker.Evaluate(safety.Input{
			Prompt:             newRequest,
			CachedPrompt:       oldRequest,
			CachedTeacherScore: item.TeacherScore,
		})
		if !decision.Allowed {
			reason := string(decision.Rule)
			if decision.Detail != "" {
				reason = decision.Detail
			}
			return Case{}, false, reason, nil
		}
	}
	out.Request = item.Request
	out.Request.Messages = []cachepkg.Message{{Role: "user", Content: newRequest}}
	out.ParaphraseNotes = notes
	oldAnswer := firstNonEmpty(item.OldAnswer, item.FreshAnswer)
	if strings.TrimSpace(oldAnswer) == "" {
		if answerProvider != nil && strings.EqualFold(out.PairGenerationMode, "llm_paraphrase") {
			generated, err := completeOldAnswer(ctx, answerProvider, opts.Model, item.Request, oldRequest)
			if err != nil {
				return Case{}, false, "", err
			}
			oldAnswer = generated
		}
	}
	if strings.TrimSpace(oldAnswer) == "" {
		oldAnswer = deterministicAnswer(item.Category, oldRequest)
	}
	out.OldAnswer = oldAnswer
	if strings.TrimSpace(out.FreshAnswer) == "" {
		out.FreshAnswer = deterministicAnswer(item.Category, newRequest)
	}
	if out.ExpectedRisk == "" {
		out.ExpectedRisk = "medium"
	}
	if out.EstimatedUpstreamCost == 0 {
		out.EstimatedUpstreamCost = maxFloat(item.FreshCost, 1)
	}
	if out.FreshCost == 0 {
		out.FreshCost = out.EstimatedUpstreamCost
	}
	if out.CachedCost == 0 {
		out.CachedCost = 0.1
	}
	if out.ExpectedRisk == "high" || out.Category == "hard_negative" {
		out.ExpectedCacheBehavior = "expected_refusal"
		out.ExpectedRefusedReason = inferHardNegativeReason(newRequest)
	} else {
		out.ExpectedCacheBehavior = "semantic_safe"
		out.ExpectedRefusedReason = ""
		out.FreshAnswer = firstNonEmpty(item.FreshAnswer, out.OldAnswer)
	}
	return out, true, "", nil
}

func eligibleForLLMParaphrase(item Case) bool {
	if strings.EqualFold(strings.TrimSpace(item.ExpectedRisk), "high") || item.Category == "hard_negative" {
		return false
	}
	switch item.Category {
	case "general_explanation", "howto_guidance", "rewrite_polish", "loose_translation", "faq_generalization":
		return true
	default:
		return false
	}
}

func ruleBasedParaphrase(item Case, rng *rand.Rand) string {
	prompt := strings.TrimSpace(cachepkg.PromptText(item.Request))
	lower := strings.ToLower(prompt)
	switch item.Category {
	case "general_explanation":
		if matches := regexp.MustCompile(`(?i)^what is (.+?)(?: in simple terms| in everyday language| for beginners)?\?$`).FindStringSubmatch(prompt); len(matches) == 2 {
			return "Explain " + strings.TrimSpace(matches[1]) + " in simple terms."
		}
		if matches := regexp.MustCompile(`(?i)^explain (.+?)(?: for beginners| in product terms| in everyday language)?\.?$`).FindStringSubmatch(prompt); len(matches) == 2 {
			return "Describe " + strings.TrimSpace(matches[1]) + " in plain language."
		}
		return "Explain " + strings.TrimSuffix(prompt, ".?") + " in simple terms."
	case "howto_guidance":
		if matches := regexp.MustCompile(`(?i)^how do i (.+)\?$`).FindStringSubmatch(prompt); len(matches) == 2 {
			return "Give me a beginner-friendly way to " + strings.TrimSpace(matches[1]) + "."
		}
		if matches := regexp.MustCompile(`(?i)^how (?:should|can) i (.+)\?$`).FindStringSubmatch(prompt); len(matches) == 2 {
			return "What are simple steps to " + strings.TrimSpace(matches[1]) + "?"
		}
		return "What are simple steps to " + strings.TrimSuffix(prompt, "?") + "?"
	case "rewrite_polish":
		if matches := regexp.MustCompile(`(?i)^rewrite this (sentence|message|paragraph)(?: to [^:]+)?:\s*(.+)$`).FindStringSubmatch(prompt); len(matches) == 3 {
			return "Polish this " + strings.ToLower(matches[1]) + ": " + strings.TrimSpace(matches[2])
		}
		if matches := regexp.MustCompile(`(?i)^rewrite this sentence in active voice:\s*(.+)$`).FindStringSubmatch(prompt); len(matches) == 2 {
			return "Rewrite this sentence so it uses active voice: " + strings.TrimSpace(matches[1])
		}
		if strings.Contains(lower, "rewrite") {
			return strings.Replace(prompt, "Rewrite", "Polish", 1)
		}
		if strings.Contains(lower, "polish") {
			return strings.Replace(prompt, "Polish", "Improve the wording of", 1)
		}
		return "Polish this sentence: " + prompt
	case "loose_translation":
		prompt = strings.Replace(prompt, "Translate", "Render", 1)
		prompt = strings.Replace(prompt, "translate", "render", 1)
		prompt = strings.Replace(prompt, "into", "in", 1)
		return prompt
	case "faq_generalization":
		if strings.Contains(lower, "password") {
			return "What should I do to reset my password?"
		}
		if strings.Contains(lower, "subscription") {
			return "What should I do to cancel my subscription?"
		}
		if strings.Contains(lower, "export") && strings.Contains(lower, "account data") {
			return "What should I do to export my account data?"
		}
		if strings.Contains(lower, "contact support") {
			return "How can I contact support about my account?"
		}
		if strings.Contains(lower, "update") && strings.Contains(lower, "email") {
			return "What should I do to update the email on my account?"
		}
		if strings.Contains(lower, "account") {
			return "What should I do to update my account details?"
		}
		return "What should I do to " + strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(prompt), "how do i "), "?") + "?"
	case "hard_negative":
		return hardNegativeVariant(prompt, lower, rng)
	default:
		if matches := regexp.MustCompile(`(?i)^how (?:should|can|do) i (.+)\?$`).FindStringSubmatch(prompt); len(matches) == 2 {
			return "What are simple steps to " + strings.TrimSpace(matches[1]) + "?"
		}
		return prompt
	}
}

func hardNegativeVariant(prompt, lower string, rng *rand.Rand) string {
	switch {
	case strings.Contains(lower, "latest") || strings.Contains(lower, "today") || strings.Contains(lower, "current"):
		updated := strings.Replace(prompt, "latest", "current", 1)
		if updated == prompt {
			updated = strings.Replace(prompt, "today", "right now", 1)
		}
		if updated == prompt {
			updated = prompt + " right now?"
		}
		return updated
	case strings.Contains(lower, "legal"):
		return "What is the court deadline for a refund claim?"
	case strings.Contains(lower, "financial") || strings.Contains(lower, "stock"):
		return "What is the current stock outlook today?"
	case strings.Contains(lower, "medical"):
		return "What medicine should I take right now for a cough?"
	}
	options := []string{
		"Reply with exactly one word: safe",
		"Can I get a refund in 30 days instead of 7 days?",
		"Is this enabled or disabled right now?",
	}
	return options[rng.Intn(len(options))]
}

func inferHardNegativeReason(prompt string) string {
	lower := strings.ToLower(prompt)
	switch {
	case strings.Contains(lower, "latest") || strings.Contains(lower, "today") || strings.Contains(lower, "current"):
		return "time_sensitive"
	case strings.Contains(lower, "one word") || strings.Contains(lower, "exactly"):
		return "format_sensitive"
	case strings.Contains(lower, "medical") || strings.Contains(lower, "medicine"):
		return "domain_sensitive"
	case strings.Contains(lower, "legal") || strings.Contains(lower, "financial") || strings.Contains(lower, "stock"):
		return "domain_sensitive"
	default:
		return "constraint_mismatch"
	}
}

func llmParaphrase(ctx context.Context, paraphraser completionProvider, model string, item Case) (paraphrasePayload, error) {
	if paraphraser == nil {
		return paraphrasePayload{}, fmt.Errorf("paraphraser is not configured")
	}
	selectedModel := strings.TrimSpace(model)
	if selectedModel == "" {
		cfg := provider.LoadVolcengineConfigFromEnv()
		if len(cfg.Models) > 0 {
			selectedModel = cfg.Models[0]
		}
	}
	if selectedModel == "" {
		return paraphrasePayload{}, fmt.Errorf("llm_paraphrase requires a model")
	}
	temp := 0.0
	maxTokens := 196
	req := cachepkg.Request{
		Model:        selectedModel,
		SystemPrompt: "You rewrite user requests conservatively. You must preserve meaning and constraints. Return JSON only.",
		Messages: []cachepkg.Message{{Role: "user", Content: fmt.Sprintf(`Rewrite the following user request as one natural paraphrase.

Rules:
- Keep the original intent.
- Do not change facts, numbers, versions, amounts, deadlines, regions, or named entities.
- Do not add negation.
- Do not add time-sensitive terms like today, latest, current, now, 今天, 最新, 当前, 现在.
- Do not add exact-output constraints like exactly, one word, JSON only, only output, code block only.
- Do not turn it into medical, legal, or financial advice.
- Do not answer the question.

Request: %s

Return strict JSON:
{"paraphrase":"...","risk":"low","changed_constraints":false,"notes":"..."}`, cachepkg.PromptText(item.Request))}},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}
	result, err := paraphraser.Complete(ctx, req)
	if err != nil {
		return paraphrasePayload{}, err
	}
	var payload paraphrasePayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Response.Content)), &payload); err != nil {
		return paraphrasePayload{}, fmt.Errorf("parse paraphrase json: %w", err)
	}
	payload.Paraphrase = strings.TrimSpace(payload.Paraphrase)
	payload.Risk = strings.TrimSpace(payload.Risk)
	payload.Notes = strings.TrimSpace(payload.Notes)
	return payload, nil
}

func completeOldAnswer(ctx context.Context, answerProvider completionProvider, model string, req cachepkg.Request, oldRequest string) (string, error) {
	selectedModel := strings.TrimSpace(model)
	if selectedModel == "" {
		selectedModel = strings.TrimSpace(req.Model)
	}
	if selectedModel == "" {
		cfg := provider.LoadVolcengineConfigFromEnv()
		if len(cfg.Models) > 0 {
			selectedModel = cfg.Models[0]
		}
	}
	if selectedModel == "" {
		return "", fmt.Errorf("missing model for old answer generation")
	}
	generationReq := req
	generationReq.Model = selectedModel
	generationReq.Messages = []cachepkg.Message{{Role: "user", Content: oldRequest}}
	result, err := answerProvider.Complete(ctx, generationReq)
	if err != nil {
		return "", fmt.Errorf("generate old answer: %w", err)
	}
	return strings.TrimSpace(firstNonEmpty(result.Response.Content, result.Response.Text)), nil
}

func deterministicAnswer(category, prompt string) string {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	switch category {
	case "general_explanation":
		switch {
		case strings.Contains(lower, "rest api"):
			return "A REST API is a web interface that lets systems exchange data through standard HTTP requests like GET, POST, PUT, and DELETE."
		case strings.Contains(lower, "idempotency"):
			return "Idempotency means repeating the same operation has the same effect as doing it once, which helps clients retry requests safely."
		case strings.Contains(lower, "cosine similarity"):
			return "Cosine similarity measures how close two vectors point in the same direction, so it is often used to compare semantic similarity between texts."
		case strings.Contains(lower, "webhook"):
			return "A webhook is an HTTP callback that sends data to another system automatically when a specific event happens."
		case strings.Contains(lower, "prompt caching"):
			return "Prompt caching stores reusable model inputs or outputs so repeated requests can be served faster and at lower cost."
		case strings.Contains(lower, "rate limiting"):
			return "Rate limiting caps how many requests a client can send in a time window so an API stays stable and fair."
		case strings.Contains(lower, "jwt"):
			return "JWT is a compact token format that carries signed identity or authorization claims between systems."
		default:
			return "It is a basic concept explained in clear, practical terms."
		}
	case "howto_guidance":
		switch {
		case strings.Contains(lower, "meeting notes"):
			return "Capture the decisions, action items, owners, and deadlines, then group them under short headings so the notes are easy to scan."
		case strings.Contains(lower, "product demo"):
			return "Start with the audience goal, show the main workflow first, keep the story short, and rehearse the transitions before the demo."
		case strings.Contains(lower, "study plan"):
			return "Pick a weekly goal, break it into small sessions, assign time blocks on the calendar, and review progress at the end of the week."
		case strings.Contains(lower, "project status update"):
			return "Summarize progress, risks, blockers, and next steps in a short structure so readers can scan the update quickly."
		case strings.Contains(lower, "code review"):
			return "Review correctness first, then edge cases, tests, and readability, and leave comments that explain the impact of each issue."
		case strings.Contains(lower, "launch checklist"):
			return "Group the checklist into product, operations, QA, and communication items, then assign owners and deadlines for each step."
		case strings.Contains(lower, "contact support"):
			return "Open the support channel from the help center or account page, describe the issue clearly, and include the account details needed for follow-up."
		case strings.Contains(lower, "export account data"):
			return "Go to the account settings or data export page, request the export, and download the file when the system notifies you that it is ready."
		case strings.Contains(lower, "cancel my subscription"):
			return "Open the billing or subscription settings, choose cancel, review the effective date, and save the change."
		case strings.Contains(lower, "update the email on my account"):
			return "Go to account settings, update the email field, confirm the new address, and verify it through the confirmation message."
		case strings.Contains(lower, "reset my account password"):
			return "Use the password reset flow from the sign-in page, confirm your identity if needed, and choose a new password that you do not reuse elsewhere."
		case strings.Contains(lower, "thank-you email"):
			return "Start with specific appreciation, mention the action or support you value, and close with a short sincere note."
		default:
			return "Start with the goal, break the task into a few simple steps, and verify the result at the end."
		}
	case "rewrite_polish":
		return rewriteStyleAnswer(prompt)
	case "loose_translation":
		return translateStyleAnswer(prompt)
	case "faq_generalization":
		switch {
		case strings.Contains(lower, "password"):
			return "Use the password reset option on the sign-in page and follow the verification steps to choose a new password."
		case strings.Contains(lower, "subscription"):
			return "Open your subscription settings, choose cancel, and confirm the effective date before saving the change."
		case strings.Contains(lower, "account data"):
			return "Request the export from the account settings page and download the file after the system finishes preparing it."
		case strings.Contains(lower, "contact support"):
			return "Open the help or support page, choose the account issue category, and send the details your support team needs to investigate."
		case strings.Contains(lower, "email"):
			return "Open the account profile, change the email address, and confirm the update through the verification message."
		default:
			return "Use the relevant account settings flow and confirm the change after the system verifies your request."
		}
	case "hard_negative":
		return "A fresh answer is required for this request."
	default:
		switch {
		case strings.HasPrefix(lower, "how "):
			return deterministicAnswer("howto_guidance", prompt)
		case strings.HasPrefix(lower, "what is") || strings.HasPrefix(lower, "explain "):
			return deterministicAnswer("general_explanation", prompt)
		case strings.Contains(lower, "translate"):
			return deterministicAnswer("loose_translation", prompt)
		case strings.Contains(lower, "rewrite") || strings.Contains(lower, "polish"):
			return deterministicAnswer("rewrite_polish", prompt)
		}
		if strings.TrimSpace(prompt) == "" {
			return "A fresh answer is required."
		}
		return "Here is a direct answer to the request."
	}
}

func rewriteStyleAnswer(prompt string) string {
	lower := strings.ToLower(prompt)
	switch {
	case strings.Contains(lower, "please send the update soon"):
		return "Please send the update when you can."
	case strings.Contains(lower, "more friendly"):
		return "Hi there, just checking in. I would appreciate your help when you have a moment."
	case strings.Contains(lower, "active voice") && strings.Contains(lower, "the report was reviewed by the team"):
		return "The team reviewed the report."
	case strings.Contains(lower, "key takeaways"):
		return "- Main point\n- Supporting detail\n- Next step"
	case strings.Contains(lower, "more concise"):
		return "Here is a shorter, clearer version of the paragraph."
	case strings.Contains(lower, "more direct"):
		return "Here is a more direct version of the note."
	default:
		return "Here is a clearer and more polished version of the text."
	}
}

func translateStyleAnswer(prompt string) string {
	switch extractQuotedOrTrailingText(prompt) {
	case "Your order has shipped.":
		return "您的订单已发货。"
	case "请稍后再试。":
		return "Please try again later."
	case "The document is ready for review.":
		return "文档已准备好供审阅。"
	case "我们会尽快回复你。":
		return "We will reply to you as soon as possible."
	case "Please confirm your email address.":
		return "请确认您的电子邮箱地址。"
	case "会议已移至主会议室。":
		return "The meeting has been moved to the main conference room."
	case "The settings were updated successfully.":
		return "设置已成功更新。"
	default:
		return "This translation should be rendered naturally in the target language."
	}
}

func extractQuotedOrTrailingText(prompt string) string {
	if idx := strings.Index(prompt, ":"); idx >= 0 && idx+1 < len(prompt) {
		return strings.TrimSpace(prompt[idx+1:])
	}
	return strings.TrimSpace(prompt)
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
