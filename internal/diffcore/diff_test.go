// Tests for change detection and severity classification — the table that
// decides whether CI goes red. Each case documents the operational reason
// for its severity.
package diffcore

import (
	"strings"
	"testing"

	"github.com/JaydenCJ/promgold/internal/contract"
	"github.com/JaydenCJ/promgold/internal/expfmt"
)

func build(t *testing.T, exposition string, opts contract.Options) contract.Contract {
	t.Helper()
	fams, err := expfmt.Parse(strings.NewReader(exposition))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return contract.Build(fams, opts)
}

// one asserts the result contains exactly one change and returns it.
func one(t *testing.T, res Result) Change {
	t.Helper()
	if len(res.Changes) != 1 {
		t.Fatalf("want exactly 1 change, got %d: %+v", len(res.Changes), res.Changes)
	}
	return res.Changes[0]
}

func TestIdenticalContractsProduceNoChanges(t *testing.T) {
	in := "# TYPE up gauge\nup 1\n"
	res := Diff(build(t, in, contract.Options{}), build(t, in, contract.Options{}))
	if len(res.Changes) != 0 || res.Breaking+res.Risky+res.Info != 0 {
		t.Fatalf("unexpected changes: %+v", res.Changes)
	}
}

func TestRemovedMetricIsBreaking(t *testing.T) {
	// A dashboard panel over a removed metric renders "No data" forever.
	old := build(t, "# TYPE a gauge\na 1\n# TYPE b gauge\nb 1\n", contract.Options{})
	cur := build(t, "# TYPE a gauge\na 1\n", contract.Options{})
	c := one(t, Diff(old, cur))
	if c.Kind != MetricRemoved || c.Severity != Breaking || c.Metric != "b" {
		t.Fatalf("got %+v", c)
	}
}

func TestAddedMetricIsInfo(t *testing.T) {
	old := build(t, "# TYPE a gauge\na 1\n", contract.Options{})
	cur := build(t, "# TYPE a gauge\na 1\n# TYPE b counter\nb_total 1\n", contract.Options{})
	c := one(t, Diff(old, cur))
	if c.Kind != MetricAdded || c.Severity != Info {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Detail, "counter") {
		t.Fatalf("detail should name the new type: %q", c.Detail)
	}
}

func TestTypeChangeIsBreaking(t *testing.T) {
	// rate() over a gauge, or histogram_quantile() over a counter, silently
	// produces garbage — worse than an error.
	old := build(t, "# TYPE a counter\na_total 1\n", contract.Options{})
	cur := build(t, "# TYPE a gauge\na 1\n", contract.Options{})
	res := Diff(old, cur)
	var found bool
	for _, c := range res.Changes {
		if c.Kind == TypeChanged {
			found = true
			if c.Severity != Breaking || !strings.Contains(c.Detail, "counter -> gauge") {
				t.Fatalf("got %+v", c)
			}
		}
	}
	if !found {
		t.Fatalf("no type-changed reported: %+v", res.Changes)
	}
}

func TestUntypedGainingTypeIsRisky(t *testing.T) {
	// Existing queries keep matching; the golden just needs a deliberate
	// refresh, so this must not hard-fail a default gate.
	old := build(t, "a 1\n", contract.Options{})
	cur := build(t, "# TYPE a gauge\na 1\n", contract.Options{})
	c := one(t, Diff(old, cur))
	if c.Kind != TypeChanged || c.Severity != Risky {
		t.Fatalf("got %+v", c)
	}
}

func TestRemovedLabelIsBreaking(t *testing.T) {
	// sum by (code) (...) returns nothing once code disappears.
	old := build(t, `# TYPE a counter`+"\n"+`a_total{code="200"} 1`+"\n", contract.Options{})
	cur := build(t, "# TYPE a counter\na_total 1\n", contract.Options{})
	c := one(t, Diff(old, cur))
	if c.Kind != LabelRemoved || c.Severity != Breaking || !strings.Contains(c.Detail, `"code"`) {
		t.Fatalf("got %+v", c)
	}
}

func TestAddedLabelIsRisky(t *testing.T) {
	// New label keys split every existing series: sums and averages change
	// value without any query erroring out.
	old := build(t, "# TYPE a counter\na_total 1\n", contract.Options{})
	cur := build(t, `# TYPE a counter`+"\n"+`a_total{tenant="x"} 1`+"\n", contract.Options{})
	c := one(t, Diff(old, cur))
	if c.Kind != LabelAdded || c.Severity != Risky || !strings.Contains(c.Detail, `"tenant"`) {
		t.Fatalf("got %+v", c)
	}
}

func TestRemovedHistogramBucketIsBreaking(t *testing.T) {
	// Recording rules pinned to le="0.5" go stale silently.
	oldExp := `# TYPE h histogram
h_bucket{le="0.5"} 1
h_bucket{le="+Inf"} 2
h_sum 1
h_count 2
`
	curExp := `# TYPE h histogram
h_bucket{le="+Inf"} 2
h_sum 1
h_count 2
`
	c := one(t, Diff(build(t, oldExp, contract.Options{}), build(t, curExp, contract.Options{})))
	if c.Kind != ValueRemoved || c.Severity != Breaking {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Detail, `histogram bucket le="0.5" removed`) {
		t.Fatalf("detail should name the bucket: %q", c.Detail)
	}
}

