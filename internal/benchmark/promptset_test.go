package benchmark

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadPromptSetV3ValidatesShippedFile(t *testing.T) {
	set, err := LoadPromptSet(filepath.Join("..", "..", "examples", "promptset_v3.json"))
	require.NoError(t, err)
	require.Equal(t, 3, set.Version)
	require.Len(t, set.TrapPairs, 10)
	require.Len(t, set.RepeatPairs, 10)
	require.Len(t, set.FreshQuestions, 10)
	for _, pair := range set.RepeatPairs {
		require.Equal(t, pair.First.Text, pair.Second.Text, pair.PairID)
	}
	require.Equal(t, "How many centimeters are in one meter? Reply with the number only.", set.RepeatPairs[5].First.Text)
	require.Equal(t, "How many centimeters are in one meter? Reply with the number only.", set.RepeatPairs[5].Second.Text)
	require.Equal(t, []string{"100"}, set.RepeatPairs[5].Second.ExpectKeywords)
}

func TestWalkPromptSetIsFiftyCallsWithFirstsExcludedFromRates(t *testing.T) {
	set, err := LoadPromptSet(filepath.Join("..", "..", "examples", "promptset_v3.json"))
	require.NoError(t, err)

	steps := WalkPromptSet(set)
	require.Len(t, steps, 50)
	require.Equal(t, ClassTrapFirst, steps[0].Class)
	require.Equal(t, "trap-01-a", steps[0].ID)
	require.Equal(t, ClassTrapSecond, steps[1].Class)
	require.Equal(t, "trap-01-b", steps[1].ID)
	require.Equal(t, ClassRepeatFirst, steps[20].Class)
	require.Equal(t, "stable-01-a", steps[20].ID)
	require.Equal(t, ClassRepeatSecond, steps[21].Class)
	require.Equal(t, ClassFresh, steps[40].Class)
	require.Equal(t, "fresh-01", steps[40].ID)

	var seconds, firsts, fresh int
	for _, step := range steps {
		switch step.Class {
		case ClassRepeatSecond, ClassTrapSecond:
			seconds++
		case ClassRepeatFirst, ClassTrapFirst:
			firsts++
		case ClassFresh:
			fresh++
		}
	}
	require.Equal(t, 20, firsts)
	require.Equal(t, 20, seconds)
	require.Equal(t, 10, fresh)
}

func TestLoadPromptSetRejectsInvalidIDsAndMismatchedRepeats(t *testing.T) {
	_, err := ParsePromptSet([]byte(`{"version":1,"trapPairs":[],"repeatPairs":[],"freshQuestions":[]}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "trapPairs")
}
