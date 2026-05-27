package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

type BuildGoldenDatasetOptions struct {
	CombinedEvalPath string
	RegressionGlobs  []string
	OutputPath       string
	ReportPath       string
	Dedupe           bool
}

type GoldenDatasetReport struct {
	Total                        int            `json:"total"`
	ByCategory                   map[string]int `json:"by_category"`
	ByTrapType                   map[string]int `json:"by_trap_type"`
	ByExpectedBehavior           map[string]int `json:"by_expected_behavior"`
	ByExpectedRisk               map[string]int `json:"by_expected_risk"`
	BySourceKind                 map[string]int `json:"by_source_kind"`
	GuardExpectedCount           int            `json:"guard_expected_count"`
	MissingGuardExpectationCount int            `json:"missing_guard_expectation_count"`
	HardTrapCount                int            `json:"hard_trap_count"`
	SafePairCount                int            `json:"safe_pair_count"`
	RegressionCount              int            `json:"regression_count"`
	DuplicateRemoved             int            `json:"duplicate_removed"`
	GeneratedHardTrapCount       int            `json:"generated_hard_trap_count"`
}

func BuildGoldenDataset(opts BuildGoldenDatasetOptions) ([]Case, GoldenDatasetReport, error) {
	if strings.TrimSpace(opts.CombinedEvalPath) == "" {
		return nil, GoldenDatasetReport{}, fmt.Errorf("combined eval path is required")
	}
	if strings.TrimSpace(opts.OutputPath) == "" {
		return nil, GoldenDatasetReport{}, fmt.Errorf("output path is required")
	}
	report := newGoldenReport()
	var merged []Case
	index := map[string]int{}
	add := func(item Case) {
		item = normalizeGoldenCase(item)
		if opts.Dedupe {
			key := combinedDedupeKey(item)
			if pos, ok := index[key]; ok {
				if preferCombinedCandidate(item, merged[pos]) {
					merged[pos] = item
				}
				report.DuplicateRemoved++
				return
			}
			index[key] = len(merged)
		}
		merged = append(merged, item)
	}

	combined, err := LoadJSONL(opts.CombinedEvalPath)
	if err != nil {
		return nil, report, err
	}
	for _, item := range combined {
		if item.SourceFile == "" {
			item.SourceFile = filepath.Base(opts.CombinedEvalPath)
		}
		if item.SourceKind == "" {
			item.SourceKind = "combined_eval_v2"
		}
		add(item)
	}

	regressionPaths, err := expandSourcePaths(opts.RegressionGlobs)
	if err != nil {
		return nil, report, err
	}
	for _, path := range regressionPaths {
		if strings.HasSuffix(path, ".json") {
			item, err := loadRegressionJSON(path)
			if err != nil {
				return nil, report, err
			}
			item.SourceFile = filepath.Base(path)
			item.SourceKind = "regression"
			add(item)
		}
	}

	traps := HardTrapSamplesV1()
	report.GeneratedHardTrapCount = len(traps)
	for _, item := range traps {
		add(item)
	}

	sort.SliceStable(merged, func(i, j int) bool { return merged[i].ID < merged[j].ID })
	for _, item := range merged {
		updateGoldenReport(&report, item)
	}
	return merged, report, nil
}

func WriteGoldenDatasetReport(path string, report GoldenDatasetReport) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func newGoldenReport() GoldenDatasetReport {
	return GoldenDatasetReport{
		ByCategory:         map[string]int{},
		ByTrapType:         map[string]int{},
		ByExpectedBehavior: map[string]int{},
		ByExpectedRisk:     map[string]int{},
		BySourceKind:       map[string]int{},
	}
}

func normalizeGoldenCase(item Case) Case {
	if item.SourceSampleID == "" {
		item.SourceSampleID = item.ID
	}
	if item.SourceKind == "" {
		item.SourceKind = "combined_eval_v2"
	}
	if item.ExpectedCacheBehavior == "refusal" {
		item.ExpectedCacheBehavior = "expected_refusal"
	}
	if isGoldenTrap(item) {
		if item.TrapType == "" {
			item.TrapType = inferTrapType(item)
		}
		if item.GuardExpected == "" {
			item.GuardExpected = firstNonEmpty(item.ExpectedRefusedReason, item.TrapType)
		}
	} else {
		if strings.TrimSpace(item.OldRequest) == "" {
			item.OldRequest = cachepkg.PromptText(item.Request)
		}
		if strings.TrimSpace(item.OldAnswer) == "" {
			item.OldAnswer = item.FreshAnswer
		}
		if strings.TrimSpace(item.ExpectedCacheBehavior) == "" && item.ExpectedRisk == "low" {
			item.ExpectedCacheBehavior = "semantic_safe"
		}
	}
	return item
}

