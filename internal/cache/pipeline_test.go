package cache

import (
	"context"
	"strings"
	"testing"

	"github.com/bianjiefilm/CacheSafety-Bench/internal/safety"

	"github.com/stretchr/testify/require"
)

func TestExactHitSkipsUpstream(t *testing.T) {
	p := NewPipeline(PipelineConfig{})
	req := testRequest("What is 2+2?")
	p.Put(req, Response{Text: "4", TeacherScore: 10})

	calls := 0
	got, err := p.Handle(context.Background(), req, func(context.Context, Request) (Response, error) {
		calls++
		return Response{Text: "upstream"}, nil
	})

	require.NoError(t, err)
	require.Equal(t, LayerExact, got.Layer)
	require.Equal(t, "4", got.Response.Text)
	require.Equal(t, 0, calls)
}

func TestCanonicalHitOnlyNormalizesAllowlistedMetadataAndWhitespace(t *testing.T) {
	p := NewPipeline(PipelineConfig{})
	seed := testRequest("  Explain cache keys.  ")
	seed.Metadata = map[string]string{
		"request_id":         "req-a",
		"trace_id":           "trace-a",
		"session_id":         "session-a",
		"metadata_timestamp": "2026-05-14T00:00:00Z",
	}
	p.Put(seed, Response{Text: "A cache key identifies a request.", TeacherScore: 10})

	query := testRequest("Explain   cache   keys.")
	query.Metadata = map[string]string{
		"request_id":         "req-b",
		"trace_id":           "trace-b",
		"session_id":         "session-b",
		"metadata_timestamp": "2026-05-14T01:00:00Z",
	}

	got, err := p.Handle(context.Background(), query, failUpstream(t))

	require.NoError(t, err)
	require.Equal(t, LayerCanonical, got.Layer)
}

func TestCanonicalMustNotNormalizePromptBodyNumbersVersionsDatesOrAmounts(t *testing.T) {
	cases := []struct {
		name  string
		seed  string
		query string
	}{
		{name: "numbers", seed: "What is 2+2?", query: "What is 3+3?"},
		{name: "python minor versions", seed: "Python 3.9", query: "Python 3.12"},
		{name: "versions", seed: "Summarize release v1.2.3", query: "Summarize release v1.2.4"},
		{name: "dates", seed: "Report for 2026-05-14", query: "Report for 2026-05-15"},
		{name: "amounts", seed: "Invoice amount is $120", query: "Invoice amount is $130"},
		{name: "legal deadlines", seed: "refund in 7 days", query: "refund in 30 days"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPipeline(PipelineConfig{Similarity: func(Request, Request) float64 { return 0 }})
			p.Put(testRequest(tc.seed), Response{Text: "cached", TeacherScore: 10})

			got, err := p.Handle(context.Background(), testRequest(tc.query), func(context.Context, Request) (Response, error) {
				return Response{Text: "fresh", TeacherScore: 10}, nil
			})

			require.NoError(t, err)
			require.Equal(t, LayerMiss, got.Layer)
			require.Equal(t, "fresh", got.Response.Text)
			require.True(t, got.UpstreamCalled)
		})
	}
}

func TestSemanticHitOnlyWhenAboveThreshold(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SemanticThreshold: 0.95,
		Similarity:        func(Request, Request) float64 { return 0.97 },
	})
	p.Put(testRequest("Explain exact cache."), Response{Text: "Exact cache uses identical keys.", TeacherScore: 10})

	got, err := p.Handle(context.Background(), testRequest("Describe exact cache behavior."), failUpstream(t))

	require.NoError(t, err)
	require.Equal(t, LayerSemantic, got.Layer)
	require.InDelta(t, 0.97, got.Similarity, 0.0001)
}

func TestSemanticBelowThresholdFallsBackToUpstream(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SemanticThreshold: 0.95,
		Similarity:        func(Request, Request) float64 { return 0.94 },
	})
	p.Put(testRequest("Explain exact cache."), Response{Text: "cached", TeacherScore: 10})

	got, err := p.Handle(context.Background(), testRequest("Describe exact cache behavior."), func(context.Context, Request) (Response, error) {
		return Response{Text: "fresh", TeacherScore: 10}, nil
	})

	require.NoError(t, err)
	require.Equal(t, LayerMiss, got.Layer)
	require.Equal(t, "fresh", got.Response.Text)
	require.True(t, got.UpstreamCalled)
}

func TestSafetyRefusedSemanticHitFallsBackToUpstream(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SemanticThreshold: 0.95,
		Similarity:        func(Request, Request) float64 { return 0.99 },
		Safety:            safety.DefaultChecker(),
	})
	p.Put(testRequest("Explain daily summaries."), Response{Text: "cached", TeacherScore: 10})

	got, err := p.Handle(context.Background(), testRequest("What is the latest AI news today?"), func(context.Context, Request) (Response, error) {
		return Response{Text: "fresh news", TeacherScore: 10}, nil
	})

	require.NoError(t, err)
	require.Equal(t, LayerMiss, got.Layer)
	require.Equal(t, safety.RuleTimeSensitive, got.RefusedByRule)
	require.True(t, got.UpstreamCalled)
	require.Equal(t, "fresh news", got.Response.Text)
}

