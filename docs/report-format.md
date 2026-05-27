# Report format

The public wrapper emits a compact report shaped like:

```json
{
  "total_pairs": 2000,
  "safe_hit_rate": 0.184,
  "bad_hit_rate": 0.0,
  "severe_bad_hit_count": 0,
  "cost_saved_per_1k_requests_usd": 0.42,
  "net_saving_rate": 0.17,
  "semantic_trap_failure_rate": 0.0,
  "cache_layer_contribution": {
    "exact": 220,
    "canonical": 148,
    "semantic": 0
  },
  "judge_delta": -0.12,
  "regression_escape_rate": 0.0,
  "best_policy": "exact+canonical",
  "semantic_cache_recommended": false,
  "bad_hits": []
}
```

## Notes

- HTML output is a lightweight summary view over the same report data.
- JSON is the machine-readable source of truth.
- `best_policy` is a recommendation, not a deployment command.
- `semantic_cache_recommended` should remain conservative.

