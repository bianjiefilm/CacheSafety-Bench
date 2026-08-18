# Changelog

## Unreleased

- Added a publication scorecard (`-scorecard publication`) that matches the NextModel hosted bench formula on promptset v3. Lab scorecard and in-process simulation stay the default. No billing, pricing, or settlement changes.
- Added an optional OpenAI-compatible provider and `-observe` mode so the Go runner can score a live gateway's `x-nextmodel-serve-mode` without replacing the in-process cache simulation.
- Bootstrapped the public CacheSafety Bench repository.
- Added the `cachesafetybench run` wrapper command.
- Added toy datasets, docs, GitHub templates, and minimal Go CI.

