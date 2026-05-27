package safety

import (
	"regexp"
	"strings"
)

var (
	exactPhrasePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(reply|respond|output)\s+(?:with\s+)?exactly\b`),
		regexp.MustCompile(`(?i)\bmust\s+be\s+exactly\b`),
	}
	oneWordPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(one|single)\s+word\b`),
		regexp.MustCompile(`(?i)\bone\s+sentence\b`),
		regexp.MustCompile(`(?i)\banswer\s+with\s+one\s+word\b`),
	}
	enumPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\banswer\s+yes\s+or\s+no\b`),
		regexp.MustCompile(`(?i)\breturn\s+true\s+or\s+false\b`),
		regexp.MustCompile(`(?i)\bvalid\s*/\s*invalid\b`),
		regexp.MustCompile(`(?i)\bsafe\s*/\s*unsafe\b`),
		regexp.MustCompile(`(?i)\bpass\s*/\s*fail\b`),
		regexp.MustCompile(`(?i)\b[A-D]\s*/\s*[A-D](?:\s*/\s*[A-D]){0,2}\b`),
	}
	structuredOutputPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bjson\s+only\b`),
		regexp.MustCompile(`(?i)\bvalid\s+json\b`),
		regexp.MustCompile(`(?i)\boutput\s+json\b`),
		regexp.MustCompile(`(?i)\bno\s+markdown\b`),
		regexp.MustCompile(`(?i)\bcode\s+block\s+only\b`),
		regexp.MustCompile(`(?i)\bcsv\s+format\b`),
		regexp.MustCompile(`(?i)\byaml\s+format\b`),
	}
)

func DetectFormatSensitivity(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if matchesRegexp(prompt, exactPhrasePatterns) || containsAnyLiteral(prompt, []string{"只回复", "严格回复", "必须回复", "精确输出"}) {
		return "exact_phrase"
	}
	if matchesRegexp(prompt, oneWordPatterns) || containsAnyLiteral(prompt, []string{"只用一个词", "只回答一个词", "一句话回答"}) {
		return "one_word"
	}
	if matchesRegexp(prompt, enumPatterns) || containsAnyLiteral(prompt, []string{"输出以下之一", "只能回答", "分类为"}) {
		return "enum"
	}
	if matchesRegexp(prompt, structuredOutputPatterns) || containsAnyLiteral(prompt, []string{"只输出 JSON", "只输出JSON", "不要解释", "不要 Markdown", "不要Markdown"}) {
		return "structured_output"
	}
	return ""
}

func matchesRegexp(value string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}
