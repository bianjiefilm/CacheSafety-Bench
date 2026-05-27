package safety

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSemanticSafetyRefusesSensitiveCasesInOrder(t *testing.T) {
	checker := DefaultChecker()

	cases := []struct {
		name  string
		input Input
		rule  Rule
	}{
		{name: "tools", input: Input{Prompt: "hello", HasTools: true}, rule: RuleHasTools},
		{name: "function call", input: Input{Prompt: "hello", HasFunctionCall: true}, rule: RuleHasTools},
		{name: "too long", input: Input{Prompt: strings.Repeat("a", 513)}, rule: RulePromptTooLong},
		{name: "time sensitive", input: Input{Prompt: "What is the latest AI news today?"}, rule: RuleTimeSensitive},
		{name: "medical domain", input: Input{Prompt: "What medicine should I take for a cough?"}, rule: RuleDomainSensitive},
		{name: "legal domain", input: Input{Prompt: "What is the legal deadline for this contract?"}, rule: RuleDomainSensitive},
		{name: "financial domain", input: Input{Prompt: "Should I buy this stock for my portfolio?"}, rule: RuleDomainSensitive},
		{name: "format sensitive", input: Input{Prompt: "Reply with exactly: safe"}, rule: RuleFormatSensitive},
		{name: "cached format sensitive", input: Input{CachedPrompt: "Answer with one word: safe", Prompt: "Explain semantic cache safety."}, rule: RuleFormatSensitive},
		{name: "constraint mismatch", input: Input{CachedPrompt: "Refund in 7 days.", Prompt: "Refund in 30 days."}, rule: RuleConstraintMismatch},
		{name: "low quality", input: Input{Prompt: "hello", CachedTeacherScore: 8.49}, rule: RuleLowQuality},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checker.Evaluate(tc.input)
			require.False(t, got.Allowed)
			require.Equal(t, tc.rule, got.Rule)
		})
	}
}

func TestSemanticSafetyRefusesRefreshNeeded(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	checker := New(Config{Now: func() time.Time { return now }})

	got := checker.Evaluate(Input{
		Prompt:             "Explain cache keys",
		CachedTeacherScore: 9.0,
		HitCount:           1001,
		LastRefreshed:      now.Add(-8 * 24 * time.Hour),
	})

	require.False(t, got.Allowed)
	require.Equal(t, RuleRefreshNeeded, got.Rule)
}

func TestSemanticSafetyRuleOrder(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	checker := New(Config{Now: func() time.Time { return now }})

	cases := []struct {
		name  string
		input Input
		want  Rule
	}{
		{
			name: "has tools wins over everything",
			input: Input{
				Prompt:             strings.Repeat("today medical financial legal ", 40),
				HasTools:           true,
				CachedTeacherScore: 1,
				HitCount:           2000,
				LastRefreshed:      now.Add(-8 * 24 * time.Hour),
			},
			want: RuleHasTools,
		},
		{
			name: "prompt too long wins after tools",
			input: Input{
				Prompt:             strings.Repeat("today medical financial legal ", 40),
				CachedTeacherScore: 1,
				HitCount:           2000,
				LastRefreshed:      now.Add(-8 * 24 * time.Hour),
			},
			want: RulePromptTooLong,
		},
		{
			name: "time sensitive wins before domain",
			input: Input{
				Prompt:             "latest medical financial legal update",
				CachedTeacherScore: 1,
				HitCount:           2000,
				LastRefreshed:      now.Add(-8 * 24 * time.Hour),
			},
			want: RuleTimeSensitive,
		},
		{
			name: "domain wins before low quality",
			input: Input{
				Prompt:             "medical financial legal advice",
				CachedTeacherScore: 1,
				HitCount:           2000,
				LastRefreshed:      now.Add(-8 * 24 * time.Hour),
			},
			want: RuleDomainSensitive,
		},
		{
			name: "format sensitive wins before constraint mismatch",
			input: Input{
				CachedPrompt:       "Answer yes or no: the account is eligible for automatic renewal.",
				Prompt:             "Answer yes or no: the account is not eligible for automatic renewal.",
				CachedTeacherScore: 1,
				HitCount:           2000,
				LastRefreshed:      now.Add(-8 * 24 * time.Hour),
			},
			want: RuleFormatSensitive,
		},
		{
			name: "constraint mismatch wins before low quality",
			input: Input{
				CachedPrompt:       "Refund in 7 days.",
				Prompt:             "Refund in 30 days.",
				CachedTeacherScore: 1,
				HitCount:           2000,
				LastRefreshed:      now.Add(-8 * 24 * time.Hour),
			},
			want: RuleConstraintMismatch,
		},
		{
			name: "low quality wins before refresh",
			input: Input{
				Prompt:             "Explain cache keys",
				CachedTeacherScore: 1,
				HitCount:           2000,
				LastRefreshed:      now.Add(-8 * 24 * time.Hour),
			},
			want: RuleLowQuality,
		},
		{
			name: "refresh is last",
			input: Input{
				Prompt:             "Explain cache keys",
				CachedTeacherScore: 9,
				HitCount:           2000,
				LastRefreshed:      now.Add(-8 * 24 * time.Hour),
			},
			want: RuleRefreshNeeded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checker.Evaluate(tc.input)
			require.False(t, got.Allowed)
			require.Equal(t, tc.want, got.Rule)
		})
	}
}

