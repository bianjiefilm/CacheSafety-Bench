# Metrics

## Scorecards

CacheSafety Bench has two scorecards. They share field names on purpose; the denominators are different.

### Lab (default)

The in-process runner. Safe / bad rates use the **total sample count**. FakeJudge and the local exact → canonical → semantic pipeline stay here.

### Publication

The NextModel hosted bench page formula. Use `-scorecard publication` with `-observe`. It never silently scores the lab pipeline.

- Promptset: `examples/promptset_v3.json` (10 trap pairs, 10 repeat pairs, 10 fresh questions; 50 calls).
- First calls are recorded but do not enter SH / BH / trap denominators.
- Hit: serve-mode `exact_cache`, `canonical_cache`, or `ln_beta` (case-sensitive as served), or mapped layers `exact` / `canonical` / `ln_beta`.
- Correct: every `expectKeywords` entry is a case-insensitive substring of the served content.
- `safe_hit_rate` = count(repeat-second AND hit AND correct) / count(repeat-second)
- `bad_hit_rate` = count(trap-second AND hit AND content contains all staleKeywords) / count(trap-second)
- `semantic_trap_failure_rate` = count(fresh AND hit) / count(fresh)
- Empty cohort rates are `null`, not `0.0`. Rates round to 4 decimal places.
- `cost_saved_per_1k_requests` is a display field from cached-token usage and an optional input price. It is not lab billing or settlement.

## Safe Hit Rate

- Definition: share of requests where a cached answer was reused and judged safe.
- Formula (lab): `safe_hits / total_requests`
- Why it matters: this is the reuse metric that actually helps users and cost.
- Failure mode: a high hit rate can hide unsafe reuse if safe hits are not separated from bad hits.
- Example JSON field: `safe_hit_rate`

## Bad Hit Rate

- Definition: share of requests where a cached answer was returned but should not have been reused.
- Formula: `bad_hits / total_requests`
- Why it matters: this is the safety line.
- Failure mode: optimizing hit rate can raise bad hits before it raises safe value.
- Example JSON field: `bad_hit_rate`

## Severe Bad Hit Count

- Definition: count of bad hits that violate high-risk constraints such as critical numbers, dates, policies, or high-risk categories.
- Formula: `count(bad_hit && severe=true)`
- Why it matters: a few severe failures can matter more than many harmless misses.
- Failure mode: an aggregate bad hit rate can look acceptable while severe errors remain hidden.
- Example JSON field: `severe_bad_hit_count`

## Cost Saved / 1K Requests

- Definition: estimated cost savings normalized to 1,000 requests.
- Formula: `(safe_cost_saved / total_requests) * 1000`
- Why it matters: it compares policies on a common scale.
- Failure mode: savings can be overstated when unsafe hits are counted as wins.
- Example JSON field: `cost_saved_per_1k_requests_usd`

## Net Saving Rate

- Definition: share of upstream cost saved after accounting for evaluation overhead recorded by the runner.
- Formula: `net_cost_saved / total_upstream_cost`
- Why it matters: semantic checks and judges are not free.
- Failure mode: gross savings can look positive while net savings disappear.
- Example JSON field: `net_saving_rate`

## Semantic Trap Failure Rate

- Definition: failure rate on semantic trap samples.
- Formula: `semantic_trap_failures / semantic_trap_samples`
- Why it matters: semantic traps test whether “looks similar” becomes “wrong answer reused”.
- Failure mode: a strategy can score well on paraphrases but still fail on traps.
- Example JSON field: `semantic_trap_failure_rate`

## Cache Layer Contribution

- Definition: how many safe outcomes came from exact, canonical, or semantic layers.
- Formula: per-layer count or per-layer share across total requests.
- Why it matters: it shows where safe savings actually come from.
- Failure mode: a semantic layer may add complexity without meaningful safe contribution.
- Example JSON field: `cache_layer_contribution`
- Observed gateway runs may also report `ln_beta` when `x-nextmodel-serve-mode` is `ln_beta`. That is the gateway's named layer, not a remapped `semantic` count.

## Judge Delta

- Definition: average score delta between cached and fresh answers for judged hits.
- Formula: `avg(cached_score - fresh_score)`
- Why it matters: it gives a compact quality gap signal.
- Failure mode: small average deltas can still hide a few severe failures.
- Example JSON field: `judge_delta`

## Regression Escape Rate

- Definition: rate at which known regression cases slipped through a candidate policy.
- Formula: `escaped_regressions / regression_cases`
- Why it matters: it checks whether previously known bad hits are still blocked.
- Failure mode: a model can improve headline metrics while reintroducing old failures.
- Example JSON field: `regression_escape_rate`

