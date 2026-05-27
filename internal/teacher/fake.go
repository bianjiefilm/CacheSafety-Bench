package teacher

import (
	"context"
	"strings"
)

type FakeJudge struct {
	Results map[string]JudgeResult
	Err     error
}

func (j FakeJudge) Score(ctx context.Context, input JudgeInput) (JudgeResult, error) {
	if j.Err != nil {
		return JudgeResult{Reason: j.Err.Error()}, j.Err
	}
	if result, ok := j.Results[input.NewRequest]; ok {
		return normalize(result), nil
	}
	if result, ok := j.Results[input.Category]; ok {
		return normalize(result), nil
	}
	cacheSafe := strings.TrimSpace(input.CachedAnswer) == strings.TrimSpace(input.FreshAnswer)
	if input.Category == "semantic_paraphrase" || input.Category == "semantic-safe" {
		cacheSafe = true
	}
	result := JudgeResult{
		CacheSafe:   cacheSafe,
		CachedScore: 9,
		FreshScore:  9,
		Delta:       0,
		BadHit:      !cacheSafe,
		Reason:      "fake judge stable rule",
	}
	return result, nil
}

func normalize(result JudgeResult) JudgeResult {
	result.BadHit = !result.CacheSafe || result.BadHit
	if result.Reason == "" {
		result.Reason = "fake judge fixture"
	}
	return result
}
