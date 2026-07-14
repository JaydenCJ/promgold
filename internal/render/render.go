// Package render turns a diff result into the three report formats:
// aligned text for terminals, stable JSON (schema_version 1) for machines,
// and a Markdown table for PR comments. All three are deterministic:
// identical inputs produce byte-identical output.
package render

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JaydenCJ/promgold/internal/diffcore"
)

// severityTag maps severities to fixed-width report tags.
func severityTag(s diffcore.Severity) string {
	switch s {
	case diffcore.Breaking:
		return "BREAKING"
	case diffcore.Risky:
		return "RISKY"
	default:
		return "INFO"
	}
}

// Text renders the human report: a summary header, one aligned row per
// change (worst first), and a verdict line.
func Text(cmd string, res diffcore.Result, failOn diffcore.Severity) string {
	var b strings.Builder
	fmt.Fprintf(&b, "promgold %s — %d breaking, %d risky, %d informational (%d golden vs %d current families)\n",
		cmd, res.Breaking, res.Risky, res.Info, res.FamiliesGolden, res.FamiliesCurrent)

	if len(res.Changes) > 0 {
		b.WriteString("\n")
		tagW, metricW := 0, 0
		for _, c := range res.Changes {
			tagW = maxInt(tagW, len(severityTag(c.Severity)))
			metricW = maxInt(metricW, len(c.Metric))
		}
		for _, c := range res.Changes {
			fmt.Fprintf(&b, "%-*s  %-*s  %s\n", tagW, severityTag(c.Severity), metricW, c.Metric, c.Detail)
		}
	}

	b.WriteString("\n")
	failing := res.AtOrAbove(failOn)
	switch {
	case failing > 0:
		fmt.Fprintf(&b, "contract: BROKEN — %s at or above fail-on=%s\n", countNoun(failing, "change", "changes"), failOn)
	case len(res.Changes) > 0:
		fmt.Fprintf(&b, "contract: OK — %s, all below fail-on=%s\n", countNoun(len(res.Changes), "change", "changes"), failOn)
	default:
		b.WriteString("contract: OK — no changes\n")
	}
	return b.String()
}

// countNoun formats a count with the correctly pluralized noun ("1 change",
// "3 changes").
func countNoun(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// jsonReport is the stable machine envelope.
type jsonReport struct {
	Tool          string       `json:"tool"`
	SchemaVersion int          `json:"schema_version"`
	Command       string       `json:"command"`
	Summary       jsonSummary  `json:"summary"`
	Changes       []jsonChange `json:"changes"`
	FailOn        string       `json:"fail_on"`
	Broken        bool         `json:"broken"`
}

type jsonSummary struct {
	Breaking        int `json:"breaking"`
	Risky           int `json:"risky"`
	Info            int `json:"info"`
	FamiliesGolden  int `json:"families_golden"`
	FamiliesCurrent int `json:"families_current"`
}

type jsonChange struct {
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Metric   string `json:"metric"`
	Detail   string `json:"detail"`
}

// JSON renders the machine report with a trailing newline.
func JSON(cmd string, res diffcore.Result, failOn diffcore.Severity) (string, error) {
	rep := jsonReport{
		Tool:          "promgold",
		SchemaVersion: 1,
		Command:       cmd,
		Summary: jsonSummary{
			Breaking:        res.Breaking,
			Risky:           res.Risky,
			Info:            res.Info,
			FamiliesGolden:  res.FamiliesGolden,
			FamiliesCurrent: res.FamiliesCurrent,
		},
		Changes: make([]jsonChange, 0, len(res.Changes)),
		FailOn:  failOn.String(),
		Broken:  res.AtOrAbove(failOn) > 0,
	}
	for _, c := range res.Changes {
		rep.Changes = append(rep.Changes, jsonChange{
			Severity: c.Severity.String(),
			Kind:     string(c.Kind),
			Metric:   c.Metric,
			Detail:   c.Detail,
		})
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

// Markdown renders a PR-comment-ready table.
func Markdown(cmd string, res diffcore.Result, failOn diffcore.Severity) string {
	var b strings.Builder
	verdict := "✅ contract holds"
	if res.AtOrAbove(failOn) > 0 {
		verdict = "❌ contract broken"
	}
	fmt.Fprintf(&b, "## promgold %s — %s\n\n", cmd, verdict)
	fmt.Fprintf(&b, "%d breaking · %d risky · %d informational (fail-on: %s)\n\n",
		res.Breaking, res.Risky, res.Info, failOn)
	if len(res.Changes) == 0 {
		b.WriteString("No changes: the /metrics surface matches the golden contract.\n")
		return b.String()
	}
	b.WriteString("| Severity | Metric | Change |\n|---|---|---|\n")
	for _, c := range res.Changes {
		fmt.Fprintf(&b, "| %s | `%s` | %s |\n",
			severityTag(c.Severity), c.Metric, escapeCell(c.Detail))
	}
	return b.String()
}

func escapeCell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
