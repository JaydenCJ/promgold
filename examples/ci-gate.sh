#!/usr/bin/env bash
# Demonstrates promgold as a CI gate: lock the v1 metrics surface, then
# check the refactored v2 exposition against it. Offline and idempotent.
#
# Usage: bash examples/ci-gate.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

BIN="$WORKDIR/promgold"
(cd "$ROOT" && go build -o "$BIN" ./cmd/promgold)

echo "== 1. lock the contract from the v1 exposition (runtime metrics ignored)"
"$BIN" snap --ignore 'go_*' --out "$WORKDIR/promgold.golden.json" \
  "$ROOT/examples/webapp-v1.metrics"

echo
echo "== 2. v1 against its own golden: values drift, shape holds, exit 0"
"$BIN" check --golden "$WORKDIR/promgold.golden.json" \
  "$ROOT/examples/webapp-v1.metrics"

echo
echo "== 3. v2 against the golden: the 'harmless refactor' fails the gate"
if "$BIN" check --golden "$WORKDIR/promgold.golden.json" \
  "$ROOT/examples/webapp-v2.metrics"; then
  echo "unexpected: v2 should have broken the contract" >&2
  exit 1
fi

echo
echo "== done: exit code 1 above is exactly what your CI should act on"
