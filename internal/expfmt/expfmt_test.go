// Tests for the exposition parser: sample syntax, escaping, metadata
// comments, family folding for histograms/summaries/counters, and the
// error positions a CI log depends on.
package expfmt

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func parse(t *testing.T, in string) []Family {
	t.Helper()
	fams, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return fams
}

func parseErr(t *testing.T, in string) *ParseError {
	t.Helper()
	_, err := Parse(strings.NewReader(in))
	if err == nil {
		t.Fatalf("Parse: expected error, got none")
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Parse: expected *ParseError, got %T: %v", err, err)
	}
	return pe
}

func family(t *testing.T, fams []Family, name string) Family {
	t.Helper()
	for _, f := range fams {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("family %q not found in %d families", name, len(fams))
	return Family{}
}

func TestParseBareSampleWithoutLabels(t *testing.T) {
	fams := parse(t, "up 1\n")
	f := family(t, fams, "up")
	if len(f.Samples) != 1 || f.Samples[0].Value != 1 {
		t.Fatalf("unexpected samples: %+v", f.Samples)
	}
	if f.Type != "untyped" {
		t.Fatalf("bare sample should default to untyped, got %q", f.Type)
	}
}

func TestParseSampleWithLabels(t *testing.T) {
	fams := parse(t, `http_requests_total{method="get",code="200"} 1027`+"\n")
	s := family(t, fams, "http_requests_total").Samples[0]
	if len(s.Labels) != 2 {
		t.Fatalf("want 2 labels, got %+v", s.Labels)
	}
	if s.Labels[0] != (Label{"method", "get"}) || s.Labels[1] != (Label{"code", "200"}) {
		t.Fatalf("labels parsed wrong: %+v", s.Labels)
	}
}

func TestParseMetadataComments(t *testing.T) {
	in := "# HELP up Whether the target is up.\n# TYPE up gauge\nup 1\n" +
		"# TYPE request_seconds gauge\n# UNIT request_seconds seconds\nrequest_seconds 0.1\n"
	fams := parse(t, in)
	up := family(t, fams, "up")
	if up.Type != "gauge" || up.Help != "Whether the target is up." {
		t.Fatalf("HELP/TYPE not captured: %+v", up)
	}
	if f := family(t, fams, "request_seconds"); f.Unit != "seconds" {
		t.Fatalf("UNIT not captured: %+v", f)
	}
}

func TestHelpEscapesResolved(t *testing.T) {
	in := `# HELP m Line one\nline two \\ backslash` + "\n# TYPE m gauge\nm 1\n"
	f := family(t, parse(t, in), "m")
	if f.Help != "Line one\nline two \\ backslash" {
		t.Fatalf("help unescaped wrong: %q", f.Help)
	}
}

func TestLabelValueEscapesResolved(t *testing.T) {
	in := `m{path="C:\\dir",msg="say \"hi\"",nl="a\nb"} 1` + "\n"
	s := family(t, parse(t, in), "m").Samples[0]
	want := []Label{{"path", `C:\dir`}, {"msg", `say "hi"`}, {"nl", "a\nb"}}
	for i, l := range s.Labels {
		if l != want[i] {
			t.Fatalf("label %d = %+v, want %+v", i, l, want[i])
		}
	}
}

func TestLabelValueMayContainCommaAndBraces(t *testing.T) {
	in := `m{q="a,b={c}"} 1` + "\n"
	s := family(t, parse(t, in), "m").Samples[0]
	if s.Labels[0].Value != "a,b={c}" {
		t.Fatalf("got %q", s.Labels[0].Value)
	}
}

func TestLabelBlockEdgeCases(t *testing.T) {
	// The Prometheus text format explicitly allows a trailing comma, and an
	// empty {} block is how some client libraries expose label-less series.
	fams := parse(t, `m{a="1",} 2`+"\n")
	if got := family(t, fams, "m").Samples[0].Labels[0]; got != (Label{"a", "1"}) {
		t.Fatalf("trailing comma: got %+v", got)
	}
	fams = parse(t, "m{} 2\n")
	if n := len(family(t, fams, "m").Samples[0].Labels); n != 0 {
		t.Fatalf("empty block: want no labels, got %d", n)
	}
}

func TestSpecialFloatValues(t *testing.T) {
	in := "a +Inf\nb -Inf\nc NaN\nd 1.7e+308\n"
	fams := parse(t, in)
	if v := family(t, fams, "a").Samples[0].Value; !math.IsInf(v, 1) {
		t.Fatalf("a = %v, want +Inf", v)
	}
	if v := family(t, fams, "b").Samples[0].Value; !math.IsInf(v, -1) {
		t.Fatalf("b = %v, want -Inf", v)
	}
	if v := family(t, fams, "c").Samples[0].Value; !math.IsNaN(v) {
		t.Fatalf("c = %v, want NaN", v)
	}
}

func TestTimestampAndExemplarDiscarded(t *testing.T) {
	// Timestamps and OpenMetrics exemplars after the value must not confuse
	// parsing — they are readings metadata, never contract surface.
	fams := parse(t, "m 42 1712345678901\n")
	if v := family(t, fams, "m").Samples[0].Value; v != 42 {
		t.Fatalf("timestamp: value = %v", v)
	}
	fams = parse(t, `m_total 5 # {trace_id="abc"} 1.0 1712345678`+"\n")
	if v := family(t, fams, "m_total").Samples[0].Value; v != 5 {
		t.Fatalf("exemplar: value = %v", v)
	}
}

func TestHistogramSeriesFoldIntoBaseFamily(t *testing.T) {
	in := `# TYPE http_request_duration_seconds histogram
http_request_duration_seconds_bucket{le="0.1"} 100
http_request_duration_seconds_bucket{le="+Inf"} 120
http_request_duration_seconds_sum 9.7
http_request_duration_seconds_count 120
`
	fams := parse(t, in)
	if len(fams) != 1 {
		t.Fatalf("want 1 folded family, got %d: %+v", len(fams), fams)
	}
	f := fams[0]
	if f.Name != "http_request_duration_seconds" || f.Type != "histogram" || len(f.Samples) != 4 {
		t.Fatalf("fold wrong: %+v", f)
	}
}

func TestSummarySeriesFoldIntoBaseFamily(t *testing.T) {
	in := `# TYPE rpc_duration_seconds summary
rpc_duration_seconds{quantile="0.5"} 0.05
rpc_duration_seconds{quantile="0.99"} 0.3
rpc_duration_seconds_sum 88.2
rpc_duration_seconds_count 1200
`
	fams := parse(t, in)
	if len(fams) != 1 || fams[0].Type != "summary" || len(fams[0].Samples) != 4 {
		t.Fatalf("fold wrong: %+v", fams)
	}
}

func TestOpenMetricsCounterTotalFolds(t *testing.T) {
	in := "# TYPE requests counter\nrequests_total 7\nrequests_created 1712345678\n"
	fams := parse(t, in)
	if len(fams) != 1 || fams[0].Name != "requests" || len(fams[0].Samples) != 2 {
		t.Fatalf("counter fold wrong: %+v", fams)
	}
}

func TestSuffixFoldRequiresMatchingType(t *testing.T) {
	// A gauge does not own `_bucket` children: the suffixed series is its
	// own family, because for gauges the suffix is part of the real name.
	in := "# TYPE queue gauge\nqueue 3\nqueue_bucket 9\n"
	fams := parse(t, in)
	if len(fams) != 2 {
		t.Fatalf("want 2 families, got %+v", fams)
	}
	family(t, fams, "queue_bucket")
}

func TestDeclaredButIdleFamilyKept(t *testing.T) {
	// A metric that exposes no series right now is still contract surface.
	in := "# HELP errs Errors seen.\n# TYPE errs counter\n"
	fams := parse(t, in)
	f := family(t, fams, "errs")
	if len(f.Samples) != 0 || f.Type != "counter" {
		t.Fatalf("idle family wrong: %+v", f)
	}
}

func TestFamiliesKeepFirstAppearanceOrder(t *testing.T) {
	in := "# TYPE b gauge\nb 1\n# TYPE a gauge\na 1\n"
	fams := parse(t, in)
	if fams[0].Name != "b" || fams[1].Name != "a" {
		t.Fatalf("order not preserved: %+v", fams)
	}
}

func TestNoiseLinesIgnoredAndEOFAccepted(t *testing.T) {
	fams := parse(t, "\n# just a note\n\nm 1\n   \n")
	if len(fams) != 1 {
		t.Fatalf("blank/comment lines: want 1 family, got %+v", fams)
	}
	fams = parse(t, "m 1\n# EOF\n")
	if len(fams) != 1 {
		t.Fatalf("EOF terminator: want 1 family, got %+v", fams)
	}
}

func TestContentAfterEOFRejected(t *testing.T) {
	pe := parseErr(t, "m 1\n# EOF\nn 2\n")
	if pe.Line != 3 {
		t.Fatalf("error line = %d, want 3", pe.Line)
	}
}

func TestUnknownTypeRejected(t *testing.T) {
	pe := parseErr(t, "# TYPE m distribution\n")
	if !strings.Contains(pe.Error(), "unknown metric type") {
		t.Fatalf("wrong error: %v", pe)
	}
}

func TestTypeRedeclarationRejected(t *testing.T) {
	pe := parseErr(t, "# TYPE m gauge\n# TYPE m counter\n")
	if pe.Line != 2 || !strings.Contains(pe.Msg, "redeclared") {
		t.Fatalf("wrong error: %+v", pe)
	}
}

func TestInvalidMetricNameRejectedWithLineNumber(t *testing.T) {
	pe := parseErr(t, "ok 1\n9bad 2\n")
	if pe.Line != 2 || !strings.Contains(pe.Msg, "invalid metric name") {
		t.Fatalf("wrong error: %+v", pe)
	}
}

func TestMalformedLabelBlocksRejected(t *testing.T) {
	cases := []struct {
		name, in, wantMsg string
	}{
		{"invalid label name", `m{9x="1"} 2` + "\n", "invalid label name"},
		{"duplicate label", `m{a="1",a="2"} 3` + "\n", "duplicate label"},
		{"unterminated value", `m{a="1} 2` + "\n", "unterminated label value"},
		{"missing equals", `m{a} 2` + "\n", "expected '='"},
		{"bad escape", `m{a="\q"} 2` + "\n", "invalid escape"},
	}
	for _, tc := range cases {
		pe := parseErr(t, tc.in)
		if !strings.Contains(pe.Msg, tc.wantMsg) {
			t.Errorf("%s: got %q, want substring %q", tc.name, pe.Msg, tc.wantMsg)
		}
	}
}

func TestBadSampleValuesRejected(t *testing.T) {
	if pe := parseErr(t, "m\n"); !strings.Contains(pe.Msg, "missing sample value") {
		t.Fatalf("missing value: wrong error: %+v", pe)
	}
	if pe := parseErr(t, "m fast\n"); !strings.Contains(pe.Msg, "invalid sample value") {
		t.Fatalf("garbage value: wrong error: %+v", pe)
	}
	if pe := parseErr(t, "m 1 2 3\n"); !strings.Contains(pe.Msg, "trailing tokens") {
		t.Fatalf("extra tokens: wrong error: %+v", pe)
	}
}

func TestStructuralLabelOnWrongTypeRejected(t *testing.T) {
	// A quantile label outside a summary, or le outside a histogram, means
	// the exposition lies about its types — refuse to snapshot a lie.
	pe := parseErr(t, "# TYPE m counter\n"+`m_total{quantile="0.5"} 1`+"\n")
	if !strings.Contains(pe.Msg, `"quantile"`) {
		t.Fatalf("wrong error: %+v", pe)
	}
	pe = parseErr(t, "# TYPE m gauge\n"+`m{le="0.5"} 1`+"\n")
	if !strings.Contains(pe.Msg, `"le"`) {
		t.Fatalf("wrong error: %+v", pe)
	}
}
