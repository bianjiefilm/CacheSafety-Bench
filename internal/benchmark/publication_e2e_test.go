package benchmark

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	cachepkg "github.com/bianjiefilm/CacheSafety-Bench/internal/cache"
	"github.com/bianjiefilm/CacheSafety-Bench/internal/provider"

	"github.com/stretchr/testify/require"
)

func TestPublicationE2E_HappyExact(t *testing.T) {
	set := loadPublicationPromptSet(t)
	score, decisions := runPublicationE2E(t, set, publicationHappyExactPlan(t, set))

	require.Equal(t, 50, score.SampleSize)
	require.Equal(t, 3, score.PromptSetVersion)
	require.Equal(t, rate4(1.0), score.SafeHitRate)
	require.Equal(t, rate4(0), score.BadHitRate)
	require.Equal(t, rate4(0), score.SemanticTrapFailureRate)
	require.Equal(t, 10, score.ServedModeCounts["exact_cache"])
	require.Equal(t, 40, score.ServedModeCounts["fresh"])
	require.Len(t, decisions, 50)
	require.Equal(t, "observed", decisions[21].CacheSource)
	require.Equal(t, "exact_cache", decisions[21].ObservedServeMode)
	require.Equal(t, "exact", decisions[21].CacheLayer)
}

func TestPublicationE2E_BadHit(t *testing.T) {
	set := loadPublicationPromptSet(t)
	score, decisions := runPublicationE2E(t, set, publicationBadHitPlan(t, set))

	require.Equal(t, 50, score.SampleSize)
	require.Equal(t, rate4(1.0), score.SafeHitRate)
	require.Equal(t, rate4(0.1), score.BadHitRate)
	require.Equal(t, rate4(0), score.SemanticTrapFailureRate)
	require.Equal(t, 11, score.ServedModeCounts["exact_cache"])
	require.Equal(t, 39, score.ServedModeCounts["fresh"])
	require.Equal(t, "trap-01-b", decisions[1].SampleID)
	require.Equal(t, "exact_cache", decisions[1].ObservedServeMode)
}

func runPublicationE2E(t *testing.T, set PromptSet, plan func(int, string) provider.ScriptedReply) (PublicationScorecard, []DecisionRecord) {
	t.Helper()
	gateway := provider.StartScriptedGateway(plan)
	t.Cleanup(gateway.Close)

	client, err := provider.NewOpenAIProvider(provider.OpenAIConfig{
		APIKey:  "test-e2e-key",
		BaseURL: gateway.URL + "/v1",
		Model:   "e2e-model",
	}, gateway.Client())
	require.NoError(t, err)

	score, decisions, err := RunPublication(context.Background(), set, Config{
		Dataset:     "examples/promptset_v3.json",
		CacheSource: "observed",
	}, func(ctx context.Context, item Case) (cachepkg.Response, error) {
		result, callErr := client.Complete(ctx, item.Request)
		if callErr != nil {
			return cachepkg.Response{}, callErr
		}
		return cachepkg.Response{
			Text:        result.Response.Content,
			Observation: result.Observation,
		}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 50, gateway.Calls())
	require.False(t, strings.Contains(gateway.URL, "api.nextmodel.app"))
	return score, decisions
}

func loadPublicationPromptSet(t *testing.T) PromptSet {
	t.Helper()
	set, err := LoadPromptSet(filepath.Join("..", "..", "examples", "promptset_v3.json"))
	require.NoError(t, err)
	require.Len(t, WalkPromptSet(set), 50)
	return set
}

func publicationHappyExactPlan(t *testing.T, set PromptSet) func(int, string) provider.ScriptedReply {
	t.Helper()
	steps := WalkPromptSet(set)
	return func(callIndex int, prompt string) provider.ScriptedReply {
		step := steps[callIndex]
		require.Equal(t, strings.TrimSpace(step.Text), strings.TrimSpace(prompt), step.ID)
		reply := provider.ScriptedReply{
			ServeMode:   "fresh",
			Content:     strings.Join(step.ExpectKeywords, " "),
			RequestID:   "req-" + step.ID,
			ReceiptHash: "hash-" + step.ID,
		}
		if step.Class == ClassRepeatSecond {
			reply.ServeMode = cachepkg.ServeModeExactCache
		}
		return reply
	}
}

func publicationBadHitPlan(t *testing.T, set PromptSet) func(int, string) provider.ScriptedReply {
	t.Helper()
	happy := publicationHappyExactPlan(t, set)
	return func(callIndex int, prompt string) provider.ScriptedReply {
		reply := happy(callIndex, prompt)
		if callIndex == 1 {
			reply.ServeMode = cachepkg.ServeModeExactCache
			reply.Content = strings.Join(WalkPromptSet(set)[1].StaleKeywords, " ")
		}
		return reply
	}
}