func updateGoldenReport(report *GoldenDatasetReport, item Case) {
	report.Total++
	report.ByCategory[defaultString(item.Category, "uncategorized")]++
	report.ByExpectedBehavior[defaultString(item.ExpectedCacheBehavior, "unspecified")]++
	report.ByExpectedRisk[defaultString(item.ExpectedRisk, "unspecified")]++
	report.BySourceKind[defaultString(item.SourceKind, "unknown")]++
	if item.TrapType != "" {
		report.ByTrapType[item.TrapType]++
	}
	if item.GuardExpected != "" {
		report.GuardExpectedCount++
	}
	if isGoldenTrap(item) {
		report.HardTrapCount++
		if item.GuardExpected == "" {
			report.MissingGuardExpectationCount++
		}
	} else if item.ExpectedCacheBehavior == "semantic_safe" || item.ExpectedRisk == "low" {
		report.SafePairCount++
	}
	if item.SourceKind == "regression" {
		report.RegressionCount++
	}
}

func isGoldenTrap(item Case) bool {
	return item.TrapType != "" ||
		item.Category == "hard_negative" ||
		item.Category == "hard_trap" ||
		item.ExpectedRisk == "guard_refuse" ||
		item.ExpectedCacheBehavior == "expected_refusal"
}

func inferTrapType(item Case) string {
	value := strings.ToLower(strings.Join([]string{
		item.ID,
		item.ExpectedRefusedReason,
		item.OldRequest,
		cachepkg.PromptText(item.Request),
	}, " "))
	switch {
	case strings.Contains(value, "negation") || strings.Contains(value, "cannot") || strings.Contains(value, "should not") || strings.Contains(value, "disabled") || strings.Contains(value, "不可以") || strings.Contains(value, "不是"):
		return "negation_trap"
	case strings.Contains(value, "money") || strings.Contains(value, "$") || strings.Contains(value, "usd") || strings.Contains(value, "eur") || strings.Contains(value, "元"):
		return "money_trap"
	case strings.Contains(value, "duration") || strings.Contains(value, "deadline") || strings.Contains(value, "days") || strings.Contains(value, "hours") || strings.Contains(value, "months") || strings.Contains(value, "天"):
		return "duration_deadline_trap"
	case strings.Contains(value, "region") || strings.Contains(value, "jurisdiction") || strings.Contains(value, " us ") || strings.Contains(value, " eu ") || strings.Contains(value, "china") || strings.Contains(value, "california") || strings.Contains(value, "new york"):
		return "region_jurisdiction_trap"
	case strings.Contains(value, "format") || strings.Contains(value, "exact") || strings.Contains(value, "json") || strings.Contains(value, "one word") || strings.Contains(value, "valid/invalid"):
		return "format_trap"
	case strings.Contains(value, "domain") || strings.Contains(value, "medical") || strings.Contains(value, "legal") || strings.Contains(value, "financial") || strings.Contains(value, "medicine") || strings.Contains(value, "stock"):
		return "domain_sensitive_trap"
	case strings.Contains(value, "time") || strings.Contains(value, "latest") || strings.Contains(value, "today") || strings.Contains(value, "current") || strings.Contains(value, "now"):
		return "time_sensitive_trap"
	case strings.Contains(value, "python") || strings.Contains(value, "version") || strings.Contains(value, "%"):
		return "number_trap"
	}
	return "constraint_trap"
}

