package teacher

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ParseJudgeJSON(value string) (JudgeResult, error) {
	cleaned := strings.TrimSpace(value)
	if strings.HasPrefix(cleaned, "```") {
		cleaned = strings.TrimPrefix(cleaned, "```json")
		cleaned = strings.TrimPrefix(cleaned, "```")
		cleaned = strings.TrimSuffix(cleaned, "```")
		cleaned = strings.TrimSpace(cleaned)
	}
	var result JudgeResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return JudgeResult{Reason: ErrJudgeParse.Error()}, fmt.Errorf("%w: %v", ErrJudgeParse, err)
	}
	if result.Reason == "" {
		result.Reason = "judge returned no reason"
	}
	return result, nil
}
