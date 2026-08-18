package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	ClassTrapFirst    = "trap-first"
	ClassTrapSecond   = "trap-second"
	ClassRepeatFirst  = "repeat-first"
	ClassRepeatSecond = "repeat-second"
	ClassFresh        = "fresh"
	DefaultPromptSet  = "examples/promptset_v3.json"
)

var promptIDPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

type PromptSet struct {
	Version        int          `json:"version"`
	TrapPairs      []PromptPair `json:"trapPairs"`
	RepeatPairs    []PromptPair `json:"repeatPairs"`
	FreshQuestions []PromptTurn `json:"freshQuestions"`
}

type PromptPair struct {
	PairID string     `json:"pairId"`
	First  PromptTurn `json:"first"`
	Second PromptTurn `json:"second"`
}

type PromptTurn struct {
	ID             string   `json:"id"`
	Text           string   `json:"text"`
	ExpectKeywords []string `json:"expectKeywords"`
	StaleKeywords  []string `json:"staleKeywords,omitempty"`
}

type PromptStep struct {
	Class          string
	PairID         string
	ID             string
	Text           string
	ExpectKeywords []string
	StaleKeywords  []string
}

func LoadPromptSet(path string) (PromptSet, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return PromptSet{}, err
	}
	return ParsePromptSet(bytes)
}

func ParsePromptSet(raw []byte) (PromptSet, error) {
	var set PromptSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return PromptSet{}, fmt.Errorf("decode promptset: %w", err)
	}
	if err := validatePromptSet(set); err != nil {
		return PromptSet{}, err
	}
	return set, nil
}

func WalkPromptSet(set PromptSet) []PromptStep {
	steps := make([]PromptStep, 0, 50)
	for _, pair := range set.TrapPairs {
		steps = append(steps, turnStep(ClassTrapFirst, pair.PairID, pair.First))
		steps = append(steps, turnStep(ClassTrapSecond, pair.PairID, pair.Second))
	}
	for _, pair := range set.RepeatPairs {
		steps = append(steps, turnStep(ClassRepeatFirst, pair.PairID, pair.First))
		steps = append(steps, turnStep(ClassRepeatSecond, pair.PairID, pair.Second))
	}
	for _, turn := range set.FreshQuestions {
		steps = append(steps, turnStep(ClassFresh, "", turn))
	}
	return steps
}

func turnStep(class, pairID string, turn PromptTurn) PromptStep {
	return PromptStep{
		Class:          class,
		PairID:         pairID,
		ID:             turn.ID,
		Text:           turn.Text,
		ExpectKeywords: append([]string(nil), turn.ExpectKeywords...),
		StaleKeywords:  append([]string(nil), turn.StaleKeywords...),
	}
}

func validatePromptSet(set PromptSet) error {
	if set.Version < 1 {
		return fmt.Errorf("promptset version must be >= 1")
	}
	if len(set.TrapPairs) != 10 {
		return fmt.Errorf("promptset trapPairs: want 10, got %d", len(set.TrapPairs))
	}
	if len(set.RepeatPairs) != 10 {
		return fmt.Errorf("promptset repeatPairs: want 10, got %d", len(set.RepeatPairs))
	}
	if len(set.FreshQuestions) != 10 {
		return fmt.Errorf("promptset freshQuestions: want 10, got %d", len(set.FreshQuestions))
	}

	seenIDs := map[string]bool{}
	seenPairs := map[string]bool{}
	for _, pair := range set.TrapPairs {
		if err := validatePair(pair, seenIDs, seenPairs, true); err != nil {
			return err
		}
	}
	for _, pair := range set.RepeatPairs {
		if strings.TrimSpace(pair.First.Text) != strings.TrimSpace(pair.Second.Text) {
			return fmt.Errorf("repeat pair %s: first.text must equal second.text", pair.PairID)
		}
		if err := validatePair(pair, seenIDs, seenPairs, false); err != nil {
			return err
		}
	}
	for _, turn := range set.FreshQuestions {
		if err := validateTurn(turn, seenIDs, false); err != nil {
			return err
		}
	}
	return nil
}

func validatePair(pair PromptPair, seenIDs, seenPairs map[string]bool, trap bool) error {
	pairID := strings.TrimSpace(pair.PairID)
	if !promptIDPattern.MatchString(pairID) {
		return fmt.Errorf("invalid pairId %q", pair.PairID)
	}
	if seenPairs[pairID] {
		return fmt.Errorf("duplicate pairId %q", pairID)
	}
	seenPairs[pairID] = true
	if err := validateTurn(pair.First, seenIDs, false); err != nil {
		return fmt.Errorf("%s first: %w", pairID, err)
	}
	if err := validateTurn(pair.Second, seenIDs, trap); err != nil {
		return fmt.Errorf("%s second: %w", pairID, err)
	}
	return nil
}

func validateTurn(turn PromptTurn, seenIDs map[string]bool, requireStale bool) error {
	id := strings.TrimSpace(turn.ID)
	if !promptIDPattern.MatchString(id) {
		return fmt.Errorf("invalid id %q", turn.ID)
	}
	if seenIDs[id] {
		return fmt.Errorf("duplicate id %q", id)
	}
	seenIDs[id] = true
	if len(strings.TrimSpace(turn.Text)) < 10 {
		return fmt.Errorf("%s text must be at least 10 characters", id)
	}
	if err := validateKeywords(turn.ExpectKeywords, "expectKeywords"); err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}
	if requireStale {
		if err := validateKeywords(turn.StaleKeywords, "staleKeywords"); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	} else if err := validateOptionalKeywords(turn.StaleKeywords, "staleKeywords"); err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}
	return nil
}

func validateKeywords(values []string, field string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	return validateOptionalKeywords(values, field)
}

func validateOptionalKeywords(values []string, field string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must contain non-empty strings", field)
		}
	}
	return nil
}
