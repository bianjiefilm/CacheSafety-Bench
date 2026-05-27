package safety

import (
	"regexp"
	"sort"
	"strings"
)

var (
	moneyRe    = regexp.MustCompile(`(?i)(?:\$\s*\d+(?:\.\d+)?|\b(?:usd|rmb|eur)\s*\d+(?:\.\d+)?\b|\b\d+(?:\.\d+)?\s*(?:usd|rmb|eur|元)\b)`)
	durationRe = regexp.MustCompile(`(?i)\b\d+(?:\.\d+)?\s*(?:days?|months?|hours?|weeks?)\b|\d+(?:\.\d+)?\s*(?:天|个月|月|小时|周)`)
	versionRe  = regexp.MustCompile(`(?i)\b(?:python\s*)?v?\d+\.\d+(?:\.\d+)?\b`)
	numberRe   = regexp.MustCompile(`(?i)\b\d+(?:\.\d+)?%?\b`)
)

func DetectConstraintMismatch(oldPrompt string, newPrompt string) string {
	oldPrompt = strings.TrimSpace(oldPrompt)
	newPrompt = strings.TrimSpace(newPrompt)
	if oldPrompt == "" || newPrompt == "" {
		return ""
	}
	if polarityMismatch(oldPrompt, newPrompt) {
		return "negation"
	}
	if setMismatch(extractRewriteStyleIntents(oldPrompt), extractRewriteStyleIntents(newPrompt)) {
		return "style_intent"
	}
	if setMismatch(extractMoney(oldPrompt), extractMoney(newPrompt)) {
		return "money"
	}
	if setMismatch(extractDuration(oldPrompt), extractDuration(newPrompt)) {
		return "duration"
	}
	if setMismatch(extractVersions(oldPrompt), extractVersions(newPrompt)) {
		return "version"
	}
	if setMismatch(extractEntities(oldPrompt), extractEntities(newPrompt)) {
		return "entity"
	}
	if setMismatch(extractRegions(oldPrompt), extractRegions(newPrompt)) {
		return "region"
	}
	if setMismatch(extractNumbers(oldPrompt), extractNumbers(newPrompt)) {
		return "number"
	}
	return ""
}

func polarityMismatch(oldPrompt string, newPrompt string) bool {
	oldPolarity := polaritySignature(oldPrompt)
	newPolarity := polaritySignature(newPrompt)
	for concept, oldValue := range oldPolarity {
		if newValue, ok := newPolarity[concept]; ok && oldValue != newValue {
			return true
		}
	}
	return false
}

func polaritySignature(value string) map[string]int {
	lower := strings.ToLower(value)
	out := map[string]int{}
	markEnglishPolarity(out, lower)
	markChinesePolarity(out, value)
	return out
}

func markEnglishPolarity(out map[string]int, lower string) {
	pairs := []struct {
		concept  string
		positive []string
		negative []string
	}{
		{concept: "can", positive: []string{"\\bcan\\b"}, negative: []string{"\\bcannot\\b", "\\bcan\\s+not\\b", "\\bcan't\\b"}},
		{concept: "should", positive: []string{"\\bshould\\b"}, negative: []string{"\\bshould\\s+not\\b", "\\bshouldn't\\b"}},
		{concept: "may", positive: []string{"\\bmay\\b"}, negative: []string{"\\bmay\\s+not\\b"}},
		{concept: "allow", positive: []string{"\\ballow(?:s|ed)?\\b", "\\ballowed\\b"}, negative: []string{"\\bdisallow(?:s|ed)?\\b", "\\bnot\\s+allowed\\b"}},
		{concept: "enabled", positive: []string{"\\benabled\\b"}, negative: []string{"\\bdisabled\\b", "\\bnot\\s+enabled\\b"}},
		{concept: "eligible", positive: []string{"\\beligible\\b"}, negative: []string{"\\bnot\\s+eligible\\b", "\\bineligible\\b"}},
		{concept: "visible", positive: []string{"\\bvisible\\b"}, negative: []string{"\\bnot\\s+visible\\b", "\\binvisible\\b"}},
	}
	for _, pair := range pairs {
		if matchesAny(lower, pair.negative) {
			out[pair.concept] = -1
			continue
		}
		if matchesAny(lower, pair.positive) {
			out[pair.concept] = 1
		}
	}
}

