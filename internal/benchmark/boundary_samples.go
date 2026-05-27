package benchmark

import (
	"fmt"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
)

func BoundarySamplesV0() []Case {
	var out []Case
	out = append(out, thresholdBoundarySamples()...)
	out = append(out, rewriteBoundarySamples()...)
	out = append(out, faqBoundarySamples()...)
	out = append(out, multilingualBoundarySamples()...)
	out = append(out, hardNegativeBoundarySamples()...)
	return out
}

func thresholdBoundarySamples() []Case {
	items := []struct {
		topic  string
		old    string
		new    string
		answer string
	}{
		{"webhook", "Explain what a webhook does in a product integration.", "Describe the role of a webhook in an app integration.", "A webhook lets one system notify another system when an event happens."},
		{"feature flag", "Explain feature flags for a product team.", "Describe why a product team uses feature flags.", "Feature flags let teams turn behavior on or off without redeploying code."},
		{"retry policy", "Explain a retry policy for API calls.", "Describe an API retry policy in simple terms.", "A retry policy defines when a failed request should be attempted again."},
		{"rate limit", "Explain rate limits for public APIs.", "Describe why public APIs use rate limits.", "Rate limits control request volume so a service stays reliable."},
		{"idempotency", "Explain idempotency for payment requests.", "Describe why idempotency matters for payment APIs.", "Idempotency lets repeated requests produce the same effect as one request."},
		{"WAL", "Explain SQLite WAL mode for local apps.", "Describe why SQLite WAL mode helps local applications.", "WAL mode lets readers continue while writes are recorded in a separate log."},
		{"JWT", "Explain JWTs for web authentication.", "Describe how JWTs are used in web sign-in flows.", "A JWT is a signed token that carries claims between systems."},
		{"semantic cache", "Explain semantic cache reuse for low-risk prompts.", "Describe when semantic cache reuse can be appropriate.", "Semantic cache reuse can answer a new prompt with a prior answer when meaning and constraints match."},
		{"cosine", "Explain cosine similarity for text embeddings.", "Describe cosine similarity when comparing text vectors.", "Cosine similarity compares vector direction to estimate how related two texts are."},
		{"prompt cache", "Explain prompt caching in a model API.", "Describe prompt caching for repeated model requests.", "Prompt caching reuses previous work to reduce latency and cost."},
	}
	out := make([]Case, 0, 30)
	for i, item := range items {
		out = append(out,
			boundarySafeCase(fmt.Sprintf("bnd-threshold-%03d", i*3+1), "threshold_boundary", item.old, item.new, item.answer),
			boundarySafeCase(fmt.Sprintf("bnd-threshold-%03d", i*3+2), "threshold_boundary", "What is "+item.topic+" in plain language?", "Give a plain-language explanation of "+item.topic+".", item.answer),
			boundarySafeCase(fmt.Sprintf("bnd-threshold-%03d", i*3+3), "threshold_boundary", "Summarize "+item.topic+" for a non-technical teammate.", "Explain "+item.topic+" to someone outside engineering.", item.answer),
		)
	}
	return out
}

