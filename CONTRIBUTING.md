# Contributing to promgold

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else — no runtime dependencies, no services.

```bash
git clone https://github.com/JaydenCJ/promgold && cd promgold
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, locks the example exposition into a
golden file, and asserts on real CLI output across every subcommand,
format, and exit code; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (90 deterministic tests, no external network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (parsing, contract building, and diffing never touch the
   network — only `fetch` does, and only for user-named URLs).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in
  the PR.
- No network calls at startup and no telemetry, ever. The only outbound
  request promgold can make is a GET to a metrics URL the user typed.
- Severity classification is data: a new change kind gets one severity,
  a test documenting the operational reason, and a row in the README's
  rules table.
- The golden file format is an API. Changes to it bump `schema_version`
  and come with a documented migration in `docs/golden-format.md`.
- Code comments and doc comments are written in English.
- Determinism first: identical input must produce byte-identical goldens
  and reports, including all orderings.

## Reporting bugs

Include the output of `promgold version`, the full command you ran, the
report output, and — for parser issues — the smallest exposition snippet
that reproduces the problem (redact label values if needed), since that
is exactly what the parser sees.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
