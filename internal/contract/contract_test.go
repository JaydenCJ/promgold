// Tests for contract building: determinism, structural-value tracking,
// pinning, ignore patterns, and golden-file round-tripping.
package contract

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JaydenCJ/promgold/internal/expfmt"
)

func mustParse(t *testing.T, in string) []expfmt.Family {
	t.Helper()
	fams, err := expfmt.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return fams
}

const webApp = `# HELP http_requests_total Requests served.
# TYPE http_requests_total counter
http_requests_total{method="get",code="200"} 100
http_requests_total{method="post",code="500"} 3
# TYPE http_request_duration_seconds histogram
http_request_duration_seconds_bucket{method="get",le="0.1"} 90
http_request_duration_seconds_bucket{method="get",le="0.5"} 99
http_request_duration_seconds_bucket{method="get",le="+Inf"} 100
http_request_duration_seconds_sum{method="get"} 4.2
http_request_duration_seconds_count{method="get"} 100
# TYPE go_goroutines gauge
go_goroutines 12
`

func find(t *testing.T, c Contract, name string) Family {
	t.Helper()
	f, ok := c.Family(name)
	if !ok {
		t.Fatalf("family %q missing from contract", name)
	}
	return f
}

func TestBuildCollectsLabelKeysAcrossSeries(t *testing.T) {
	c := Build(mustParse(t, webApp), Options{})
	f := find(t, c, "http_requests_total")
	if len(f.Labels) != 2 || f.Labels[0] != "code" || f.Labels[1] != "method" {
		t.Fatalf("labels = %v, want [code method]", f.Labels)
	}
}

func TestBuildFamiliesSortedByName(t *testing.T) {
	c := Build(mustParse(t, webApp), Options{})
	for i := 1; i < len(c.Families); i++ {
		if c.Families[i-1].Name >= c.Families[i].Name {
			t.Fatalf("families not sorted: %q >= %q", c.Families[i-1].Name, c.Families[i].Name)
		}
	}
}

func TestBucketsTrackedAndLeExcludedFromLabels(t *testing.T) {
	c := Build(mustParse(t, webApp), Options{})
	f := find(t, c, "http_request_duration_seconds")
	if len(f.Labels) != 1 || f.Labels[0] != "method" {
		t.Fatalf("le must not appear as a plain label: %v", f.Labels)
	}
	got := f.Values["le"]
	want := []string{"0.1", "0.5", "+Inf"}
	if len(got) != len(want) {
		t.Fatalf("buckets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("buckets = %v, want %v (numeric order, +Inf last)", got, want)
		}
	}
}

func TestQuantilesTrackedNumerically(t *testing.T) {
	in := `# TYPE lat summary
lat{quantile="0.99"} 1
lat{quantile="0.5"} 1
lat{quantile="0.9"} 1
lat_sum 1
lat_count 1
`
	f := find(t, Build(mustParse(t, in), Options{}), "lat")
	got := f.Values["quantile"]
	if len(got) != 3 || got[0] != "0.5" || got[1] != "0.9" || got[2] != "0.99" {
		t.Fatalf("quantiles = %v", got)
	}
}

func TestPinnedLabelValuesEnumerated(t *testing.T) {
	c := Build(mustParse(t, webApp), Options{Pin: []string{"code"}})
	f := find(t, c, "http_requests_total")
	got := f.Values["code"]
	if len(got) != 2 || got[0] != "200" || got[1] != "500" {
		t.Fatalf("pinned values = %v, want [200 500]", got)
	}
	// Pinned labels stay in the labels list — they are still real keys.
	if len(f.Labels) != 2 {
		t.Fatalf("pinning must not remove the label key: %v", f.Labels)
	}
	// Without the pin, values stay out of the contract: enumerating
	// arbitrary label values would make every deploy a diff.
	unpinned := find(t, Build(mustParse(t, webApp), Options{}), "http_requests_total")
	if unpinned.Values != nil {
		t.Fatalf("unpinned label values must not be stored, got %v", unpinned.Values)
	}
}

func TestIgnorePatternDropsFamilies(t *testing.T) {
	c := Build(mustParse(t, webApp), Options{Ignore: []string{"go_*"}})
	if _, ok := c.Family("go_goroutines"); ok {
		t.Fatalf("go_goroutines should be ignored")
	}
	if len(c.Families) != 2 {
		t.Fatalf("want 2 families, got %d", len(c.Families))
	}
}

func TestMatchStarPatterns(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"go", "go_goroutines", false}, // no wildcard = full-name match only
		{"go", "go", true},
		{"*_seconds", "http_request_duration_seconds", true},
		{"http_*_seconds", "http_request_duration_seconds", true},
		{"http_*_seconds", "http_requests_total", false},
		{"*", "anything", true},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "aXcYb", false},
	}
	for _, tc := range cases {
		if got := matchStar(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchStar(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestOptionsRecordedNormalized(t *testing.T) {
	c := Build(nil, Options{Pin: []string{"code", "code", " method "}, Ignore: []string{"go_*", ""}})
	if len(c.Pinned) != 2 || c.Pinned[0] != "code" || c.Pinned[1] != "method" {
		t.Fatalf("pinned = %v", c.Pinned)
	}
	if len(c.Ignored) != 1 || c.Ignored[0] != "go_*" {
		t.Fatalf("ignored = %v", c.Ignored)
	}
}

func TestMarshalIsByteDeterministic(t *testing.T) {
	fams := mustParse(t, webApp)
	a, err := Marshal(Build(fams, Options{Pin: []string{"code"}}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	b, err := Marshal(Build(mustParse(t, webApp), Options{Pin: []string{"code"}}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("marshal not deterministic:\n%s\n---\n%s", a, b)
	}
	if !bytes.HasSuffix(a, []byte("\n")) {
		t.Fatalf("golden file must end with a newline")
	}
}

func TestMarshalLoadRoundTrip(t *testing.T) {
	orig := Build(mustParse(t, webApp), Options{Pin: []string{"code"}, Ignore: []string{"noop_*"}})
	data, err := Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	back, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	again, err := Marshal(back)
	if err != nil {
		t.Fatalf("Marshal(back): %v", err)
	}
	if !bytes.Equal(data, again) {
		t.Fatalf("round trip changed bytes:\n%s\n---\n%s", data, again)
	}
}

func TestLoadRejectsBadGoldenFiles(t *testing.T) {
	cases := []struct {
		name, data, wantErr string
	}{
		{"foreign tool", `{"tool":"other","schema_version":1,"families":[]}`, "not written by promgold"},
		{"future schema", `{"tool":"promgold","schema_version":99,"families":[]}`, "schema_version 99"},
		{"not json", "# HELP not json\n", "not valid JSON"},
		{"nameless family", `{"tool":"promgold","schema_version":1,"families":[{"type":"gauge"}]}`, "no name"},
	}
	for _, tc := range cases {
		_, err := Load([]byte(tc.data))
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want substring %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestLooksLikeGoldenSniffing(t *testing.T) {
	if !LooksLikeGolden([]byte("  \n\t{\"tool\":\"promgold\"}")) {
		t.Fatalf("JSON object should sniff as golden")
	}
	if LooksLikeGolden([]byte("# HELP up ...\nup 1\n")) {
		t.Fatalf("exposition text must not sniff as golden")
	}
}

func TestEmptyExpositionYieldsEmptyContract(t *testing.T) {
	c := Build(mustParse(t, ""), Options{})
	if len(c.Families) != 0 || c.Tool != "promgold" || c.SchemaVersion != SchemaVersion {
		t.Fatalf("empty contract wrong: %+v", c)
	}
}