func markChinesePolarity(out map[string]int, value string) {
	pairs := []struct {
		concept  string
		positive []string
		negative []string
	}{
		{concept: "has", positive: []string{"有"}, negative: []string{"没有"}},
		{concept: "can", positive: []string{"可以"}, negative: []string{"不可以", "不能"}},
		{concept: "should", positive: []string{"要"}, negative: []string{"不要"}},
		{concept: "is", positive: []string{"是"}, negative: []string{"不是"}},
	}
	for _, pair := range pairs {
		if containsAnyLiteral(value, pair.negative) {
			out[pair.concept] = -1
			continue
		}
		if containsAnyLiteral(value, pair.positive) {
			out[pair.concept] = 1
		}
	}
}

func extractMoney(value string) []string {
	return normalizedMatches(moneyRe, value)
}

func extractDuration(value string) []string {
	return normalizedMatches(durationRe, value)
}

func extractVersions(value string) []string {
	return normalizedMatches(versionRe, value)
}

func extractNumbers(value string) []string {
	matches := normalizedMatches(numberRe, value)
	if len(matches) == 0 {
		return nil
	}
	excluded := append([]string{}, extractMoney(value)...)
	excluded = append(excluded, extractDuration(value)...)
	excluded = append(excluded, extractVersions(value)...)
	if len(excluded) == 0 {
		return matches
	}
	filtered := make([]string, 0, len(matches))
	for _, match := range matches {
		if !numberContainedInAny(match, excluded) {
			filtered = append(filtered, match)
		}
	}
	return uniqueSorted(filtered)
}

func extractRewriteStyleIntents(value string) []string {
	lower := strings.ToLower(value)
	if !looksLikeRewriteTask(lower, value) {
		return nil
	}
	intents := map[string]bool{}
	stylePatterns := map[string][]string{
		"concise":        {`(?i)\bconcise\b`, `(?i)\bshorter\b`, `(?i)\bsuccinct\b`, `(?i)\bbrief\b`, "简洁", "更短"},
		"natural_spoken": {`(?i)\bnatural\b`, `(?i)\bspoken\b`, `(?i)\bconversational\b`, `(?i)\bcasual\b`, "自然", "口语", "口语化"},
		"polite":         {`(?i)\bpolite\b`, `(?i)\bcourteous\b`, `(?i)\bkind\b`, "礼貌", "客气"},
		"professional":   {`(?i)\bprofessional\b`, `(?i)\bbusiness\b`, `(?i)\bformal\b`, "商务", "正式", "专业"},
		"clear":          {`(?i)\bclear\b`, `(?i)\bclearer\b`, `(?i)\bclarity\b`, "清楚", "清晰", "明确"},
		"friendly":       {`(?i)\bfriendly\b`, `(?i)\bwarm\b`, "友好", "亲切"},
	}
	for intent, patterns := range stylePatterns {
		for _, pattern := range patterns {
			if strings.Contains(pattern, `\b`) || strings.Contains(pattern, "(?i)") {
				if regexp.MustCompile(pattern).MatchString(value) {
					intents[intent] = true
				}
				continue
			}
			if strings.Contains(lower, strings.ToLower(pattern)) {
				intents[intent] = true
			}
		}
	}
	return keys(intents)
}

func looksLikeRewriteTask(lower string, value string) bool {
	return containsAnyLiteral(lower, []string{
		"rewrite", "polish", "rephrase", "wording", "sentence", "make this", "write this",
	}) || containsAnyLiteral(value, []string{
		"改写", "润色", "文案", "句子", "表达",
	})
}

