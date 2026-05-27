# Strategy interface

The benchmark runner stays fixed. A strategy decides whether an old answer should be reused.

## Minimal Go interface

```go
type Decision struct {
    Hit        bool
    Layer      string
    Confidence float64
    Reason     string
}

type Strategy interface {
    Decide(oldRequest cache.Request, oldAnswer string, newRequest cache.Request) Decision
}
```

## Design intent

- input: `old_request`, `old_answer`, `new_request`
- output: `hit`, `miss`, `layer`, `confidence`, `reason`
- benchmark runner: fixed and reportable
- strategy: replaceable and comparable

## Current public wrapper

The current `cachesafetybench run` wrapper is intentionally small. It documents strategy configuration through YAML while still leaning on the benchmark runner’s built-in conservative behavior.

