#!/usr/bin/env bash
set -euo pipefail

count="${1:-1}"
cd "$(dirname "$0")/.."
exec go test ./internal/benchmark -run PublicationE2E -count="$count"
