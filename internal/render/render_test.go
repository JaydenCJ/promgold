// Tests for the three report renderers: alignment, verdict lines, JSON
// envelope stability, and Markdown table escaping.
package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaydenCJ/promgold/internal/diffcore"
)

func sampleResult() diffcore.Result {
	return diffcore.Result{
		Changes: []diffcore.Change{
			{Severity: diffcore.Breaking, Kind: diffcore.MetricRemoved, Metric: "queue_depth", Detail: "metric no longer exposed"},
			{Severity: diffcore.Risky, Kind: diffcore.LabelAdded, Metric: "http_requests_total", Detail: `new label "tenant" (existing series split; sum/avg results change)`},
			{Severity: diffcore.Info, Kind: diffcore.HelpChanged, Metric: "up", Detail: "help text changed"},
		},
		Breaking: 1, Risky: 1, Info: 1,
		FamiliesGolden: 5, FamiliesCurrent: 5,
	}
}

func TestTextReportAlignsColumnsAndOrders(t *testing.T) {
	out := Text("check", sampleResult(), diffcore.Breaking)
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], "1 breaking, 1 risky, 1 informational") {
		t.Fatalf("header wrong: %q", lines[0])
	}
	if !strings.Contains(out, "BREAKING  queue_depth") {
		t.Fatalf("breaking row missing:\n%s", out)
	}
	// Every severity tag column starts at the same offset.
	var rows []string
	for _, l := range lines {
		if strings.HasPrefix(l, "BREAKING") || strings.HasPrefix(l, "RISKY") || strings.HasPrefix(l, "INFO") {
			rows = append(rows, l)
		}
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 change rows, got %d:\n%s", len(rows), out)
	}
	if !strings.Contains(out, "contract: BROKEN — 1 change at or above fail-on=breaking") {
		t.Fatalf("verdict wrong:\n%s", out)
	}
}

func TestTextVerdictBelowGateAndClean(t *testing.T) {
	res := sampleResult()
	res.Changes = res.Changes[2:] // info only
	res.Breaking, res.Risky = 0, 0
	out := Text("check", res, diffcore.Breaking)
	if !strings.Contains(out, "contract: OK — 1 change, all below fail-on=breaking") {
		t.Fatalf("below-gate verdict wrong:\n%s", out)
	}
	out = Text("check", diffcore.Result{FamiliesGolden: 3, FamiliesCurrent: 3}, diffcore.Breaking)
	if !strings.Contains(out, "contract: OK — no changes") {
		t.Fatalf("clean verdict wrong:\n%s", out)
	}
}

func TestJSONReportIsValidAndComplete(t *testing.T) {
	out, err := JSON("check", sampleResult(), diffcore.Risky)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var rep struct {
		Tool          string `json:"tool"`
		SchemaVersion int    `json:"schema_version"`
		Command       string `json:"command"`
		Summary       struct {
			Breaking int `json:"breaking"`
		} `json:"summary"`
		Changes []struct {
			Severity string `json:"severity"`
			Kind     string `json:"kind"`
		} `json:"changes"`
		FailOn string `json:"fail_on"`
		Broken bool   `json:"broken"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if rep.Tool != "promgold" || rep.SchemaVersion != 1 || rep.Command != "check" {
		t.Fatalf("envelope wrong: %+v", rep)
	}
	if !rep.Broken || rep.FailOn != "risky" || rep.Summary.Breaking != 1 {
		t.Fatalf("summary wrong: %+v", rep)
	}
	if len(rep.Changes) != 3 || rep.Changes[0].Severity != "breaking" || rep.Changes[0].Kind != "metric-removed" {
		t.Fatalf("changes wrong: %+v", rep.Changes)
	}
	// Empty change lists must render as [] not null, for strict consumers.
	empty, err := JSON("check", diffcore.Result{}, diffcore.Breaking)
	if err != nil || !strings.Contains(empty, `"changes": []`) {
		t.Fatalf("empty changes must render as []: %v\n%s", err, empty)
	}
}

func TestMarkdownTableAndVerdict(t *testing.T) {
	out := Markdown("diff", sampleResult(), diffcore.Breaking)
	if !strings.Contains(out, "## promgold diff — ❌ contract broken") {
		t.Fatalf("verdict heading wrong:\n%s", out)
	}
	if !strings.Contains(out, "| Severity | Metric | Change |") {
		t.Fatalf("table header missing:\n%s", out)
	}
	if !strings.Contains(out, "| BREAKING | `queue_depth` | metric no longer exposed |") {
		t.Fatalf("table row missing:\n%s", out)
	}
}

func TestMarkdownNoChanges(t *testing.T) {
	out := Markdown("check", diffcore.Result{}, diffcore.Breaking)
	if !strings.Contains(out, "✅ contract holds") || !strings.Contains(out, "No changes") {
		t.Fatalf("clean report wrong:\n%s", out)
	}
}

func TestMarkdownEscapesPipes(t *testing.T) {
	res := diffcore.Result{Changes: []diffcore.Change{
		{Severity: diffcore.Info, Kind: diffcore.HelpChanged, Metric: "m", Detail: "a|b"},
	}, Info: 1}
	out := Markdown("check", res, diffcore.Breaking)
	if !strings.Contains(out, `a\|b`) {
		t.Fatalf("pipe not escaped:\n%s", out)
	}
}
