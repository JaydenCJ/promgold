// Package contract turns a parsed exposition into promgold's golden
// snapshot: the stable, queryable *shape* of a /metrics endpoint — family
// names, types, units, label keys, histogram buckets, summary quantiles,
// and the enumerated values of explicitly pinned labels. Sample values and
// high-cardinality label values are deliberately excluded: a contract locks
// the API surface, not the readings.
package contract

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/JaydenCJ/promgold/internal/expfmt"
)

// SchemaVersion identifies the golden-file layout. Bump only with a
// documented migration.
const SchemaVersion = 1

// structural labels are always value-tracked: their value sets ARE the
// metric's shape (bucket boundaries, published quantiles).
var structural = map[string]bool{"le": true, "quantile": true}

// Family is the contract of one metric family.
type Family struct {
	Name   string              `json:"name"`
	Type   string              `json:"type"`
	Help   string              `json:"help,omitempty"`
	Unit   string              `json:"unit,omitempty"`
	Labels []string            `json:"labels"`
	Values map[string][]string `json:"values,omitempty"`
}

// Contract is the full golden snapshot of an exposition, plus the options
// it was captured with, so `promgold check` reproduces the exact same view.
type Contract struct {
	Tool          string   `json:"tool"`
	SchemaVersion int      `json:"schema_version"`
	Pinned        []string `json:"pinned,omitempty"`
	Ignored       []string `json:"ignored,omitempty"`
	Families      []Family `json:"families"`
}

// Options controls how an exposition is condensed into a contract.
type Options struct {
	// Pin lists label names whose value sets become part of the contract
	// (e.g. "code" to lock the exact set of exposed status codes).
	Pin []string
	// Ignore lists metric-name patterns to exclude entirely; `*` matches
	// any run of characters (e.g. "go_*" to skip runtime metrics).
	Ignore []string
}

// Build condenses parsed families into a deterministic contract: families
// sorted by name, label keys sorted, tracked values sorted (numerically for
// le/quantile, +Inf last).
func Build(families []expfmt.Family, opts Options) Contract {
	pinned := normalizeList(opts.Pin)
	ignored := normalizeList(opts.Ignore)
	pinSet := map[string]bool{}
	for _, p := range pinned {
		pinSet[p] = true
	}

	out := Contract{
		Tool:          "promgold",
		SchemaVersion: SchemaVersion,
		Pinned:        pinned,
		Ignored:       ignored,
	}
	for _, f := range families {
		if matchAny(ignored, f.Name) {
			continue
		}
		fc := Family{
			Name: f.Name,
			Type: f.Type,
			Help: f.Help,
			Unit: f.Unit,
		}
		labelSet := map[string]bool{}
		valueSets := map[string]map[string]bool{}
		for _, s := range f.Samples {
			for _, l := range s.Labels {
				if structural[l.Name] || pinSet[l.Name] {
					if valueSets[l.Name] == nil {
						valueSets[l.Name] = map[string]bool{}
					}
					valueSets[l.Name][l.Value] = true
				}
				if !structural[l.Name] {
					labelSet[l.Name] = true
				}
			}
		}
		fc.Labels = sortedKeys(labelSet)
		if len(valueSets) > 0 {
			fc.Values = map[string][]string{}
			for name, set := range valueSets {
				fc.Values[name] = sortValues(name, sortedKeys(set))
			}
		}
		out.Families = append(out.Families, fc)
	}
	sort.Slice(out.Families, func(i, j int) bool {
		return out.Families[i].Name < out.Families[j].Name
	})
	return out
}

// Marshal renders the contract as stable, human-diffable JSON: two-space
// indent, sorted keys, trailing newline. Byte-identical for equal contracts,
// so the golden file itself is friendly to `git diff`.
func Marshal(c Contract) ([]byte, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Load parses and validates a golden file produced by Marshal.
func Load(data []byte) (Contract, error) {
	var c Contract
	if err := json.Unmarshal(data, &c); err != nil {
		return Contract{}, fmt.Errorf("golden file is not valid JSON: %w", err)
	}
	if c.Tool != "promgold" {
		return Contract{}, fmt.Errorf("golden file was not written by promgold (tool=%q)", c.Tool)
	}
	if c.SchemaVersion != SchemaVersion {
		return Contract{}, fmt.Errorf("golden file has schema_version %d; this promgold reads %d",
			c.SchemaVersion, SchemaVersion)
	}
	for _, f := range c.Families {
		if f.Name == "" {
			return Contract{}, fmt.Errorf("golden file contains a family with no name")
		}
	}
	return c, nil
}

// LooksLikeGolden reports whether raw bytes are a promgold golden file
// rather than a text exposition, so `promgold diff` accepts either.
func LooksLikeGolden(data []byte) bool {
	trimmed := strings.TrimLeft(string(data), " \t\r\n")
	return strings.HasPrefix(trimmed, "{")
}

// Family returns the named family and whether it exists.
func (c Contract) Family(name string) (Family, bool) {
	for _, f := range c.Families {
		if f.Name == name {
			return f, true
		}
	}
	return Family{}, false
}

// matchAny reports whether name matches any pattern; `*` in a pattern
// matches any (possibly empty) run of characters.
func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if matchStar(p, name) {
			return true
		}
	}
	return false
}

func matchStar(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// sortValues orders a tracked label's values: numerically for the
// structural labels (so buckets read 0.1 < 0.5 < 1 < +Inf), lexically for
// pinned application labels.
func sortValues(label string, vals []string) []string {
	if !structural[label] {
		return vals
	}
	sort.SliceStable(vals, func(i, j int) bool {
		a, errA := strconv.ParseFloat(vals[i], 64)
		b, errB := strconv.ParseFloat(vals[j], 64)
		if errA != nil || errB != nil {
			return vals[i] < vals[j]
		}
		return a < b
	})
	return vals
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// normalizeList sorts and deduplicates a flag-provided list so the same
// options always serialize identically into the golden file.
func normalizeList(in []string) []string {
	set := map[string]bool{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			set[v] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return sortedKeys(set)
}