func TestFormatGuardDetectsSensitivePrompts(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{name: "reply exactly", prompt: "Reply exactly with: safe", want: "exact_phrase"},
		{name: "respond exactly", prompt: "Respond exactly: ok", want: "exact_phrase"},
		{name: "output exactly", prompt: "Output exactly VALID", want: "exact_phrase"},
		{name: "must be exactly", prompt: "The answer must be exactly pong", want: "exact_phrase"},
		{name: "chinese exact", prompt: "只回复：安全", want: "exact_phrase"},
		{name: "one word", prompt: "Answer with one word: safe", want: "one_word"},
		{name: "single word", prompt: "Use a single word label.", want: "one_word"},
		{name: "one sentence", prompt: "Answer in one sentence.", want: "one_word"},
		{name: "chinese one word", prompt: "只回答一个词：安全", want: "one_word"},
		{name: "yes no", prompt: "Answer yes or no: is this valid?", want: "enum"},
		{name: "true false", prompt: "Return true or false.", want: "enum"},
		{name: "valid invalid", prompt: "Classify as valid / invalid.", want: "enum"},
		{name: "safe unsafe", prompt: "Return safe / unsafe.", want: "enum"},
		{name: "pass fail", prompt: "Answer pass / fail.", want: "enum"},
		{name: "abc", prompt: "Choose A/B/C/D.", want: "enum"},
		{name: "chinese enum", prompt: "输出以下之一：safe / unsafe", want: "enum"},
		{name: "json only", prompt: "JSON only.", want: "structured_output"},
		{name: "valid json", prompt: "Return valid JSON.", want: "structured_output"},
		{name: "output json", prompt: "Output JSON with no markdown.", want: "structured_output"},
		{name: "no markdown", prompt: "No markdown, just the value.", want: "structured_output"},
		{name: "code block", prompt: "Code block only.", want: "structured_output"},
		{name: "csv", prompt: "CSV format please.", want: "structured_output"},
		{name: "yaml", prompt: "YAML format please.", want: "structured_output"},
		{name: "chinese json", prompt: "只输出 JSON，不要解释。", want: "structured_output"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, DetectFormatSensitivity(tc.prompt))
		})
	}
}

func TestFormatGuardAllowsNonFormatPrompts(t *testing.T) {
	require.Empty(t, DetectFormatSensitivity("Explain semantic cache reuse in plain language."))
	require.Empty(t, DetectFormatSensitivity("Summarize the refund policy briefly."))
}

func TestCachedFormatSensitivePromptRefusesSemantic(t *testing.T) {
	got := DefaultChecker().Evaluate(Input{
		CachedPrompt: "Answer with one word: safe",
		Prompt:       "Explain when semantic cache reuse is safe.",
	})

	require.False(t, got.Allowed)
	require.Equal(t, RuleFormatSensitive, got.Rule)
	require.Equal(t, "format_sensitive:cached_one_word", got.Detail)
}

func TestTrapGuardDetectsConstraintMismatches(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "negation", old: "The account is eligible for automatic renewal.", new: "The account is not eligible for automatic renewal.", want: "negation"},
		{name: "number", old: "Set retry count to 7.", new: "Set retry count to 30.", want: "number"},
		{name: "version", old: "Install on Python 3.9.", new: "Install on Python 3.12.", want: "version"},
		{name: "money", old: "Discount applies above $100.", new: "Discount applies above $1000.", want: "money"},
		{name: "duration", old: "Refund within 7 days.", new: "Refund within 30 days.", want: "duration"},
		{name: "region", old: "Warranty rule for US customers.", new: "Warranty rule for EU customers.", want: "region"},
		{name: "entity", old: "Explain Company A's onboarding steps.", new: "Explain Company B's onboarding steps.", want: "entity"},
		{name: "role entity", old: "Explain admin permissions.", new: "Explain user permissions.", want: "entity"},
		{name: "rewrite style intent", old: "Make this sentence more concise: We should explain the next step clearly.", new: "Rewrite this sentence in a more natural spoken style: We should explain the next step clearly.", want: "style_intent"},
		{name: "may negation", old: "The app may store this preference.", new: "The app may not store this preference.", want: "negation"},
		{name: "visible negation", old: "The setting is visible to users.", new: "The setting is not visible to users.", want: "negation"},
		{name: "chinese negation", old: "这个账户可以续费。", new: "这个账户不可以续费。", want: "negation"},
		{name: "chinese duration", old: "退款期限是7天。", new: "退款期限是30天。", want: "duration"},
		{name: "chinese region", old: "适用于中国用户。", new: "适用于美国用户。", want: "region"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, DetectConstraintMismatch(tc.old, tc.new))
		})
	}
}

func TestTrapGuardAllowsSameConstraints(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "same duration", old: "Refund within 30 days.", new: "Returns are allowed within 30 days."},
		{name: "same money", old: "Free shipping over $100.", new: "Orders above $100 get free shipping."},
		{name: "same region", old: "Warranty rule for US customers.", new: "Explain the US warranty rule."},
		{name: "same entity", old: "Explain Company A onboarding.", new: "Describe Company A onboarding."},
		{name: "same rewrite style", old: "Make this sentence sound more conversational: We should explain the next step clearly.", new: "Rewrite this sentence in a more natural spoken style: We should explain the next step clearly."},
		{name: "same chinese money with spacing", old: "The fee is 1000元.", new: "The fee is 1000 元."},
		{name: "no constraints", old: "Explain safe semantic cache reuse.", new: "Describe safe semantic cache reuse."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Empty(t, DetectConstraintMismatch(tc.old, tc.new))
		})
	}
}
