// In-process integration tests: every subcommand is driven through Run
// exactly as main invokes it, asserting on real stdout/stderr text and the
// exit codes the CI contract promises.
package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const expoV1 = `# HELP http_requests_total Requests served.
# TYPE http_requests_total counter
http_requests_total{method="get",code="200"} 100
http_requests_total{method="post",code="500"} 3
# HELP http_request_duration_seconds Request latency.
# TYPE http_request_duration_seconds histogram
http_request_duration_seconds_bucket{le="0.1"} 90
http_request_duration_seconds_bucket{le="0.5"} 99
http_request_duration_seconds_bucket{le="+Inf"} 100
http_request_duration_seconds_sum 4.2
http_request_duration_seconds_count 100
# HELP queue_depth Jobs waiting.
# TYPE queue_depth gauge
queue_depth 7
# TYPE go_goroutines gauge
go_goroutines 12
`

// expoV2 is v1 after a "harmless refactor": queue_depth was dropped, the
// 0.5 bucket disappeared, and requests grew a tenant label.
const expoV2 = `# HELP http_requests_total Requests served.
# TYPE http_requests_total counter
http_requests_total{method="get",code="200",tenant="a"} 60
# HELP http_request_duration_seconds Request latency.
# TYPE http_request_duration_seconds histogram
http_request_duration_seconds_bucket{le="0.1"} 90
http_request_duration_seconds_bucket{le="+Inf"} 100
http_request_duration_seconds_sum 4.2
http_request_duration_seconds_count 100
# TYPE go_goroutines gauge
go_goroutines 12
`

// run executes promgold in-process and returns exit code, stdout, stderr.
func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// write drops content into dir under name and returns the full path.
func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVersionSubcommandAndAliases(t *testing.T) {
	code, out, _ := run(t, "", "version")
	if code != ExitOK || out != "promgold 0.1.0\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	for _, alias := range []string{"--version", "-v"} {
		code, out, _ := run(t, "", alias)
		if code != ExitOK || !strings.Contains(out, "0.1.0") {
			t.Fatalf("%s: code=%d out=%q", alias, code, out)
		}
	}
}

func TestUsageSurface(t *testing.T) {
	code, out, _ := run(t, "", "help")
	if code != ExitOK || !strings.Contains(out, "promgold snap") {
		t.Fatalf("help: code=%d out=%q", code, out)
	}
	code, _, errb := run(t, "")
	if code != ExitUsage || !strings.Contains(errb, "Usage:") {
		t.Fatalf("no args: code=%d err=%q", code, errb)
	}
	code, _, errb = run(t, "", "freeze")
	if code != ExitUsage || !strings.Contains(errb, `unknown command "freeze"`) {
		t.Fatalf("unknown command: code=%d err=%q", code, errb)
	}
}

func TestSnapWritesGoldenFile(t *testing.T) {
	dir := t.TempDir()
	src := write(t, dir, "metrics.txt", expoV1)
	golden := filepath.Join(dir, "g.json")
	code, out, errb := run(t, "", "snap", "--out", golden, src)
	if code != ExitOK {
		t.Fatalf("code=%d err=%q", code, errb)
	}
	if !strings.Contains(out, "4 families locked") {
		t.Fatalf("summary wrong: %q", out)
	}
	data, err := os.ReadFile(golden)
	if err != nil || !strings.Contains(string(data), `"http_request_duration_seconds"`) {
		t.Fatalf("golden not written: %v\n%s", err, data)
	}
}