func rewriteBoundarySamples() []Case {
	sentences := []struct {
		text    string
		answers map[string]string
	}{
		{"Send me the deck when you can.", map[string]string{
			"polite":   "Could you please send me the deck when you have a chance?",
			"concise":  "Please send the deck when ready.",
			"casual":   "Send me the deck when you get a chance.",
			"business": "Please provide the deck at your earliest convenience.",
			"clear":    "Please send me the presentation deck when it is available.",
		}},
		{"We need the report before the meeting.", map[string]string{
			"polite":   "Could you please share the report before the meeting?",
			"concise":  "Please send the report before the meeting.",
			"casual":   "Please get the report to us before the meeting.",
			"business": "The report is required before the meeting begins.",
			"clear":    "Please provide the report before the meeting starts.",
		}},
		{"The current wording is hard to understand.", map[string]string{
			"polite":   "Could we make the current wording easier to understand?",
			"concise":  "The wording is unclear.",
			"casual":   "This wording is hard to follow.",
			"business": "The current wording would benefit from improved clarity.",
			"clear":    "The current wording is difficult to understand and should be clarified.",
		}},
		{"Please share the update with the team.", map[string]string{
			"polite":   "Could you please share the update with the team?",
			"concise":  "Please update the team.",
			"casual":   "Please let the team know about the update.",
			"business": "Please distribute the update to the team.",
			"clear":    "Please send the latest update to everyone on the team.",
		}},
		{"This feature is useful but confusing.", map[string]string{
			"polite":   "This feature is helpful, though it may be clearer with some refinement.",
			"concise":  "This feature is useful but unclear.",
			"casual":   "This feature helps, but it is a bit confusing.",
			"business": "This feature provides value but may require clearer guidance.",
			"clear":    "The feature is useful, but users may find it difficult to understand.",
		}},
		{"The customer did not receive the export.", map[string]string{
			"polite":   "The customer has not received the export yet.",
			"concise":  "The customer has not received the export.",
			"casual":   "The customer still has not gotten the export.",
			"business": "The customer has not yet received the requested export.",
			"clear":    "The export was expected, but the customer has not received it.",
		}},
		{"We should explain the next step clearly.", map[string]string{
			"polite":   "We should clearly explain the next step.",
			"concise":  "Explain the next step clearly.",
			"casual":   "We should make the next step easy to understand.",
			"business": "We should provide a clear explanation of the next step.",
			"clear":    "We should explain exactly what happens next.",
		}},
	}
	styles := []struct {
		name string
		old  string
		new  string
	}{
		{"polite", "Make this sentence more polite: %s", "Please rewrite this sentence in a more courteous tone: %s"},
		{"concise", "Make this sentence more concise: %s", "Rewrite this sentence so it is shorter and clearer: %s"},
		{"casual", "Make this sentence sound more conversational: %s", "Rewrite this sentence in a more natural spoken style: %s"},
		{"business", "Make this sentence sound more businesslike: %s", "Rewrite this sentence in a professional business tone: %s"},
		{"clear", "Make this sentence clearer: %s", "Improve this sentence so the meaning is easier to understand: %s"},
	}
	out := make([]Case, 0, len(sentences)*len(styles))
	id := 1
	for _, sentence := range sentences {
		for _, style := range styles {
			out = append(out, boundarySafeCase(
				fmt.Sprintf("bnd-rewrite-%03d", id),
				"rewrite_polish",
				fmt.Sprintf(style.old, sentence.text),
				fmt.Sprintf(style.new, sentence.text),
				sentence.answers[style.name],
			))
			id++
		}
	}
	return out
}

func faqBoundarySamples() []Case {
	topics := []struct {
		name   string
		answer string
	}{
		{"reset my password", "Use the password reset option, confirm your identity, and choose a new password."},
		{"update my email address", "Open account settings, add the new email address, and confirm the verification message."},
		{"export my data", "Go to data export settings, choose the export format, and start the export request."},
		{"contact support", "Use the support page or help menu to send a message to the support team."},
		{"cancel my subscription", "Open billing settings, choose the subscription, and follow the cancellation flow."},
		{"change my billing email", "Open billing settings and update the billing contact email."},
	}
	out := make([]Case, 0, 30)
	id := 1
	for _, topic := range topics {
		pairs := [][2]string{
			{"How do I " + topic.name + "?", "What should I do to " + topic.name + "?"},
			{"Where can I " + topic.name + "?", "Which account area lets me " + topic.name + "?"},
			{"Can you explain how to " + topic.name + "?", "Give me the basic steps to " + topic.name + "."},
			{"I need help to " + topic.name + ".", "How can a customer " + topic.name + "?"},
			{"What is the usual way to " + topic.name + "?", "Describe the normal process to " + topic.name + "."},
		}
		for _, pair := range pairs {
			out = append(out, boundarySafeCase(fmt.Sprintf("bnd-faq-%03d", id), "faq_generalization", pair[0], pair[1], topic.answer))
			id++
		}
	}
	return out
}

