# promgold examples

Everything here is offline and self-contained.

## webapp-v1.metrics / webapp-v2.metrics

Two captures of the same web service's `/metrics` endpoint. v2 is a
"harmless metrics refactor" that actually hides three breaking changes and
two risky ones: the `code` label was renamed to `status_code`, the
`queue_depth` gauge was dropped, the `le="0.5"` histogram bucket vanished,
and a `tenant` label now splits every request series.

```bash
promgold diff examples/webapp-v1.metrics examples/webapp-v2.metrics
```

## ci-gate.sh

The full CI shape: snap the v1 surface into a golden file, verify that
value drift alone passes, then watch v2 fail the gate with exit code 1.

```bash
bash examples/ci-gate.sh
```

Both expositions are plain files, so the run is identical on every machine.
Against a real service you would snap and check `http://127.0.0.1:PORT/metrics`
instead of a file path.