func TestSnapStdinToStdout(t *testing.T) {
	// The pipe form: `curl -s .../metrics | promgold snap --out - -`.
	code, out, _ := run(t, expoV1, "snap", "--out", "-", "-")
	if code != ExitOK || !strings.HasPrefix(out, "{") || !strings.Contains(out, `"tool": "promgold"`) {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if !strings.Contains(out, `"queue_depth"`) {
		t.Fatalf("family missing from golden:\n%s", out)
	}
}

func TestSnapRecordsPinAndIgnore(t *testing.T) {
	dir := t.TempDir()
	src := write(t, dir, "metrics.txt", expoV1)
	code, out, _ := run(t, "", "snap", "--out", "-", "--pin", "code", "--ignore", "go_*", src)
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	for _, want := range []string{`"pinned": [`, `"code"`, `"ignored": [`, `"go_*"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("golden missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "go_goroutines") {
		t.Fatalf("ignored family leaked into golden:\n%s", out)
	}
}

func TestSnapUsageErrors(t *testing.T) {
	code, _, errb := run(t, "", "snap")
	if code != ExitUsage || !strings.Contains(errb, "exactly one <source>") {
		t.Fatalf("no source: code=%d err=%q", code, errb)
	}
	code, _, errb = run(t, "", "snap", "--frob", "x")
	if code != ExitUsage || !strings.Contains(errb, "--frob") {
		t.Fatalf("unknown flag: code=%d err=%q", code, errb)
	}
	code, _, errb = run(t, "", "snap", "--timeout", "soon", "x.txt")
	if code != ExitUsage || !strings.Contains(errb, "--timeout") {
		t.Fatalf("bad timeout: code=%d err=%q", code, errb)
	}
}

func TestSnapInvalidExpositionIsRuntimeError(t *testing.T) {
	dir := t.TempDir()
	src := write(t, dir, "bad.txt", "up one\n")
	code, _, errb := run(t, "", "snap", "--out", "-", src)
	if code != ExitRuntime || !strings.Contains(errb, "line 1") {
		t.Fatalf("code=%d err=%q", code, errb)
	}
}

// snapTo captures expoV1 into a golden file and returns its path.
func snapTo(t *testing.T, dir string, extra ...string) string {
	t.Helper()
	src := write(t, dir, "v1.txt", expoV1)
	golden := filepath.Join(dir, "promgold.golden.json")
	args := append([]string{"snap", "--out", golden}, extra...)
	args = append(args, src)
	code, _, errb := run(t, "", args...)
	if code != ExitOK {
		t.Fatalf("snap failed: code=%d err=%q", code, errb)
	}
	return golden
}

func TestCheckPassesOnIdenticalSurface(t *testing.T) {
	dir := t.TempDir()
	golden := snapTo(t, dir)
	src := write(t, dir, "again.txt", expoV1)
	code, out, _ := run(t, "", "check", "--golden", golden, src)
	if code != ExitOK || !strings.Contains(out, "contract: OK — no changes") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckIgnoresValueDrift(t *testing.T) {
	// Same shape, different readings: the whole point of a shape contract.
	dir := t.TempDir()
	golden := snapTo(t, dir)
	drift := strings.ReplaceAll(expoV1, " 100", " 99999")
	src := write(t, dir, "drift.txt", drift)
	code, out, _ := run(t, "", "check", "--golden", golden, src)
	if code != ExitOK || !strings.Contains(out, "no changes") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckFailsOnBreakingChange(t *testing.T) {
	dir := t.TempDir()
	golden := snapTo(t, dir)
	src := write(t, dir, "v2.txt", expoV2)
	code, out, _ := run(t, "", "check", "--golden", golden, src)
	if code != ExitBroken {
		t.Fatalf("code=%d out=%q", code, out)
	}
	for _, want := range []string{
		"BREAKING  http_request_duration_seconds",
		`histogram bucket le="0.5" removed`,
		"BREAKING  queue_depth",
		"metric no longer exposed",
		`new label "tenant"`,
		"contract: BROKEN",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}

func TestCheckFailOnRiskyTightensGate(t *testing.T) {
	// Only a risky change (new label): default gate passes, risky gate fails.
	dir := t.TempDir()
	golden := snapTo(t, dir)
	withLabel := strings.ReplaceAll(expoV1,
		`http_requests_total{method="get",code="200"} 100`,
		`http_requests_total{method="get",code="200",tenant="a"} 100`)
	src := write(t, dir, "risky.txt", withLabel)
	code, _, _ := run(t, "", "check", "--golden", golden, src)
	if code != ExitOK {
		t.Fatalf("default gate should pass on risky-only, got %d", code)
	}
	code, out, _ := run(t, "", "check", "--golden", golden, "--fail-on", "risky", src)
	if code != ExitBroken || !strings.Contains(out, "contract: BROKEN") {
		t.Fatalf("risky gate should fail: code=%d out=%q", code, out)
	}
}

func TestCheckJSONFormat(t *testing.T) {
	dir := t.TempDir()
	golden := snapTo(t, dir)
	src := write(t, dir, "v2.txt", expoV2)
	code, out, _ := run(t, "", "check", "--golden", golden, "--format", "json", src)
	if code != ExitBroken {
		t.Fatalf("code=%d", code)
	}
	for _, want := range []string{`"tool": "promgold"`, `"broken": true`, `"kind": "metric-removed"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %q:\n%s", want, out)
		}
	}
}