func TestConstraintMismatchRefusedSemanticHitFallsBackToUpstream(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SemanticThreshold: 0.95,
		Similarity:        func(Request, Request) float64 { return 0.99 },
		Safety:            safety.DefaultChecker(),
	})
	p.Put(testRequest("The account is eligible for automatic renewal."), Response{Text: "eligible", TeacherScore: 10})

	got, err := p.Handle(context.Background(), testRequest("The account is not eligible for automatic renewal."), func(context.Context, Request) (Response, error) {
		return Response{Text: "fresh", TeacherScore: 10}, nil
	})

	require.NoError(t, err)
	require.Equal(t, LayerMiss, got.Layer)
	require.Equal(t, safety.RuleConstraintMismatch, got.RefusedByRule)
	require.Equal(t, "constraint_mismatch:negation", got.RefusalDetail)
	require.True(t, got.UpstreamCalled)
	require.Equal(t, "fresh", got.Response.Text)
}

func TestFormatSensitiveRefusedSemanticHitFallsBackToUpstream(t *testing.T) {
	p := NewPipeline(PipelineConfig{
		SemanticThreshold: 0.95,
		Similarity:        func(Request, Request) float64 { return 0.99 },
		Safety:            safety.DefaultChecker(),
	})
	p.Put(testRequest("Reply with one word about safe semantic cache reuse."), Response{Text: "Valid", TeacherScore: 10})

	got, err := p.Handle(context.Background(), testRequest("Give one word describing safe semantic cache reuse."), func(context.Context, Request) (Response, error) {
		return Response{Text: "safe", TeacherScore: 10}, nil
	})

	require.NoError(t, err)
	require.Equal(t, LayerMiss, got.Layer)
	require.Equal(t, safety.RuleFormatSensitive, got.RefusedByRule)
	require.Equal(t, "format_sensitive:one_word", got.RefusalDetail)
	require.True(t, got.UpstreamCalled)
	require.Equal(t, "safe", got.Response.Text)
}

func TestSemanticSafetyRulesThroughPipeline(t *testing.T) {
	tests := []struct {
		name   string
		req    Request
		resp   Response
		refuse safety.Rule
	}{
		{name: "medical", req: testRequest("What medicine should I take for a cough?"), resp: Response{Text: "cached", TeacherScore: 10}, refuse: safety.RuleDomainSensitive},
		{name: "legal", req: testRequest("What is the legal deadline for this contract?"), resp: Response{Text: "cached", TeacherScore: 10}, refuse: safety.RuleDomainSensitive},
		{name: "financial", req: testRequest("Should I buy this stock for my portfolio?"), resp: Response{Text: "cached", TeacherScore: 10}, refuse: safety.RuleDomainSensitive},
		{name: "tools", req: requestWithTool("Calculate using a function."), resp: Response{Text: "cached", TeacherScore: 10}, refuse: safety.RuleHasTools},
		{name: "function call", req: requestWithFunctionCall("Calculate using a function."), resp: Response{Text: "cached", TeacherScore: 10}, refuse: safety.RuleHasTools},
		{name: "low quality", req: testRequest("Explain deterministic test strategy."), resp: Response{Text: "cached", TeacherScore: 8.49}, refuse: safety.RuleLowQuality},
		{name: "too long", req: testRequest(strings.Repeat("long ", 120)), resp: Response{Text: "cached", TeacherScore: 10}, refuse: safety.RulePromptTooLong},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPipeline(PipelineConfig{
				SemanticThreshold: 0.95,
				Similarity:        func(Request, Request) float64 { return 0.99 },
			})
			p.Put(testRequest("Explain deterministic tests."), tc.resp)

			got, err := p.Handle(context.Background(), tc.req, func(context.Context, Request) (Response, error) {
				return Response{Text: "fresh", TeacherScore: 10}, nil
			})

			require.NoError(t, err)
			require.Equal(t, LayerMiss, got.Layer)
			require.Equal(t, tc.refuse, got.RefusedByRule)
			require.True(t, got.UpstreamCalled)
		})
	}
}

func testRequest(prompt string) Request {
	temp := 0.2
	return Request{
		Model:          "test-model",
		SystemPrompt:   "Be concise.",
		Messages:       []Message{{Role: "user", Content: prompt}},
		Temperature:    &temp,
		ResponseFormat: "text",
	}
}

func requestWithTool(prompt string) Request {
	req := testRequest(prompt)
	req.Tools = []Tool{{Name: "calculator"}}
	return req
}

func requestWithFunctionCall(prompt string) Request {
	req := testRequest(prompt)
	req.Extra = map[string]any{"function_call": map[string]any{"name": "calculator"}}
	return req
}

func failUpstream(t *testing.T) UpstreamFunc {
	t.Helper()
	return func(context.Context, Request) (Response, error) {
		t.Fatalf("upstream should not be called")
		return Response{}, nil
	}
}
