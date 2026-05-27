# Overview

CacheSafety Bench evaluates whether an old LLM response can be safely reused for a new request.

## What problem it addresses

Many cache discussions stop at hit rate. That is not enough for LLM APIs. Two prompts can look similar while the old answer is still wrong for the new request because of:

- dates and deadlines
- numbers, amounts, or quantities
- policy or region changes
- user-specific constraints
- semantic traps that look similar but require a different answer

## What this project is

- an open benchmark for safe response reuse
- a way to compare exact, canonical, and semantic layers
- a way to estimate cost savings under a strict Bad Hit Rate constraint

## What this project is not

- not a generic semantic cache
- not a model router
- not a promise that all traffic can be cached
- not a production gateway

## Non-goals

- blindly maximizing hit rate
- claiming that semantic caching always saves money
- supporting medical, legal, or financial reuse as safe benchmark defaults
- replacing product validation with marketing claims

## Positioning

Benchmarking comes before productization. The first question is not “how many hits can we get?” The first question is “which hits remain invisible to the user, and which ones turn into bad hits?”