func TestCheckMarkdownFormat(t *testing.T) {
	dir := t.TempDir()
	golden := snapTo(t, dir)
	src := write(t, dir, "v2.txt", expoV2)
	code, out, _ := run(t, "", "check", "--golden", golden, "--format", "markdown", src)
	if code != ExitBroken || !strings.Contains(out, "| Severity | Metric | Change |") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckReusesGoldenCaptureOptions(t *testing.T) {
	// The golden ignored go_* at snap time; check must not resurrect it.
	dir := t.TempDir()
	golden := snapTo(t, dir, "--ignore", "go_*", "--pin", "code")
	// Current exposition drops go_goroutines entirely AND drops code="500":
	// the former must stay invisible, the latter must break via the pin.
	cur := strings.ReplaceAll(expoV1, "# TYPE go_goroutines gauge\ngo_goroutines 12\n", "")
	cur = strings.ReplaceAll(cur, `http_requests_total{method="post",code="500"} 3`+"\n", "")
	src := write(t, dir, "cur.txt", cur)
	code, out, _ := run(t, "", "check", "--golden", golden, src)
	if code != ExitBroken {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if strings.Contains(out, "go_goroutines") {
		t.Fatalf("ignored metric resurfaced:\n%s", out)
	}
	if !strings.Contains(out, `pinned value code="500" removed`) {
		t.Fatalf("pinned value removal not detected:\n%s", out)
	}
}

func TestCheckMissingGoldenIsRuntimeError(t *testing.T) {
	dir := t.TempDir()
	src := write(t, dir, "v1.txt", expoV1)
	code, _, errb := run(t, "", "check", "--golden", filepath.Join(dir, "absent.json"), src)
	if code != ExitRuntime || !strings.Contains(errb, "run `promgold snap` first") {
		t.Fatalf("code=%d err=%q", code, errb)
	}
}

func TestCheckUpdateBootstrapsMissingGolden(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "g.json")
	src := write(t, dir, "v1.txt", expoV1)
	code, out, _ := run(t, "", "check", "--golden", golden, "--update", src)
	if code != ExitOK || !strings.Contains(out, "updated "+golden) {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if _, err := os.Stat(golden); err != nil {
		t.Fatalf("golden not created: %v", err)
	}
}

func TestCheckUpdateRewritesAndThenPasses(t *testing.T) {
	dir := t.TempDir()
	golden := snapTo(t, dir)
	src := write(t, dir, "v2.txt", expoV2)
	code, _, _ := run(t, "", "check", "--golden", golden, "--update", src)
	if code != ExitOK {
		t.Fatalf("update failed: %d", code)
	}
	code, out, _ := run(t, "", "check", "--golden", golden, src)
	if code != ExitOK || !strings.Contains(out, "no changes") {
		t.Fatalf("post-update check: code=%d out=%q", code, out)
	}
}

func TestCheckUsageErrors(t *testing.T) {
	code, _, errb := run(t, "", "check", "--fail-on", "fatal", "x.txt")
	if code != ExitUsage || !strings.Contains(errb, "--fail-on") {
		t.Fatalf("bad fail-on: code=%d err=%q", code, errb)
	}
	code, _, errb = run(t, "", "check", "--format", "yaml", "x.txt")
	if code != ExitUsage || !strings.Contains(errb, "--format") {
		t.Fatalf("bad format: code=%d err=%q", code, errb)
	}
}

func TestDiffTwoExpositions(t *testing.T) {
	dir := t.TempDir()
	old := write(t, dir, "v1.txt", expoV1)
	cur := write(t, dir, "v2.txt", expoV2)
	code, out, _ := run(t, "", "diff", old, cur)
	if code != ExitBroken {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if !strings.Contains(out, "promgold diff — 2 breaking, 1 risky") {
		t.Fatalf("header wrong:\n%s", out)
	}
}

func TestDiffGoldenAgainstExposition(t *testing.T) {
	dir := t.TempDir()
	golden := snapTo(t, dir, "--ignore", "go_*")
	// v2 without go_goroutines changes: the golden's ignore list must apply
	// to the text side too, keeping go_* out of the comparison.
	cur := write(t, dir, "v2.txt", expoV2)
	code, out, _ := run(t, "", "diff", golden, cur)
	if code != ExitBroken {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if strings.Contains(out, "go_goroutines") {
		t.Fatalf("golden ignore list not adopted for text side:\n%s", out)
	}
}

func TestDiffIdenticalSidesExitsZero(t *testing.T) {
	dir := t.TempDir()
	a := write(t, dir, "a.txt", expoV1)
	b := write(t, dir, "b.txt", expoV1)
	code, out, _ := run(t, "", "diff", a, b)
	if code != ExitOK || !strings.Contains(out, "no changes") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestDiffUsageErrors(t *testing.T) {
	code, _, errb := run(t, "", "diff", "only-one")
	if code != ExitUsage || !strings.Contains(errb, "exactly two sources") {
		t.Fatalf("one source: code=%d err=%q", code, errb)
	}
	code, _, errb = run(t, "", "diff", "-", "-")
	if code != ExitUsage || !strings.Contains(errb, "one side may read stdin") {
		t.Fatalf("double stdin: code=%d err=%q", code, errb)
	}
}

func TestCheckScrapesLoopbackEndpoint(t *testing.T) {
	// End-to-end over HTTP on 127.0.0.1: the exact CI shape for a service
	// that exposes /metrics in a test environment.
	dir := t.TempDir()
	golden := snapTo(t, dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(expoV1))
	}))
	defer srv.Close()
	code, out, errb := run(t, "", "check", "--golden", golden, srv.URL+"/metrics")
	if code != ExitOK || !strings.Contains(out, "no changes") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
}

func TestFlagEqualsSyntaxAccepted(t *testing.T) {
	dir := t.TempDir()
	src := write(t, dir, "v1.txt", expoV1)
	code, out, _ := run(t, "", "snap", "--out=-", "--ignore=go_*", src)
	if code != ExitOK || strings.Contains(out, "go_goroutines") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}
