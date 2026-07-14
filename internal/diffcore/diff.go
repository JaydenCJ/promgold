package diffcore

import (
	"fmt"
	"sort"

	"github.com/JaydenCJ/promgold/internal/contract"
)

// labelValueName names a tracked label in change details: structural labels
// get their domain term, pinned labels stay generic.
func labelValueName(label string) string {
	switch label {
	case "le":
		return "histogram bucket"
	case "quantile":
		return "summary quantile"
	default:
		return "pinned value"
	}
}

// Diff compares the golden contract against the current one and returns
// every classified change, sorted worst-first then by metric name, so the
// top of the report is always the most urgent line.
func Diff(golden, current contract.Contract) Result {
	res := Result{
		FamiliesGolden:  len(golden.Families),
		FamiliesCurrent: len(current.Families),
	}
	add := func(sev Severity, kind Kind, metric, detail string) {
		res.Changes = append(res.Changes, Change{Severity: sev, Kind: kind, Metric: metric, Detail: detail})
		switch sev {
		case Breaking:
			res.Breaking++
		case Risky:
			res.Risky++
		default:
			res.Info++
		}
	}

	seen := map[string]bool{}
	for _, g := range golden.Families {
		seen[g.Name] = true
		c, ok := current.Family(g.Name)
		if !ok {
			add(Breaking, MetricRemoved, g.Name, "metric no longer exposed")
			continue
		}
		diffFamily(g, c, add)
	}
	for _, c := range current.Families {
		if !seen[c.Name] {
			add(Info, MetricAdded, c.Name, fmt.Sprintf("new %s metric", c.Type))
		}
	}

	sortChanges(res.Changes)
	return res
}

func diffFamily(g, c contract.Family, add func(Severity, Kind, string, string)) {
	if g.Type != c.Type {
		sev := Breaking
		if g.Type == "untyped" || g.Type == "unknown" {
			// Gaining a concrete type keeps every existing query matching;
			// flag it so the golden gets refreshed deliberately.
			sev = Risky
		}
		add(sev, TypeChanged, g.Name, fmt.Sprintf("type changed: %s -> %s", g.Type, c.Type))
	}
	if g.Unit != c.Unit {
		add(Breaking, UnitChanged, g.Name,
			fmt.Sprintf("unit changed: %s -> %s (dashboards now read the wrong scale)",
				orNone(g.Unit), orNone(c.Unit)))
	}
	if g.Help != c.Help {
		add(Info, HelpChanged, g.Name, "help text changed")
	}

	gl, cl := toSet(g.Labels), toSet(c.Labels)
	for _, l := range g.Labels {
		if !cl[l] {
			add(Breaking, LabelRemoved, g.Name,
				fmt.Sprintf("label %q removed (selectors and by(%s) clauses stop matching)", l, l))
		}
	}
	for _, l := range c.Labels {
		if !gl[l] {
			add(Risky, LabelAdded, g.Name,
				fmt.Sprintf("new label %q (existing series split; sum/avg results change)", l))
		}
	}

	// Tracked values: compare each label enumerated on either side.
	for _, label := range unionKeys(g.Values, c.Values) {
		gv, cv := toSet(g.Values[label]), toSet(c.Values[label])
		for _, v := range g.Values[label] {
			if !cv[v] {
				add(Breaking, ValueRemoved, g.Name,
					fmt.Sprintf("%s %s=%q removed", labelValueName(label), label, v))
			}
		}
		for _, v := range c.Values[label] {
			if !gv[v] {
				add(Info, ValueAdded, g.Name,
					fmt.Sprintf("new %s %s=%q", labelValueName(label), label, v))
			}
		}
	}
}

// sortChanges orders worst-first, then by metric, then kind, then detail —
// a total order, so reports are byte-stable.
func sortChanges(changes []Change) {
	sort.SliceStable(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.Severity != b.Severity {
			return a.Severity > b.Severity
		}
		if a.Metric != b.Metric {
			return a.Metric < b.Metric
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Detail < b.Detail
	})
}

func toSet(vals []string) map[string]bool {
	set := make(map[string]bool, len(vals))
	for _, v := range vals {
		set[v] = true
	}
	return set
}

func unionKeys(a, b map[string][]string) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
