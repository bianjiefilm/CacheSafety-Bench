package teacher

import (
	"context"
	"errors"
)

var ErrJudgeParse = errors.New("judge_parse_error")

type JudgeInput struct {
	OldRequest   string `json:"old_request"`
	CachedAnswer string `json:"cached_answer"`
	NewRequest   string `json:"new_request"`
	FreshAnswer  string `json:"fresh_answer"`
	Category     string `json:"category"`
}

type JudgeResult struct {
	CacheSafe   bool    `json:"cache_safe"`
	CachedScore float64 `json:"cached_score"`
	FreshScore  float64 `json:"fresh_score"`
	Delta       float64 `json:"delta"`
	BadHit      bool    `json:"bad_hit"`
	Reason      string  `json:"reason"`
}

type Judge interface {
	Score(ctx context.Context, input JudgeInput) (JudgeResult, error)
}

type Record struct {
	ID     string      `json:"id"`
	Input  JudgeInput  `json:"input"`
	Result JudgeResult `json:"result"`
	Error  string      `json:"error,omitempty"`
}
