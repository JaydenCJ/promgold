# The promgold golden file

`promgold snap` writes the contract to `promgold.golden.json` (schema
version 1). It is stable, sorted JSON designed to live in git and produce
small, reviewable diffs when the metrics surface changes on purpose.

```json
{
  "tool": "promgold",
  "schema_version": 1,
  "pinned": ["code"],
  "ignored": ["go_*"],
  "families": [
    {
      "name": "http_request_duration_seconds",
      "type": "histogram",
      "help": "HTTP request latency.",
      "labels": [],
      "values": {
        "le": ["0.1", "0.5", "1", "+Inf"]
      }
    },
    {
      "name": "http_requests_total",
      "type": "counter",
      "help": "Total HTTP requests served.",
      "labels": ["code", "method"],
      "values": {
        "code": ["200", "404", "500"]
      }
    }
  ]
}
```

## Fields

| Field | Meaning |
|---|---|
| `tool` | Always `promgold`; `check` refuses files written by anything else. |
| `schema_version` | Golden layout version. This release reads and writes `1`. |
| `pinned` | Label names whose value sets are part of the contract. Recorded at snap time so `check` reproduces the exact same view. |
| `ignored` | Metric-name patterns excluded from the contract (`*` wildcard). |
| `families[].name` | Metric family name. Histogram `_bucket`/`_sum`/`_count` and summary quantile series fold into their base family. |
| `families[].type` | `counter`, `gauge`, `histogram`, `summary`, `untyped`, or an OpenMetrics type. |
| `families[].help` | HELP text, escapes resolved. Changes are informational. |
| `families[].unit` | OpenMetrics `# UNIT`, when present. Changes are breaking. |
| `families[].labels` | Sorted union of label keys across all series, excluding the structural `le` and `quantile`. |
| `families[].values` | Enumerated value sets: always `le` (bucket boundaries) and `quantile`, plus any pinned label. Sorted numerically for structural labels, `+Inf` last. |

## What is deliberately NOT stored

- **Sample values and timestamps** — a contract locks shape, not readings.
- **Unpinned label values** — enumerating `instance` or `pod` values would
  make every deploy a diff. Pin a label only when alerts match its values
  literally (`code="500"`).

## Determinism

`Marshal` produces byte-identical output for equal contracts: families
sorted by name, label keys sorted, two-space indent, trailing newline.
Re-snapping an unchanged endpoint yields a zero-line git diff.