func extractEntities(value string) []string {
	lower := strings.ToLower(value)
	entities := map[string]bool{}
	entityPatterns := map[string][]string{
		"alice":      {`(?i)\bAlice\b`},
		"bob":        {`(?i)\bBob\b`},
		"company_a":  {`(?i)\bCompany\s+A\b`},
		"company_b":  {`(?i)\bCompany\s+B\b`},
		"ios":        {`(?i)\biOS\b`},
		"android":    {`(?i)\bAndroid\b`},
		"admin":      {`(?i)\badmins?\b`, `(?i)\badministrator\b`, "管理员"},
		"user":       {`(?i)\busers?\b`, "普通用户"},
		"acme":       {`(?i)\bAcme\b`},
		"globex":     {`(?i)\bGlobex\b`},
		"team_alpha": {`(?i)\bteam\s+Alpha\b`},
		"team_beta":  {`(?i)\bteam\s+Beta\b`},
		"apollo":     {`(?i)\bproject\s+Apollo\b`},
		"hermes":     {`(?i)\bproject\s+Hermes\b`},
		"seller":     {`(?i)\bseller\b`},
		"buyer":      {`(?i)\bbuyer\b`},
		"tenant_one": {`(?i)\btenant\s+One\b`},
		"tenant_two": {`(?i)\btenant\s+Two\b`},
		"owner":      {`(?i)\bowner\b`},
		"viewer":     {`(?i)\bviewer\b`},
		"staging":    {`(?i)\bstaging\b`},
		"production": {`(?i)\bproduction\b`},
		"mobile_app": {`(?i)\bmobile\s+app\b`},
		"web_app":    {`(?i)\bweb\s+app\b`},
	}
	for entity, patterns := range entityPatterns {
		for _, pattern := range patterns {
			if strings.Contains(pattern, `\b`) || strings.Contains(pattern, "(?i)") {
				if regexp.MustCompile(pattern).MatchString(value) {
					entities[entity] = true
				}
				continue
			}
			if strings.Contains(lower, strings.ToLower(pattern)) {
				entities[entity] = true
			}
		}
	}
	return keys(entities)
}

func extractRegions(value string) []string {
	lower := strings.ToLower(value)
	regions := map[string]bool{}
	regionPatterns := map[string][]string{
		"us":         {`(?i)\bUS\b`, `(?i)\bUnited States\b`, "美国"},
		"eu":         {`(?i)\bEU\b`, `(?i)\bEuropean Union\b`, "欧盟"},
		"china":      {`(?i)\bChina\b`, "中国"},
		"california": {`(?i)\bCalifornia\b`, "加州"},
		"new_york":   {`(?i)\bNew York\b`, "纽约"},
		"canada":     {`(?i)\bCanada\b`, "加拿大"},
		"australia":  {`(?i)\bAustralia\b`, "澳大利亚"},
		"apac":       {`(?i)\bAPAC\b`},
		"emea":       {`(?i)\bEMEA\b`},
	}
	for region, patterns := range regionPatterns {
		for _, pattern := range patterns {
			if strings.Contains(pattern, `\b`) || strings.Contains(pattern, "(?i)") {
				if regexp.MustCompile(pattern).MatchString(value) {
					regions[region] = true
				}
				continue
			}
			if strings.Contains(lower, strings.ToLower(pattern)) {
				regions[region] = true
			}
		}
	}
	return keys(regions)
}

func setMismatch(oldSet []string, newSet []string) bool {
	if len(oldSet) == 0 && len(newSet) == 0 {
		return false
	}
	return strings.Join(uniqueSorted(oldSet), "\x00") != strings.Join(uniqueSorted(newSet), "\x00")
}

func normalizedMatches(re *regexp.Regexp, value string) []string {
	matches := re.FindAllString(value, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		normalized := strings.ToLower(strings.Join(strings.Fields(match), ""))
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return uniqueSorted(out)
}

func matchesAny(value string, patterns []string) bool {
	for _, pattern := range patterns {
		if regexp.MustCompile(pattern).MatchString(value) {
			return true
		}
	}
	return false
}

func containsAnyLiteral(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func numberContainedInAny(number string, values []string) bool {
	for _, value := range values {
		if strings.Contains(value, number) {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func keys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