func HardTrapSamplesV1() []Case {
	var out []Case
	addPairs := func(trapType string, guard string, pairs [][4]string) {
		for i, pair := range pairs {
			out = append(out, goldenTrapCase(fmt.Sprintf("golden-%s-%03d", strings.ReplaceAll(trapType, "_", "-"), i+1), trapType, guard, pair[0], pair[1], pair[2], pair[3]))
		}
	}
	addPairs("negation_trap", "constraint_mismatch:negation", [][4]string{
		{"Users can export invoices.", "Users can export invoices.", "Users cannot export invoices.", "Users cannot export invoices."},
		{"Admins should enable auto-renewal.", "Admins should enable auto-renewal.", "Admins should not enable auto-renewal.", "Admins should not enable auto-renewal."},
		{"The feature is enabled for guests.", "The feature is enabled.", "The feature is disabled for guests.", "The feature is disabled."},
		{"Support agents are allowed to reset passwords.", "Agents are allowed to reset passwords.", "Support agents are disallowed from resetting passwords.", "Agents are not allowed to reset passwords."},
		{"This account can receive exports.", "This account can receive exports.", "This account cannot receive exports.", "This account cannot receive exports."},
		{"可以为用户开启导出功能。", "可以开启导出功能。", "不可以为用户开启导出功能。", "不可以开启导出功能。"},
		{"这个配置是安全的。", "这个配置是安全的。", "这个配置不是安全的。", "这个配置不是安全的。"},
		{"用户有权限查看账单。", "用户有权限查看账单。", "用户没有权限查看账单。", "用户没有权限查看账单。"},
		{"The rollout should proceed.", "The rollout should proceed.", "The rollout should not proceed.", "The rollout should not proceed."},
		{"The app may store this preference.", "The app may store this preference.", "The app may not store this preference.", "The app may not store this preference."},
		{"The API allows public downloads.", "The API allows public downloads.", "The API disallows public downloads.", "The API disallows public downloads."},
		{"The setting is visible to users.", "The setting is visible to users.", "The setting is not visible to users.", "The setting is not visible to users."},
	})
	addPairs("number_trap", "constraint_mismatch:number", [][4]string{
		{"The plan supports 100 API calls.", "The plan supports 100 API calls.", "The plan supports 1000 API calls.", "The plan supports 1000 API calls."},
		{"Refunds are available for 7 users.", "Refunds are available for 7 users.", "Refunds are available for 30 users.", "Refunds are available for 30 users."},
		{"The usage alert fires at 5%.", "The usage alert fires at 5%.", "The usage alert fires at 15%.", "The usage alert fires at 15%."},
		{"The workspace allows 1 user.", "The workspace allows 1 user.", "The workspace allows 10 users.", "The workspace allows 10 users."},
		{"Python 3.9 setup steps.", "These steps are for Python 3.9.", "Python 3.12 setup steps.", "These steps are for Python 3.12."},
		{"API version 1.2 migration notes.", "These notes apply to API version 1.2.", "API version 2.1 migration notes.", "These notes apply to API version 2.1."},
		{"The retry count is 3.", "The retry count is 3.", "The retry count is 8.", "The retry count is 8."},
		{"The batch size is 50 records.", "The batch size is 50 records.", "The batch size is 500 records.", "The batch size is 500 records."},
		{"The limit is 2 devices.", "The limit is 2 devices.", "The limit is 20 devices.", "The limit is 20 devices."},
		{"The threshold is 0.75.", "The threshold is 0.75.", "The threshold is 0.95.", "The threshold is 0.95."},
		{"需要保留 7 条记录。", "需要保留7条记录。", "需要保留 30 条记录。", "需要保留30条记录。"},
		{"最多允许 1 个管理员。", "最多允许1个管理员。", "最多允许 10 个管理员。", "最多允许10个管理员。"},
	})
	addPairs("money_trap", "constraint_mismatch:money", [][4]string{
		{"The refund cap is $100.", "The refund cap is $100.", "The refund cap is $1000.", "The refund cap is $1000."},
		{"The credit amount is USD 25.", "The credit amount is USD 25.", "The credit amount is USD 250.", "The credit amount is USD 250."},
		{"The fee is 100 元.", "The fee is 100 元.", "The fee is 1000 元.", "The fee is 1000 元."},
		{"The budget is EUR 50.", "The budget is EUR 50.", "The budget is EUR 500.", "The budget is EUR 500."},
		{"The monthly charge is $20.", "The monthly charge is $20.", "The monthly charge is $200.", "The monthly charge is $200."},
		{"The minimum balance is RMB 100.", "The minimum balance is RMB 100.", "The minimum balance is RMB 1000.", "The minimum balance is RMB 1000."},
		{"The invoice total is $75.", "The invoice total is $75.", "The invoice total is $750.", "The invoice total is $750."},
		{"The grant is 100美元.", "The grant is 100美元.", "The grant is 1000美元.", "The grant is 1000美元."},
		{"The payout is 300 RMB.", "The payout is 300 RMB.", "The payout is 3000 RMB.", "The payout is 3000 RMB."},
		{"The discount cap is $15.", "The discount cap is $15.", "The discount cap is $150.", "The discount cap is $150."},
	})
	addPairs("duration_deadline_trap", "constraint_mismatch:duration", [][4]string{
		{"Refund requests close after 7 days.", "Refund requests close after 7 days.", "Refund requests close after 30 days.", "Refund requests close after 30 days."},
		{"Exports expire after 24 hours.", "Exports expire after 24 hours.", "Exports expire after 72 hours.", "Exports expire after 72 hours."},
		{"Trial access lasts 3 months.", "Trial access lasts 3 months.", "Trial access lasts 6 months.", "Trial access lasts 6 months."},
		{"Password reset links last 15 minutes.", "Reset links last 15 minutes.", "Password reset links last 60 minutes.", "Reset links last 60 minutes."},
		{"The review deadline is 2 weeks.", "The review deadline is 2 weeks.", "The review deadline is 4 weeks.", "The review deadline is 4 weeks."},
		{"Logs are retained for 90 days.", "Logs are retained for 90 days.", "Logs are retained for 180 days.", "Logs are retained for 180 days."},
		{"Invoices can be edited within 5 days.", "Invoices can be edited within 5 days.", "Invoices can be edited within 45 days.", "Invoices can be edited within 45 days."},
		{"退款申请期限是7天。", "退款申请期限是7天。", "退款申请期限是30天。", "退款申请期限是30天。"},
		{"导出链接保留24小时。", "导出链接保留24小时。", "导出链接保留72小时。", "导出链接保留72小时。"},
		{"The SLA response window is 4 hours.", "The SLA response window is 4 hours.", "The SLA response window is 12 hours.", "The SLA response window is 12 hours."},
	})
	addPairs("entity_trap", "constraint_mismatch:entity", [][4]string{
		{"Summarize Alice's account status.", "Alice's account is active.", "Summarize Bob's account status.", "Bob's account status is requested."},
		{"Explain Company A's onboarding steps.", "Company A should follow the onboarding steps.", "Explain Company B's onboarding steps.", "Company B should follow the onboarding steps."},
		{"Describe iOS notification settings.", "This describes iOS notification settings.", "Describe Android notification settings.", "This describes Android notification settings."},
		{"Explain admin permissions.", "Admins have elevated permissions.", "Explain user permissions.", "Users have standard permissions."},
		{"Summarize the Acme export request.", "This summarizes Acme's export request.", "Summarize the Globex export request.", "This summarizes Globex's export request."},
		{"Describe team Alpha's rollout.", "This describes team Alpha's rollout.", "Describe team Beta's rollout.", "This describes team Beta's rollout."},
		{"Explain project Apollo status.", "This explains project Apollo status.", "Explain project Hermes status.", "This explains project Hermes status."},
		{"Describe the seller workflow.", "This describes the seller workflow.", "Describe the buyer workflow.", "This describes the buyer workflow."},
		{"说明 Alice 的账号状态。", "Alice 的账号是正常的。", "说明 Bob 的账号状态。", "Bob 的账号状态需要单独说明。"},
		{"解释管理员视图。", "这是管理员视图。", "解释普通用户视图。", "这是普通用户视图。"},
		{"Summarize tenant One's billing issue.", "This summarizes tenant One's billing issue.", "Summarize tenant Two's billing issue.", "This summarizes tenant Two's billing issue."},
		{"Describe the owner role.", "This describes the owner role.", "Describe the viewer role.", "This describes the viewer role."},
		{"Explain the staging environment settings.", "This explains staging settings.", "Explain the production environment settings.", "This explains production settings."},
		{"Describe the mobile app flow.", "This describes the mobile app flow.", "Describe the web app flow.", "This describes the web app flow."},
	})
	addPairs("region_jurisdiction_trap", "constraint_mismatch:region", [][4]string{
		{"Explain export rules for US customers.", "These are US export rules.", "Explain export rules for EU customers.", "These are EU export rules."},
		{"Describe support availability in China.", "This describes China support availability.", "Describe support availability in the US.", "This describes US support availability."},
		{"Explain retention rules for California.", "These are California retention rules.", "Explain retention rules for New York.", "These are New York retention rules."},
		{"Describe billing rules for the EU.", "These are EU billing rules.", "Describe billing rules for China.", "These are China billing rules."},
		{"Explain privacy settings for US accounts.", "These are US privacy settings.", "Explain privacy settings for EU accounts.", "These are EU privacy settings."},
		{"说明中国账号的数据导出规则。", "这是中国账号的数据导出规则。", "说明美国账号的数据导出规则。", "这是美国账号的数据导出规则。"},
		{"Explain tax handling in California.", "This describes California tax handling.", "Explain tax handling in New York.", "This describes New York tax handling."},
		{"Describe EU consent handling.", "This describes EU consent handling.", "Describe US consent handling.", "This describes US consent handling."},
		{"Explain Canada account deletion rules.", "These are Canada deletion rules.", "Explain Australia account deletion rules.", "These are Australia deletion rules."},
		{"Describe APAC support hours.", "This describes APAC support hours.", "Describe EMEA support hours.", "This describes EMEA support hours."},
	})
	addPairs("format_trap", "format_sensitive", [][4]string{
		{"Describe whether this request is safe.", "The request appears safe.", "Reply exactly: safe", "safe"},
		{"Explain the validation result.", "The result is valid because inputs are complete.", "Answer valid/invalid for the validation result.", "valid"},
		{"Describe the status.", "The service status is healthy.", "Return JSON only for the status.", "{\"status\":\"ok\"}"},
		{"Explain whether the check passed.", "The check passed because all criteria succeeded.", "Answer yes or no: did the check pass?", "yes"},
		{"Summarize the classification.", "The classification is safe.", "Answer safe/unsafe only.", "safe"},
		{"Explain the next action.", "The next action is to retry the request.", "Answer with one word for the next action.", "retry"},
		{"描述这个请求是否安全。", "这个请求是安全的。", "只回答一个词：安全", "安全"},
		{"说明状态。", "状态正常。", "只输出 JSON。", "{\"status\":\"ok\"}"},
		{"Explain if output is acceptable.", "The output is acceptable.", "Answer pass/fail only.", "pass"},
		{"Describe the label.", "The label should be valid.", "Output exactly: valid", "valid"},
		{"Explain the decision.", "The decision is allowed.", "Return true or false.", "true"},
		{"Describe the result.", "The result is successful.", "No markdown. CSV format only.", "status,result\nok,success"},
	})
	return out
}

func goldenTrapCase(id string, trapType string, guard string, oldPrompt string, oldAnswer string, newPrompt string, freshAnswer string) Case {
	return Case{
		ID:                    id,
		Kind:                  "golden_hard_trap",
		Category:              "hard_trap",
		ExpectedRisk:          "guard_refuse",
		Request:               goldenRequest(newPrompt),
		FreshAnswer:           freshAnswer,
		TeacherScore:          10,
		FreshCost:             1,
		CachedCost:            0.1,
		EstimatedUpstreamCost: 1,
		OldRequest:            oldPrompt,
		OldAnswer:             oldAnswer,
		ExpectedCacheBehavior: "expected_refusal",
		ExpectedRefusedReason: guard,
		PairGenerationMode:    "template_v1",
		SourceSampleID:        id,
		SourceFile:            "golden_hard_traps_v1",
		SourceKind:            "golden_trap",
		TrapType:              trapType,
		GuardExpected:         guard,
	}
}

func goldenRequest(prompt string) cachepkg.Request {
	temp := 0.0
	maxTokens := 96
	return cachepkg.Request{
		Model:       "benchmark-provider-model",
		Messages:    []cachepkg.Message{{Role: "user", Content: prompt}},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}
}
