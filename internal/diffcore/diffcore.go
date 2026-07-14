// Package diffcore compares two metric contracts and classifies every
// difference by blast radius:
//
//   - breaking — queries, recording rules, or alerts that exist today stop
//     matching (removed metric, changed type, removed label key, removed
//     bucket/quantile/pinned value, changed unit).
//   - risky — existing queries still match but their results silently change
//     shape (a new label key splits series and skews aggregations).
//   - info — additive or cosmetic (new metric, new tracked value, help text).
//
// The classification is data, not code spread across call sites: one change
// kind maps to one severity, documented in the README's rules table.
package diffcore

// Severity orders changes by blast radius.
type Severity int

const (
	Info Severity = iota
	Risky
	Breaking
)

// String returns the lowercase name used in JSON output and --fail-on.
func (s Severity) String() string {
	switch s {
	case Breaking:
		return "breaking"
	case Risky:
		return "risky"
	default:
		return "info"
	}
}

// ParseSeverity resolves a --fail-on flag value.
func ParseSeverity(s string) (Severity, bool) {
	switch s {
	case "breaking":
		return Breaking, true
	case "risky":
		return Risky, true
	case "info":
		return Info, true
	}
	return 0, false
}

// Kind identifies what changed. Stable strings: they appear in JSON output.
type Kind string

const (
	MetricRemoved Kind = "metric-removed"
	MetricAdded   Kind = "metric-added"
	TypeChanged   Kind = "type-changed"
	UnitChanged   Kind = "unit-changed"
	HelpChanged   Kind = "help-changed"
	LabelRemoved  Kind = "label-removed"
	LabelAdded    Kind = "label-added"
	ValueRemoved  Kind = "value-removed"
	ValueAdded    Kind = "value-added"
)

// Change is one classified difference between golden and current.
type Change struct {
	Severity Severity `json:"-"`
	Kind     Kind     `json:"kind"`
	Metric   string   `json:"metric"`
	Detail   string   `json:"detail"`
}

// Result is the full comparison outcome.
type Result struct {
	Changes  []Change
	Breaking int
	Risky    int
	Info     int
	// FamiliesGolden and FamiliesCurrent are the family counts on each side,
	// for the report header.
	FamiliesGolden  int
	FamiliesCurrent int
}

// AtOrAbove counts changes with severity >= level.
func (r Result) AtOrAbove(level Severity) int {
	n := 0
	for _, c := range r.Changes {
		if c.Severity >= level {
			n++
		}
	}
	return n
}
