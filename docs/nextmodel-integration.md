# NextModel integration

CacheSafety Bench works with any OpenAI-compatible endpoint.

## Neutral stance

- Works with any OpenAI-compatible endpoint.
- NextModel is an optional hosted endpoint and production gateway example.
- This benchmark does not require NextModel.
- Local toy runs keep using the in-process cache simulation. Observation is an added mode.

## Example environment

```bash
export OPENAI_API_KEY=...
export OPENAI_BASE_URL=https://api.nextmodel.app/v1
export OPENAI_MODEL=your-model-id
```

You can also point the same workflow at:

```bash
export OPENAI_API_KEY=...
export OPENAI_BASE_URL=https://api.openai.com/v1
export OPENAI_MODEL=gpt-4.1-mini
```

Optional timeout:

```bash
export OPENAI_TIMEOUT_SECONDS=60
```

## Two ways the runner can use a live endpoint

### 1. Local simulation (default)

The Go runner still seeds `old_request` / `old_answer`, looks up `new_request` through exact → canonical → semantic, and judges hits itself.

`-provider openai` only fills **misses** with a live chat-completions call, the same way `-provider volcengine` does. This does not replace the in-process pipeline.

```bash
go run ./cmd/benchmark \
  -dataset testdata/benchmark/synthetic_v0.jsonl \
  -provider openai \
  -model "$OPENAI_MODEL"
```

### 2. Observe a live gateway

`-observe` skips the in-process cache decision and scores whatever the gateway actually served.

Each request is a chat-completions call. The runner records these response headers when present:

| Header | Decision-log field |
| --- | --- |
| `x-nextmodel-serve-mode` | `observed_serve_mode` |
| `x-nextmodel-request-id` | `observed_request_id` |
| `x-nextmodel-receipt-hash` | `observed_receipt_hash` |
| `x-nextmodel-receipt-url` | `observed_receipt_url` |

Known **hit** serve-modes:

| `x-nextmodel-serve-mode` | Cache layer |
| --- | --- |
| `exact_cache` | `exact` |
| `canonical_cache` | `canonical` |
| `ln_beta` | `ln_beta` (named layer, not remapped to `semantic`) |

Anything else (`fresh`, empty, unknown) is a **miss**.

```bash
go run ./cmd/benchmark \
  -dataset testdata/benchmark/synthetic_v0.jsonl \
  -observe \
  -model "$OPENAI_MODEL" \
  -decision-log reports/observed-decisions.jsonl \
  -output reports/observed-metrics.json
```

`-observe` implies `-provider openai`. Metrics include `"cache_source": "observed"`. Decision log rows include `cache_layer` from the mapped serve-mode plus the raw header fields.

The public wrapper can do the same:

```bash
go run ./cmd/cachesafetybench run \
  --dataset examples/support_pairs.jsonl \
  --config configs/default.yaml \
  --observe \
  --model "$OPENAI_MODEL" \
  --output reports/observed-report.json
```

## Publication scorecard

The NextModel hosted bench page uses a publication scorecard. The Go runner can emit the same object:

```bash
go run ./cmd/benchmark \
  -scorecard publication \
  -observe \
  -model "$OPENAI_MODEL" \
  -promptset examples/promptset_v3.json \
  -decision-log reports/publication-decisions.jsonl
```

```bash
go run ./cmd/cachesafetybench run \
  --scorecard=publication \
  --observe \
  --model "$OPENAI_MODEL" \
  --promptset examples/promptset_v3.json
```

`-scorecard publication` requires `-observe`. It will not score the local lab pipeline as publication. NextModel remains optional: any gateway that returns the serve-mode headers can be measured. See [metrics.md](metrics.md) for the formula.

## What observation is not

- It is not a rewrite of the local exact / canonical / semantic pipeline.
- It does not require NextModel. Any OpenAI-compatible gateway that returns the headers above can be observed.
- Gateways that omit `x-nextmodel-serve-mode` are scored as misses.
- Tests use fixtures and `httptest`. Do not commit API keys or call production from CI.
