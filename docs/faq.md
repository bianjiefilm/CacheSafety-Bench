# FAQ

## Is this a semantic cache?

No. It is a benchmark for safe LLM response reuse.

## Why not optimize hit rate?

Because a bad hit makes the model look wrong. Safe Hit Rate matters more than raw hit rate.

## What is a bad hit?

A bad hit is a reused answer that should not have been returned for the new request.

## Can I use this with OpenAI?

Yes, with any OpenAI-compatible endpoint.

## Can I use this with OpenRouter?

Yes, if your workflow is OpenAI-compatible.

## Can I use this with NextModel?

Yes. NextModel is an optional hosted endpoint example, not a requirement.

Local runs simulate cache in-process. To score what a NextModel-shaped gateway actually served, set `OPENAI_API_KEY` / `OPENAI_BASE_URL` and pass `-observe`. See [nextmodel-integration.md](nextmodel-integration.md).

## Does this store my prompts?

The public repo ships only toy examples. You should review your own local datasets and reports before sharing them.

## Is this safe for medical, legal, or financial use?

No default benchmark claim should treat those domains as safe reuse targets.

## How do I report a bad hit?

Open a `bad hit report` issue with a toy reproduction case and the generated report JSON.

## 中文说明

这是一个评估旧答案是否可以安全复用的 benchmark，不是承诺“都能缓存”的产品。

