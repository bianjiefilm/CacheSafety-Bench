package benchmark

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

type samplingSelection struct {
	Mode             string
	StratifyBy       []string
	StrataCount      int
	SampledByStratum map[string]int
}

func selectCases(cases []Case, cfg Config) ([]Case, samplingSelection) {
	if cfg.OnlyReplayPairs {
		cases = filterReplayPairs(cases)
	}
	cases = filterSampleIDs(cases, cfg.SampleIDs)
	cases = selectCaseRange(cases, cfg.StartIndex, cfg.EndIndex)
	if cfg.Stratified {
		return stratifiedSample(cases, cfg)
	}
	selected := append([]Case(nil), cases...)
	if cfg.MaxSamples > 0 && len(selected) > cfg.MaxSamples {
		selected = selected[:cfg.MaxSamples]
	}
	mode := "head"
	if cfg.MaxSamples <= 0 {
		mode = "all"
	}
	return selected, samplingSelection{Mode: mode}
}

func filterSampleIDs(cases []Case, sampleIDs []string) []Case {
	if len(sampleIDs) == 0 {
		return cases
	}
	allowed := map[string]struct{}{}
	for _, sampleID := range sampleIDs {
		sampleID = strings.TrimSpace(sampleID)
		if sampleID != "" {
			allowed[sampleID] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return cases
	}
	out := make([]Case, 0, len(cases))
	for _, item := range cases {
		if _, ok := allowed[item.ID]; ok {
			out = append(out, item)
		}
	}
	return out
}

func filterReplayPairs(cases []Case) []Case {
	out := make([]Case, 0, len(cases))
	for _, item := range cases {
		if strings.TrimSpace(item.Kind) == "replay_pair" || (strings.TrimSpace(item.OldRequest) != "" && strings.TrimSpace(item.OldAnswer) != "") {
			out = append(out, item)
		}
	}
	return out
}

func selectCaseRange(cases []Case, startIndex int, endIndex int) []Case {
	if startIndex < 0 {
		startIndex = 0
	}
	if endIndex <= 0 || endIndex > len(cases) {
		endIndex = len(cases)
	}
	if startIndex > len(cases) {
		startIndex = len(cases)
	}
	if endIndex < startIndex {
		endIndex = startIndex
	}
	return append([]Case(nil), cases[startIndex:endIndex]...)
}

func stratifiedSample(cases []Case, cfg Config) ([]Case, samplingSelection) {
	fields := normalizeStratifyBy(cfg.StratifyBy)
	if len(fields) == 0 {
		fields = []string{"category"}
	}
	groups := map[string][]Case{}
	keys := make([]string, 0)
	for _, item := range cases {
		key := stratumKey(item, fields)
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], item)
	}
	sort.Strings(keys)
	rng := rand.New(rand.NewSource(cfg.Seed))
	selected := make([]Case, 0, len(cases))
	sampledByStratum := map[string]int{}
	selectedByStratum := map[string][]Case{}
	for _, key := range keys {
		group := append([]Case(nil), groups[key]...)
		target := len(group)
		if cfg.MaxPerStratum > 0 && target > cfg.MaxPerStratum {
			target = cfg.MaxPerStratum
		}
		if cfg.MinPerStratum > 0 && len(group) >= cfg.MinPerStratum && target < cfg.MinPerStratum {
			target = cfg.MinPerStratum
		}
		if target < len(group) {
			rng.Shuffle(len(group), func(i, j int) { group[i], group[j] = group[j], group[i] })
			group = group[:target]
			sort.SliceStable(group, func(i, j int) bool { return group[i].ID < group[j].ID })
		}
		selectedByStratum[key] = group
		selected = append(selected, group...)
		sampledByStratum[key] = len(group)
	}
	if cfg.MaxSamples > 0 && len(selected) > cfg.MaxSamples {
		selected = selected[:0]
		sampledByStratum = map[string]int{}
		shuffledByStratum := map[string][]Case{}
		for _, key := range keys {
			group := append([]Case(nil), selectedByStratum[key]...)
			rng.Shuffle(len(group), func(i, j int) { group[i], group[j] = group[j], group[i] })
			shuffledByStratum[key] = group
		}
		for depth := 0; len(selected) < cfg.MaxSamples; depth++ {
			added := false
			for _, key := range keys {
				group := shuffledByStratum[key]
				if depth >= len(group) {
					continue
				}
				selected = append(selected, group[depth])
				sampledByStratum[key]++
				added = true
				if len(selected) >= cfg.MaxSamples {
					break
				}
			}
			if !added {
				break
			}
		}
	}
	return selected, samplingSelection{
		Mode:             "stratified",
		StratifyBy:       fields,
		StrataCount:      len(keys),
		SampledByStratum: sampledByStratum,
	}
}

func normalizeStratifyBy(fields []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range fields {
		field := strings.ToLower(strings.TrimSpace(raw))
		if field == "" || seen[field] {
			continue
		}
		switch field {
		case "category", "source_kind", "expected_cache_behavior":
			out = append(out, field)
			seen[field] = true
		}
	}
	return out
}

func stratumKey(item Case, fields []string) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, fmt.Sprintf("%s=%s", field, stratumValue(item, field)))
	}
	return strings.Join(parts, "|")
}

func stratumValue(item Case, field string) string {
	switch field {
	case "category":
		return defaultString(normalizedCategory(item.Category), "uncategorized")
	case "source_kind":
		return defaultString(item.SourceKind, "unknown")
	case "expected_cache_behavior":
		return defaultString(item.ExpectedCacheBehavior, "unspecified")
	default:
		return "unknown"
	}
}
