# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Full parser for the Prometheus text exposition format and the
  OpenMetrics dialect: HELP/TYPE/UNIT metadata, escaped help text and
  label values, timestamps, exemplars, `# EOF`, and structural validation
  (`le`/`quantile` on the wrong metric type is rejected) with line-numbered
  errors.
- Family folding: histogram `_bucket`/`_sum`/`_count`, summary quantile
  series, OpenMetrics counter `_total`/`_created` children all collapse
  into their base family; declared-but-idle families stay in the contract.
- `snap` subcommand producing a deterministic, git-diff-friendly golden
  file (`promgold.golden.json`, schema_version 1): sorted families, label
  keys, numerically sorted bucket boundaries and quantiles, with `--pin`
  to lock a label's value set and `--ignore` patterns for runtime metrics.
- `check` subcommand comparing a live exposition (file, stdin, or
  http(s) URL) against the golden, reusing the capture options recorded
  in the golden; `--update` refreshes or bootstraps it.
- Severity-classified diff engine: breaking (removed metric, changed
  type or unit, removed label key, removed bucket/quantile/pinned value),
  risky (added label key, untyped gaining a type), and informational
  (added metric, added value, help change), with a `--fail-on` gate and
  exit code 1 on breach.
- `diff` subcommand comparing any two sources — expositions or golden
  files, sniffed by content.
- Three deterministic report formats: aligned text, stable JSON
  (`schema_version: 1`), and PR-ready Markdown tables.
- Runnable examples (`examples/webapp-v1.metrics`, `webapp-v2.metrics`,
  `ci-gate.sh`) and a golden-file reference (`docs/golden-format.md`).
- 90 deterministic offline tests (unit + in-process CLI integration,
  loopback-only HTTP) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/promgold/releases/tag/v0.1.0
