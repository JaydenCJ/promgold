#!/usr/bin/env bash
# End-to-end smoke test for promgold: builds the binary, snapshots a
# realistic exposition, and asserts on the real CLI output of every
# subcommand, format, and exit code. No network, idempotent, finishes in
# seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/promgold"
GOLDEN="$WORKDIR/promgold.golden.json"
V1="$ROOT/examples/webapp-v1.metrics"
V2="$ROOT/examples/webapp-v2.metrics"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/promgold) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" version | grep -qx "promgold 0.1.0" || fail "version mismatch"

echo "3. snap locks the v1 surface"
OUT="$("$BIN" snap --ignore 'go_*' --pin code --out "$GOLDEN" "$V1")"
echo "$OUT" | grep -q "4 families locked" || fail "snap summary wrong: $OUT"
grep -q '"tool": "promgold"' "$GOLDEN" || fail "golden envelope missing"
grep -q '"http_request_duration_seconds"' "$GOLDEN" || fail "histogram family missing"
grep -q '"go_goroutines"' "$GOLDEN" && fail "ignored family leaked into golden"

echo "4. check passes when only values drift"
sed 's/ 10234$/ 999999/' "$V1" > "$WORKDIR/drift.metrics"
"$BIN" check --golden "$GOLDEN" "$WORKDIR/drift.metrics" \
  | grep -q "contract: OK — no changes" || fail "value drift should not fail"

echo "5. check catches the v2 refactor and exits 1"
set +e
OUT="$("$BIN" check --golden "$GOLDEN" "$V2")"
CODE=$?
set -e
[ "$CODE" -eq 1 ] || fail "check should exit 1 on breaking changes, got $CODE"
echo "$OUT" | grep -q 'BREAKING  queue_depth' || fail "removed metric not reported"
echo "$OUT" | grep -q 'label "code" removed' || fail "removed label not reported"
echo "$OUT" | grep -q 'histogram bucket le="0.5" removed' || fail "removed bucket not reported"
echo "$OUT" | grep -q 'new label "tenant"' || fail "added label not reported"
echo "$OUT" | grep -q 'contract: BROKEN' || fail "verdict missing"

echo "6. JSON report is machine-readable"
set +e
JSON="$("$BIN" check --golden "$GOLDEN" --format json "$V2")"
set -e
echo "$JSON" | grep -q '"broken": true' || fail "json verdict missing"
echo "$JSON" | grep -q '"kind": "metric-removed"' || fail "json change kinds missing"

echo "7. markdown report renders a table"
set +e
MD="$("$BIN" check --golden "$GOLDEN" --format markdown "$V2")"
set -e
echo "$MD" | grep -q '| Severity | Metric | Change |' || fail "markdown table missing"

echo "8. diff compares two expositions directly"
set +e
DIFFOUT="$("$BIN" diff "$V1" "$V2")"
set -e
echo "$DIFFOUT" | grep -q "metric no longer exposed" || fail "diff missed removal"

echo "9. stdin works as a source"
"$BIN" snap --out - - < "$V1" | grep -q '"queue_depth"' || fail "stdin snap failed"

echo "10. check --update refreshes the golden, then passes"
"$BIN" check --golden "$GOLDEN" --update "$V2" | grep -q "updated" || fail "--update failed"
"$BIN" check --golden "$GOLDEN" "$V2" | grep -q "contract: OK" || fail "post-update check failed"

echo "11. usage errors exit 2"
set +e
"$BIN" check --format yaml "$V1" >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad --format should exit 2"
"$BIN" snap >/dev/null 2>&1
[ $? -eq 2 ] || fail "missing source should exit 2"
set -e

echo "SMOKE OK"