func TestAddedBucketIsInfo(t *testing.T) {
	oldExp := "# TYPE h histogram\n" + `h_bucket{le="+Inf"} 2` + "\nh_sum 1\nh_count 2\n"
	curExp := "# TYPE h histogram\n" + `h_bucket{le="0.1"} 1` + "\n" + `h_bucket{le="+Inf"} 2` + "\nh_sum 1\nh_count 2\n"
	c := one(t, Diff(build(t, oldExp, contract.Options{}), build(t, curExp, contract.Options{})))
	if c.Kind != ValueAdded || c.Severity != Info {
		t.Fatalf("got %+v", c)
	}
}

func TestRemovedQuantileIsBreaking(t *testing.T) {
	oldExp := "# TYPE s summary\n" + `s{quantile="0.99"} 1` + "\ns_sum 1\ns_count 1\n"
	curExp := "# TYPE s summary\ns_sum 1\ns_count 1\n"
	c := one(t, Diff(build(t, oldExp, contract.Options{}), build(t, curExp, contract.Options{})))
	if c.Kind != ValueRemoved || c.Severity != Breaking || !strings.Contains(c.Detail, "summary quantile") {
		t.Fatalf("got %+v", c)
	}
}

func TestRemovedPinnedValueIsBreaking(t *testing.T) {
	// The team pinned `code` because alerts match code="500" literally.
	opts := contract.Options{Pin: []string{"code"}}
	old := build(t, `# TYPE a counter`+"\n"+`a_total{code="200"} 1`+"\n"+`a_total{code="500"} 1`+"\n", opts)
	cur := build(t, `# TYPE a counter`+"\n"+`a_total{code="200"} 1`+"\n", opts)
	c := one(t, Diff(old, cur))
	if c.Kind != ValueRemoved || c.Severity != Breaking || !strings.Contains(c.Detail, `pinned value code="500"`) {
		t.Fatalf("got %+v", c)
	}
}

func TestHelpChangeIsInfo(t *testing.T) {
	old := build(t, "# HELP a Old text.\n# TYPE a gauge\na 1\n", contract.Options{})
	cur := build(t, "# HELP a New text.\n# TYPE a gauge\na 1\n", contract.Options{})
	c := one(t, Diff(old, cur))
	if c.Kind != HelpChanged || c.Severity != Info {
		t.Fatalf("got %+v", c)
	}
}

func TestUnitChangeIsBreaking(t *testing.T) {
	old := build(t, "# TYPE d gauge\n# UNIT d seconds\nd 1\n", contract.Options{})
	cur := build(t, "# TYPE d gauge\n# UNIT d milliseconds\nd 1\n", contract.Options{})
	c := one(t, Diff(old, cur))
	if c.Kind != UnitChanged || c.Severity != Breaking {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Detail, "seconds -> milliseconds") {
		t.Fatalf("detail should show both units: %q", c.Detail)
	}
}

func TestChangesSortedWorstFirstThenByMetric(t *testing.T) {
	old := build(t, "# TYPE a gauge\na 1\n# TYPE z gauge\nz 1\n", contract.Options{})
	cur := build(t, "# HELP a changed\n# TYPE a gauge\na 1\n# TYPE m counter\nm_total 1\n", contract.Options{})
	res := Diff(old, cur)
	if len(res.Changes) != 3 {
		t.Fatalf("want 3 changes, got %+v", res.Changes)
	}
	if res.Changes[0].Kind != MetricRemoved { // breaking first
		t.Fatalf("order wrong: %+v", res.Changes)
	}
	if res.Changes[1].Metric != "a" || res.Changes[2].Metric != "m" { // then metric asc
		t.Fatalf("order wrong: %+v", res.Changes)
	}
}

func TestCountersMatchChangeList(t *testing.T) {
	old := build(t, "# TYPE a counter\na_total 1\n# TYPE b gauge\nb 1\n", contract.Options{})
	cur := build(t, `# TYPE a counter`+"\n"+`a_total{region="eu"} 1`+"\n# TYPE c gauge\nc 1\n", contract.Options{})
	res := Diff(old, cur)
	if res.Breaking != 1 || res.Risky != 1 || res.Info != 1 {
		t.Fatalf("counters wrong: breaking=%d risky=%d info=%d (%+v)",
			res.Breaking, res.Risky, res.Info, res.Changes)
	}
}

func TestAtOrAboveThresholds(t *testing.T) {
	res := Result{Changes: []Change{
		{Severity: Breaking}, {Severity: Risky}, {Severity: Info},
	}}
	if res.AtOrAbove(Breaking) != 1 || res.AtOrAbove(Risky) != 2 || res.AtOrAbove(Info) != 3 {
		t.Fatalf("thresholds wrong: %d %d %d",
			res.AtOrAbove(Breaking), res.AtOrAbove(Risky), res.AtOrAbove(Info))
	}
}

func TestSeverityNamesRoundTrip(t *testing.T) {
	for name, want := range map[string]Severity{"breaking": Breaking, "risky": Risky, "info": Info} {
		got, ok := ParseSeverity(name)
		if !ok || got != want || got.String() != name {
			t.Fatalf("ParseSeverity(%q) = %v, %v", name, got, ok)
		}
	}
	if _, ok := ParseSeverity("fatal"); ok {
		t.Fatalf("unknown severity must not parse")
	}
}

func TestFamilyCountsReported(t *testing.T) {
	old := build(t, "a 1\nb 1\n", contract.Options{})
	cur := build(t, "a 1\n", contract.Options{})
	res := Diff(old, cur)
	if res.FamiliesGolden != 2 || res.FamiliesCurrent != 1 {
		t.Fatalf("family counts wrong: %+v", res)
	}
}
