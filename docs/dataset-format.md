# Dataset format

CacheSafety Bench uses JSONL.

Recommended pair shape:

```json
{
  "id": "sample_001",
  "category": "support",
  "old_request": {
    "model": "benchmark-provider-model",
    "messages": [
      { "role": "user", "content": "How do I reset my password?" }
    ]
  },
  "old_answer": {
    "text": "Open Settings, choose Security, and follow the password reset steps."
  },
  "new_request": {
    "model": "benchmark-provider-model",
    "messages": [
      { "role": "user", "content": "How do I reset my password?" }
    ]
  },
  "fresh_answer": "Open Settings, choose Security, and follow the password reset steps.",
  "expected_risk": "low",
  "estimated_upstream_cost_usd": 0.0021,
  "notes": "exact duplicate toy sample"
}
```

## Required ideas

- `old_request` and `new_request` may be a subset of an OpenAI chat-completions request.
- `old_answer` is the candidate cached answer.
- `fresh_answer` is strongly recommended for offline benchmark runs.
- `estimated_upstream_cost_usd` is used for cost estimation in toy or replay datasets.

## Categories

Suggested categories:

- `support`
- `rewrite`
- `translation`
- `summary`
- `semantic_trap`
- `real_chat`

## Why semantic traps matter

A semantic trap is a pair that looks similar but should not reuse the old answer. These samples are important because they reveal unsafe semantic hits that raw similarity cannot catch.

## Privacy rules

- Do not submit real user prompts or tickets to the public dataset.
- Do not include secrets, emails, account IDs, or provider keys.
- Use toy or synthetic samples only.

