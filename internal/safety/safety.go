package safety

import (
	"strings"
	"time"
)

type Rule string

const (
	RuleNone               Rule = ""
	RuleHasTools           Rule = "has_tools"
	RulePromptTooLong      Rule = "prompt_too_long"
	RuleTimeSensitive      Rule = "time_sensitive"
	RuleDomainSensitive    Rule = "domain_sensitive"
	RuleFormatSensitive    Rule = "format_sensitive"
	RuleConstraintMismatch Rule = "constraint_mismatch"
	RuleLowQuality         Rule = "low_quality"
	RuleRefreshNeeded      Rule = "refresh_needed"
)

type Decision struct {
	Allowed bool
	Rule    Rule
	Detail  string
}

type Config struct {
	MaxPromptChars           int
	MinTeacherScore          float64
	Now                      func() time.Time
	RefreshHitCountThreshold int
	RefreshAfterAge          time.Duration
}

type Checker struct {
	cfg Config
}

type Input struct {
	Prompt             string
	CachedPrompt       string
	HasTools           bool
	HasFunctionCall    bool
	CachedTeacherScore float64
	HitCount           int
	LastRefreshed      time.Time
}

func New(cfg Config) Checker {
	if cfg.MaxPromptChars <= 0 {
		cfg.MaxPromptChars = 512
	}
	if cfg.MinTeacherScore <= 0 {
		cfg.MinTeacherScore = 8.5
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.RefreshHitCountThreshold <= 0 {
		cfg.RefreshHitCountThreshold = 1000
	}
	if cfg.RefreshAfterAge <= 0 {
		cfg.RefreshAfterAge = 7 * 24 * time.Hour
	}
	return Checker{cfg: cfg}
}

func DefaultChecker() Checker {
	return New(Config{})
}

func (c Checker) IsZero() bool {
	return c.cfg.MaxPromptChars == 0 &&
		c.cfg.MinTeacherScore == 0 &&
		c.cfg.Now == nil &&
		c.cfg.RefreshHitCountThreshold == 0 &&
		c.cfg.RefreshAfterAge == 0
}

func (c Checker) Evaluate(input Input) Decision {
	if input.HasTools || input.HasFunctionCall {
		return refused(RuleHasTools)
	}
	if len([]rune(input.Prompt)) > c.cfg.MaxPromptChars {
		return refused(RulePromptTooLong)
	}
	if isTimeSensitive(input.Prompt) {
		return refused(RuleTimeSensitive)
	}
	if isDomainSensitive(input.Prompt) {
		return refused(RuleDomainSensitive)
	}
	if format := DetectFormatSensitivity(input.Prompt); format != "" {
		return refusedDetail(RuleFormatSensitive, "format_sensitive:"+format)
	}
	if format := DetectFormatSensitivity(input.CachedPrompt); format != "" {
		return refusedDetail(RuleFormatSensitive, "format_sensitive:cached_"+format)
	}
	if mismatch := DetectConstraintMismatch(input.CachedPrompt, input.Prompt); mismatch != "" {
		return refusedDetail(RuleConstraintMismatch, "constraint_mismatch:"+mismatch)
	}
	if input.CachedTeacherScore > 0 && input.CachedTeacherScore < c.cfg.MinTeacherScore {
		return refused(RuleLowQuality)
	}
	now := c.cfg.Now()
	if input.HitCount > c.cfg.RefreshHitCountThreshold &&
		!input.LastRefreshed.IsZero() &&
		input.LastRefreshed.Add(c.cfg.RefreshAfterAge).Before(now) {
		return refused(RuleRefreshNeeded)
	}
	return Decision{Allowed: true}
}

func refused(rule Rule) Decision {
	return Decision{Allowed: false, Rule: rule}
}

func refusedDetail(rule Rule, detail string) Decision {
	return Decision{Allowed: false, Rule: rule, Detail: detail}
}

func isTimeSensitive(prompt string) bool {
	return containsAny(prompt, []string{
		"today", "tomorrow", "yesterday", "current", "latest", "now", "weather", "stock price",
		"breaking", "recent", "this week", "this month",
		"今天", "明天", "昨天", "当前", "最新", "现在", "实时", "天气", "股价", "新闻",
	})
}

func isDomainSensitive(prompt string) bool {
	return containsAny(prompt, []string{
		"medical", "medicine", "dose", "dosage", "symptom", "diagnosis", "cough", "prescription",
		"legal", "lawsuit", "contract", "deadline", "liability", "court",
		"financial", "finance", "investment", "stock", "loan", "tax", "interest rate", "portfolio",
		"医疗", "药", "剂量", "症状", "诊断", "咳嗽", "处方",
		"法律", "诉讼", "合同", "期限", "责任", "法院",
		"金融", "投资", "股票", "贷款", "税", "利率",
	})
}

func containsAny(value string, needles []string) bool {
	lower := strings.ToLower(value)
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}