func multilingualBoundarySamples() []Case {
	items := []struct {
		category string
		old      string
		new      string
		answer   string
	}{
		{"multilingual", "Please explain semantic caching in Chinese.", "用中文解释 semantic caching 的基本意思。", "语义缓存会在新请求含义和约束一致时复用之前的回答。"},
		{"multilingual", "用简单中文解释 REST API。", "用中文解释什么是 REST API。", "REST API 让系统通过标准的网络请求进行通信。"},
		{"multilingual", "Translate this sentence into natural English: 我们会尽快处理你的请求。", "Render this Chinese sentence in natural English: 我们会尽快处理你的请求。", "We will process your request as soon as possible."},
		{"multilingual", "Translate this sentence into Chinese: The export is ready to download.", "用自然中文翻译: The export is ready to download.", "导出文件已准备好，可以下载。"},
		{"multilingual", "Explain prompt caching to a Chinese-speaking product manager in Chinese.", "用中文给产品经理解释 prompt caching。", "Prompt caching 会复用之前的模型处理结果，从而降低重复请求的成本和延迟。"},
	}
	out := make([]Case, 0, 25)
	id := 1
	for round := 0; round < 5; round++ {
		for _, item := range items {
			oldPrompt := item.old
			newPrompt := item.new
			if round%2 == 1 {
				oldPrompt, newPrompt = newPrompt, oldPrompt
			}
			out = append(out, boundarySafeCase(fmt.Sprintf("bnd-multi-%03d", id), item.category, oldPrompt, newPrompt, item.answer))
			id++
		}
	}
	return out
}

