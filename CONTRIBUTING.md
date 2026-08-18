# Contributing

Thanks for helping improve CacheSafety Bench.

## Ground rules

- Keep the project endpoint-neutral.
- Optimize for Safe Hit Rate, not raw hit rate.
- Do not add real customer prompts, real API keys, or redacted production traffic.
- Do not introduce heavy dependencies for convenience.

## Development

```bash
go test ./...
go vet ./...
go test ./internal/benchmark -run PublicationE2E -count=1
```
go run ./cmd/cachesafetybench run \
  --dataset examples/support_pairs.jsonl \
  --config configs/default.yaml \
  --output reports/local-report.json
```

## Pull requests

- Keep changes scoped and reviewable.
- Update docs when public behavior changes.
- Add or update toy fixtures when benchmark behavior changes.
- Explain safety tradeoffs, especially around bad hits, semantic traps, and cost metrics.

## Dataset contributions

- Submit toy or synthetic examples only.
- Do not submit medical, legal, or financial prompts as “safe reuse” examples.
- Do not submit prompts containing personal data, secrets, or private support logs.

