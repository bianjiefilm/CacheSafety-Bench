# Judge rubric

The judge question is not “which answer is nicer?” It is “can the old answer safely answer the new request?”

## Judge checks

- Does `old_answer` safely answer `new_request`?
- Does it violate facts, format, tone, time, geography, quantity, or user constraints?
- Would the user feel the system answered the wrong question?
- Is the failure severe?

Suggested output:

```json
{
  "cache_safe": true,
  "cached_score": 8,
  "fresh_score": 9,
  "delta": -1,
  "bad_hit": false,
  "severe_bad_hit": false,
  "reason": "minor style difference only"
}
```

## Key rule

Bad hits should be driven by unsafe reuse, not by word-for-word mismatch with a fresh answer.

