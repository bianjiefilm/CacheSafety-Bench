package teacher

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/provider"
)

const (
	EnvVolcengineJudgeModel     = "VOLCENGINE_JUDGE_MODEL"
	EnvRunTeacherIntegration    = "RUN_TEACHER_INTEGRATION"
	defaultJudgeMaxOutputTokens = 256
)

type VolcengineJudge struct {
	provider *provider.VolcengineProvider
	model    string
}

func NewVolcengineJudgeFromEnv(modelOverride string) (*VolcengineJudge, bool, error) {
	if os.Getenv(EnvRunTeacherIntegration) != "1" {
		return nil, false, nil
	}
	cfg := provider.LoadVolcengineConfigFromEnv()
	model := strings.TrimSpace(modelOverride)
	if model == "" {
		model = strings.TrimSpace(os.Getenv(EnvVolcengineJudgeModel))
	}
	if model == "" && len(cfg.Models) > 0 {
		model = cfg.Models[0]
	}
	if model == "" {
		return nil, false, fmt.Errorf("judge=volcengine requires -judge-model, VOLCENGINE_JUDGE_MODEL, or VOLCENGINE_MODEL_1")
	}
	volc, err := provider.NewVolcengineProvider(cfg)
	if err != nil {
		return nil, false, err
	}
	return &VolcengineJudge{provider: volc, model: model}, true, nil
}

func (j *VolcengineJudge) Score(ctx context.Context, input JudgeInput) (JudgeResult, error) {
	if j == nil || j.provider == nil {
		return JudgeResult{Reason: "judge_not_configured"}, fmt.Errorf("judge_not_configured")
	}
	temp := 0.0
	maxTokens := defaultJudgeMaxOutputTokens
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, err := j.provider.Complete(ctx, cachepkg.Request{
		Model:        j.model,
		SystemPrompt: judgeSystemPrompt(),
		Messages:     []cachepkg.Message{{Role: "user", Content: JudgePrompt(input)}},
		Temperature:  &temp,
		MaxTokens:    &maxTokens,
	})
	if err != nil {
		return JudgeResult{Reason: err.Error()}, err
	}
	parsed, err := ParseJudgeJSON(result.Response.Content)
	if err != nil {
		return parsed, err
	}
	return parsed, nil
}

func judgeSystemPrompt() string {
	return "You are a strict cache safety judge. Return only valid JSON. Do not include markdown. Judge semantic safety, not string equality."
}

func JudgePrompt(input JudgeInput) string {
	return fmt.Sprintf(`Judge whether cached_answer can safely answer new_request.

Do not score by literal string equality. Evaluate whether the old cached answer is safe for the new user request.

Questions:
- Can cached_answer safely answer new_request?
- Would the user feel it answers the wrong question?
- Does cached_answer violate facts, numbers, dates, times, formats, regions, user identity, quantities, legal deadlines, medical dosages, financial ratios, or other constraints in new_request?
- fresh_answer is only a reference answer. cached_answer does not need to match it word-for-word.
- fresh_answer is not ground truth. Do not mark bad solely because cached_answer uses different wording.
- If cached_answer is a synonym, paraphrase, or otherwise safely satisfies new_request, set cache_safe=true and bad_hit=false.
- If category is semantic_paraphrase, be tolerant of short synonymous answers, while still rejecting factual or constraint mismatches.
- Mark bad_hit=true only when cached_answer is unsafe, materially wrong, misleading, off-topic, or violates a constraint in new_request.

Return strict JSON only:
{
  "cache_safe": true,
  "cached_score": 8,
  "fresh_score": 9,
  "delta": -1,
  "bad_hit": false,
  "reason": "minor wording difference only"
}

old_request:
%s

cached_answer:
%s

new_request:
%s

fresh_answer:
%s

category:
%s`, input.OldRequest, input.CachedAnswer, input.NewRequest, input.FreshAnswer, input.Category)
}