func hardNegativeBoundarySamples() []Case {
	var out []Case
	add := func(id string, oldPrompt string, oldAnswer string, newPrompt string, freshAnswer string, reason string) {
		out = append(out, boundaryRefusalCase(id, oldPrompt, oldAnswer, newPrompt, freshAnswer, reason))
	}
	durationPairs := [][4]string{
		{"Refund requests are accepted for 7 days.", "Refunds are accepted for 7 days.", "Refund requests are accepted for 30 days.", "Refunds are accepted for 30 days."},
		{"Trials expire after 14 days.", "Trials expire after 14 days.", "Trials expire after 30 days.", "Trials expire after 30 days."},
		{"Data exports are retained for 24 hours.", "Exports remain available for 24 hours.", "Data exports are retained for 72 hours.", "Exports remain available for 72 hours."},
		{"Invoices can be corrected within 3 months.", "Invoice corrections are available for 3 months.", "Invoices can be corrected within 6 months.", "Invoice corrections are available for 6 months."},
		{"The approval window is 2 weeks.", "The approval window is 2 weeks.", "The approval window is 4 weeks.", "The approval window is 4 weeks."},
		{"退款申请可在7天内提交。", "退款申请期限是7天。", "退款申请可在30天内提交。", "退款申请期限是30天。"},
	}
	for i, pair := range durationPairs {
		add(fmt.Sprintf("bnd-hard-duration-%03d", i+1), pair[0], pair[1], pair[2], pair[3], "constraint_mismatch:duration")
	}
	moneyPairs := [][4]string{
		{"The refund cap is $100.", "The refund cap is $100.", "The refund cap is $1000.", "The refund cap is $1000."},
		{"The monthly credit is USD 25.", "The monthly credit is USD 25.", "The monthly credit is USD 250.", "The monthly credit is USD 250."},
		{"The fee is 100元.", "The fee is 100元.", "The fee is 1000元.", "The fee is 1000元."},
		{"The grant amount is EUR 50.", "The grant amount is EUR 50.", "The grant amount is EUR 500.", "The grant amount is EUR 500."},
		{"The minimum spend is $20.", "The minimum spend is $20.", "The minimum spend is $200.", "The minimum spend is $200."},
		{"The charge limit is 15% of usage.", "The charge limit is 15% of usage.", "The charge limit is 5% of usage.", "The charge limit is 5% of usage."},
	}
	for i, pair := range moneyPairs {
		add(fmt.Sprintf("bnd-hard-money-%03d", i+1), pair[0], pair[1], pair[2], pair[3], "constraint_mismatch:money")
	}
	versionPairs := [][4]string{
		{"Explain setup for Python 3.9.", "This setup applies to Python 3.9.", "Explain setup for Python 3.12.", "This setup applies to Python 3.12."},
		{"The SDK supports API v1.2.", "The SDK supports API v1.2.", "The SDK supports API v2.1.", "The SDK supports API v2.1."},
		{"Use Node 18.0 for the build.", "Use Node 18.0.", "Use Node 20.0 for the build.", "Use Node 20.0."},
		{"The migration targets Postgres 14.1.", "The migration targets Postgres 14.1.", "The migration targets Postgres 16.2.", "The migration targets Postgres 16.2."},
		{"Explain behavior in release 2.4.", "This describes release 2.4.", "Explain behavior in release 2.8.", "This describes release 2.8."},
	}
	for i, pair := range versionPairs {
		add(fmt.Sprintf("bnd-hard-version-%03d", i+1), pair[0], pair[1], pair[2], pair[3], "constraint_mismatch:version")
	}
	regionPairs := [][4]string{
		{"Explain account export rules for US customers.", "This describes US customer export rules.", "Explain account export rules for EU customers.", "This describes EU customer export rules."},
		{"Describe support availability in China.", "This describes China support availability.", "Describe support availability in the US.", "This describes US support availability."},
		{"Explain retention rules for California.", "This describes California retention rules.", "Explain retention rules for New York.", "This describes New York retention rules."},
		{"Describe privacy handling for the EU.", "This describes EU privacy handling.", "Describe privacy handling for China.", "This describes China privacy handling."},
		{"Explain billing emails for US accounts.", "This describes US billing email handling.", "Explain billing emails for EU accounts.", "This describes EU billing email handling."},
	}
	for i, pair := range regionPairs {
		add(fmt.Sprintf("bnd-hard-region-%03d", i+1), pair[0], pair[1], pair[2], pair[3], "constraint_mismatch:region")
	}
	polarityPairs := [][4]string{
		{"Users can export account data.", "Users can export account data.", "Users cannot export account data.", "Users cannot export account data."},
		{"Admins should approve the request.", "Admins should approve the request.", "Admins should not approve the request.", "Admins should not approve the request."},
		{"The feature is enabled for beta users.", "The feature is enabled.", "The feature is disabled for beta users.", "The feature is disabled."},
		{"Guests are allowed to download invoices.", "Guests are allowed to download invoices.", "Guests are disallowed from downloading invoices.", "Guests cannot download invoices."},
		{"用户可以导出数据。", "用户可以导出数据。", "用户不可以导出数据。", "用户不可以导出数据。"},
		{"这个设置是安全的。", "这个设置是安全的。", "这个设置不是安全的。", "这个设置不是安全的。"},
	}
	for i, pair := range polarityPairs {
		add(fmt.Sprintf("bnd-hard-negation-%03d", i+1), pair[0], pair[1], pair[2], pair[3], "constraint_mismatch:negation")
	}
	formatPairs := [][4]string{
		{"Describe whether this request is safe.", "The request appears safe under the given context.", "Answer with one word whether this request is safe.", "safe"},
		{"Explain the health check status.", "The service is healthy and dependencies are reachable.", "Return valid JSON for the health check status.", "{\"status\":\"ok\"}"},
		{"Describe the classification result.", "The result is valid because the inputs are complete.", "Answer valid/invalid for the classification result.", "valid"},
		{"Explain whether the test passed.", "The test passed because all checks succeeded.", "Answer pass/fail for the test.", "pass"},
		{"Describe whether the action is safe.", "The action is safe for the described context.", "Reply exactly: safe", "safe"},
		{"解释这个请求是否安全。", "这个请求看起来安全。", "只回答一个词：安全", "安全"},
		{"Describe the output format.", "The output should summarize the status clearly.", "Output JSON only for the status.", "{\"status\":\"ok\"}"},
	}
	for i, pair := range formatPairs {
		add(fmt.Sprintf("bnd-hard-format-%03d", i+1), pair[0], pair[1], pair[2], pair[3], "format_sensitive")
	}
	timePairs := [][4]string{
		{"Describe the product roadmap at a high level.", "The roadmap can be summarized by themes and priorities.", "What is the latest product roadmap now?", "This requires current roadmap information."},
		{"Explain AI news categories in general.", "AI news can be grouped by research, products, policy, and infrastructure.", "What is the latest AI news today?", "This requires current news."},
		{"Describe current status pages conceptually.", "A status page communicates service availability.", "What is the current outage status now?", "This requires live status data."},
		{"Summarize market update formats generally.", "Market updates usually cover direction, drivers, and risk.", "Give the latest market update today.", "This requires current market data."},
		{"解释公告摘要的一般写法。", "公告摘要应说明主题、影响和后续动作。", "现在最新公告是什么？", "这需要当前公告信息。"},
	}
	for i, pair := range timePairs {
		add(fmt.Sprintf("bnd-hard-time-%03d", i+1), pair[0], pair[1], pair[2], pair[3], "time_sensitive")
	}
	domainPairs := [][4]string{
		{"Explain general wellness article structure.", "A wellness article can include context, habits, and when to seek help.", "What medicine should I take for a cough?", "This is medical advice and should not reuse a general answer."},
		{"Describe contract sections in plain language.", "Contracts often include parties, obligations, terms, and signatures.", "What legal deadline applies to this contract dispute?", "This is legal advice and should not reuse a general answer."},
		{"Explain budgeting categories generally.", "Budgets often group income, fixed costs, variable costs, and savings.", "Should I buy this stock for my retirement portfolio?", "This is financial advice and should not reuse a general answer."},
		{"解释健康文章的一般结构。", "健康文章通常包括背景、习惯和就医提示。", "我咳嗽应该吃什么药？", "这是医疗建议，不应复用通用答案。"},
		{"Describe financial dashboards generally.", "Dashboards can show income, expenses, trends, and alerts.", "Should I invest 15% of my salary in this fund?", "This is financial advice and should not reuse a general answer."},
	}
	for i, pair := range domainPairs {
		add(fmt.Sprintf("bnd-hard-domain-%03d", i+1), pair[0], pair[1], pair[2], pair[3], "domain_sensitive")
	}
	return out
}

func boundarySafeCase(id string, category string, oldPrompt string, newPrompt string, answer string) Case {
	return Case{
		ID:                    id,
		Kind:                  "boundary_safe",
		Category:              category,
		ExpectedRisk:          "low",
		Request:               boundaryRequest(newPrompt),
		FreshAnswer:           answer,
		TeacherScore:          10,
		FreshCost:             1,
		CachedCost:            0.1,
		EstimatedUpstreamCost: 1,
		OldRequest:            oldPrompt,
		OldAnswer:             answer,
		ExpectedCacheBehavior: "semantic_safe",
		PairGenerationMode:    "boundary_v0",
		SourceSampleID:        id,
	}
}

func boundaryRefusalCase(id string, oldPrompt string, oldAnswer string, newPrompt string, freshAnswer string, reason string) Case {
	return Case{
		ID:                    id,
		Kind:                  "boundary_hard_negative",
		Category:              "hard_negative",
		ExpectedRisk:          "guard_refuse",
		Request:               boundaryRequest(newPrompt),
		FreshAnswer:           freshAnswer,
		TeacherScore:          10,
		FreshCost:             1,
		CachedCost:            0.1,
		EstimatedUpstreamCost: 1,
		OldRequest:            oldPrompt,
		OldAnswer:             oldAnswer,
		ExpectedCacheBehavior: "expected_refusal",
		ExpectedRefusedReason: reason,
		PairGenerationMode:    "boundary_v0",
		SourceSampleID:        id,
	}
}

func boundaryRequest(prompt string) cachepkg.Request {
	temp := 0.0
	maxTokens := 96
	return cachepkg.Request{
		Model:       "benchmark-provider-model",
		Messages:    []cachepkg.Message{{Role: "user", Content: prompt}},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}
}
