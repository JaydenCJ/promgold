// Package expfmt parses the Prometheus text exposition format (the payload
// of a /metrics endpoint), including the OpenMetrics dialect: HELP/TYPE/UNIT
// metadata, escaped label values, histogram and summary series folding,
// exemplars, and the `# EOF` terminator.
//
// The parser is deliberately strict about the parts a metrics contract
// depends on (metric names, label names, label syntax) and tolerant about
// the parts it does not (unknown comment lines, timestamps, exemplars).
package expfmt

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Label is a single name="value" pair on a sample.
type Label struct {
	Name  string
	Value string
}

// Sample is one exposed series line: a metric name, its labels, and a value.
// Timestamps and exemplars are parsed and discarded — a contract is about
// shape, not readings.
type Sample struct {
	Name   string
	Labels []Label
	Value  float64
}

// Family is a group of samples that belong to one logical metric: the base
// name plus its HELP/TYPE/UNIT metadata. Histogram `_bucket`/`_sum`/`_count`
// series and summary quantile series fold into their base family.
type Family struct {
	Name    string
	Type    string // counter, gauge, histogram, summary, untyped, or an OpenMetrics type
	Help    string
	Unit    string // OpenMetrics `# UNIT`, empty for the classic format
	Samples []Sample
}

// ParseError reports a syntax problem with its 1-based line number, so CI
// logs point at the offending exposition line directly.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
}

func errAt(line int, format string, args ...any) error {
	return &ParseError{Line: line, Msg: fmt.Sprintf(format, args...)}
}

// knownTypes is every TYPE value promgold accepts: the four classic
// Prometheus types plus the OpenMetrics additions.
var knownTypes = map[string]bool{
	"counter": true, "gauge": true, "histogram": true, "summary": true,
	"untyped": true, "unknown": true, "gaugehistogram": true,
	"info": true, "stateset": true,
}

// suffixesByType lists the series-name suffixes each metric type may emit
// beyond its bare name. Used to fold child series into their base family.
var suffixesByType = map[string][]string{
	"counter":        {"_total", "_created"},
	"histogram":      {"_bucket", "_sum", "_count", "_created"},
	"gaugehistogram": {"_bucket", "_gsum", "_gcount"},
	"summary":        {"_sum", "_count", "_created"},
	"info":           {"_info"},
}

// meta accumulates comment metadata for one family while scanning.
type meta struct {
	help    string
	hasHelp bool
	typ     string
	unit    string
	order   int
}

// Parse reads a complete text exposition and returns its metric families in
// order of first appearance. Families declared via `# TYPE` but exposing no
// samples are still returned: an instrumented-but-idle metric is part of the
// contract too.
func Parse(r io.Reader) ([]Family, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	metas := map[string]*meta{}
	var order []string
	type rawSample struct {
		s    Sample
		line int
	}
	var samples []rawSample
	sawEOF := false
	lineNo := 0

	touch := func(name string) *meta {
		m, ok := metas[name]
		if !ok {
			m = &meta{order: len(order)}
			metas[name] = m
			order = append(order, name)
		}
		return m
	}

	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if sawEOF {
			return nil, errAt(lineNo, "content after # EOF terminator")
		}
		if strings.HasPrefix(line, "#") {
			kind, name, rest, ok := parseComment(line)
			if !ok {
				continue // free-form comment: ignored by design
			}
			switch kind {
			case "EOF":
				sawEOF = true
			case "HELP":
				if err := validateMetricName(name); err != nil {
					return nil, errAt(lineNo, "HELP: %v", err)
				}
				m := touch(name)
				m.help = unescapeHelp(rest)
				m.hasHelp = true
			case "TYPE":
				if err := validateMetricName(name); err != nil {
					return nil, errAt(lineNo, "TYPE: %v", err)
				}
				t := strings.TrimSpace(rest)
				if !knownTypes[t] {
					return nil, errAt(lineNo, "TYPE %s: unknown metric type %q", name, t)
				}
				m := touch(name)
				if m.typ != "" && m.typ != t {
					return nil, errAt(lineNo, "TYPE %s: redeclared as %q (was %q)", name, t, m.typ)
				}
				m.typ = t
			case "UNIT":
				if err := validateMetricName(name); err != nil {
					return nil, errAt(lineNo, "UNIT: %v", err)
				}
				touch(name).unit = strings.TrimSpace(rest)
			}
			continue
		}
		s, err := parseSample(line, lineNo)
		if err != nil {
			return nil, err
		}
		samples = append(samples, rawSample{s: s, line: lineNo})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading exposition: %w", err)
	}

	// Fold samples into families. A sample belongs to the family whose base
	// name plus a type-appropriate suffix produces the sample name; otherwise
	// it is its own untyped family.
	families := map[string]*Family{}
	var famOrder []string
	get := func(name string) *Family {
		f, ok := families[name]
		if !ok {
			f = &Family{Name: name, Type: "untyped"}
			if m, ok := metas[name]; ok {
				if m.typ != "" {
					f.Type = m.typ
				}
				f.Help = m.help
				f.Unit = m.unit
			}
			families[name] = f
			famOrder = append(famOrder, name)
		}
		return f
	}
	// Declared families first, in declaration order, so idle metrics keep
	// their place in the exposition.
	for _, name := range order {
		get(name)
	}
	for _, rs := range samples {
		base := familyFor(rs.s.Name, metas)
		f := get(base)
		if err := checkSampleShape(f, rs.s, rs.line); err != nil {
			return nil, err
		}
		f.Samples = append(f.Samples, rs.s)
	}

	out := make([]Family, 0, len(famOrder))
	for _, name := range famOrder {
		out = append(out, *families[name])
	}
	return out, nil
}

// familyFor resolves the base family name for a sample name: exact metadata
// match wins, then suffix stripping against a declared family of a type that
// legally emits that suffix.
func familyFor(name string, metas map[string]*meta) string {
	if m, ok := metas[name]; ok && m.typ != "" {
		return name
	}
	for base, m := range metas {
		if m.typ == "" || len(name) <= len(base) {
			continue
		}
		if !strings.HasPrefix(name, base) {
			continue
		}
		suffix := name[len(base):]
		for _, s := range suffixesByType[m.typ] {
			if suffix == s {
				return base
			}
		}
	}
	return name
}

// checkSampleShape rejects structurally impossible series early: a `le`
// label outside a histogram bucket, or `quantile` outside a summary, almost
// always means a mis-typed exposition rather than an intentional contract.
func checkSampleShape(f *Family, s Sample, line int) error {
	for _, l := range s.Labels {
		if l.Name == "le" && f.Type != "histogram" && f.Type != "gaugehistogram" && f.Type != "untyped" {
			return errAt(line, "%s: label \"le\" on a %s metric", s.Name, f.Type)
		}
		if l.Name == "quantile" && f.Type != "summary" && f.Type != "untyped" {
			return errAt(line, "%s: label \"quantile\" on a %s metric", s.Name, f.Type)
		}
	}
	return nil
}

// parseComment splits a `#` line into (keyword, metric name, remainder).
// ok is false for comments that are not HELP/TYPE/UNIT/EOF.
func parseComment(line string) (kind, name, rest string, ok bool) {
	body := strings.TrimPrefix(line, "#")
	body = strings.TrimLeft(body, " \t")
	if body == "EOF" {
		return "EOF", "", "", true
	}
	kw, tail, found := strings.Cut(body, " ")
	if !found {
		return "", "", "", false
	}
	switch kw {
	case "HELP", "TYPE", "UNIT":
		tail = strings.TrimLeft(tail, " \t")
		n, r, _ := strings.Cut(tail, " ")
		return kw, n, r, true
	}
	return "", "", "", false
}

// parseSample parses one series line:
//
//	name{label="value",...} value [timestamp] [# exemplar]
func parseSample(line string, lineNo int) (Sample, error) {
	i := 0
	for i < len(line) && !isNameEnd(line[i]) {
		i++
	}
	name := line[:i]
	if err := validateMetricName(name); err != nil {
		return Sample{}, errAt(lineNo, "%v", err)
	}
	s := Sample{Name: name}

	rest := line[i:]
	rest = strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(rest, "{") {
		labels, tail, err := parseLabels(rest, lineNo)
		if err != nil {
			return Sample{}, err
		}
		s.Labels = labels
		rest = strings.TrimLeft(tail, " \t")
	}

	// Strip an OpenMetrics exemplar: everything from " # " onward.
	if idx := strings.Index(rest, " # "); idx >= 0 {
		rest = rest[:idx]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return Sample{}, errAt(lineNo, "%s: missing sample value", name)
	}
	if len(fields) > 2 {
		return Sample{}, errAt(lineNo, "%s: unexpected trailing tokens after value", name)
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return Sample{}, errAt(lineNo, "%s: invalid sample value %q", name, fields[0])
	}
	s.Value = v
	if len(fields) == 2 {
		if _, err := strconv.ParseFloat(fields[1], 64); err != nil {
			return Sample{}, errAt(lineNo, "%s: invalid timestamp %q", name, fields[1])
		}
	}
	return s, nil
}

// parseLabels consumes a `{...}` label block and returns the labels plus the
// unconsumed remainder of the line.
func parseLabels(in string, lineNo int) ([]Label, string, error) {
	var labels []Label
	seen := map[string]bool{}
	i := 1 // past '{'
	for {
		for i < len(in) && (in[i] == ' ' || in[i] == '\t') {
			i++
		}
		if i >= len(in) {
			return nil, "", errAt(lineNo, "unterminated label block")
		}
		if in[i] == '}' {
			return labels, in[i+1:], nil
		}
		start := i
		for i < len(in) && in[i] != '=' && in[i] != '}' && in[i] != ',' {
			i++
		}
		if i >= len(in) || in[i] != '=' {
			return nil, "", errAt(lineNo, "label %q: expected '='", strings.TrimSpace(in[start:min(i, len(in))]))
		}
		lname := strings.TrimRight(in[start:i], " \t")
		if err := validateLabelName(lname); err != nil {
			return nil, "", errAt(lineNo, "%v", err)
		}
		if seen[lname] {
			return nil, "", errAt(lineNo, "duplicate label %q", lname)
		}
		seen[lname] = true
		i++ // past '='
		for i < len(in) && (in[i] == ' ' || in[i] == '\t') {
			i++
		}
		if i >= len(in) || in[i] != '"' {
			return nil, "", errAt(lineNo, "label %q: expected opening quote", lname)
		}
		val, next, err := readQuoted(in, i, lineNo)
		if err != nil {
			return nil, "", err
		}
		labels = append(labels, Label{Name: lname, Value: val})
		i = next
		for i < len(in) && (in[i] == ' ' || in[i] == '\t') {
			i++
		}
		if i >= len(in) {
			return nil, "", errAt(lineNo, "unterminated label block")
		}
		switch in[i] {
		case ',':
			i++
		case '}':
			// loop handles the close
		default:
			return nil, "", errAt(lineNo, "expected ',' or '}' after label value, got %q", string(in[i]))
		}
	}
}

// readQuoted reads a double-quoted label value starting at in[i] == '"',
// resolving the three escapes the format defines: \\ \" \n.
func readQuoted(in string, i, lineNo int) (string, int, error) {
	var b strings.Builder
	i++ // past opening quote
	for i < len(in) {
		c := in[i]
		switch c {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			i++
			if i >= len(in) {
				return "", 0, errAt(lineNo, "dangling escape in label value")
			}
			switch in[i] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			default:
				return "", 0, errAt(lineNo, "invalid escape \\%s in label value", string(in[i]))
			}
		default:
			b.WriteByte(c)
		}
		i++
	}
	return "", 0, errAt(lineNo, "unterminated label value")
}

// unescapeHelp resolves HELP-text escapes (\\ and \n only, per the format).
func unescapeHelp(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isNameEnd(c byte) bool {
	return c == '{' || c == ' ' || c == '\t'
}

func validateMetricName(name string) error {
	if name == "" {
		return fmt.Errorf("empty metric name")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := c == '_' || c == ':' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(i > 0 && c >= '0' && c <= '9')
		if !ok {
			return fmt.Errorf("invalid metric name %q", name)
		}
	}
	return nil
}

func validateLabelName(name string) error {
	if name == "" {
		return fmt.Errorf("empty label name")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(i > 0 && c >= '0' && c <= '9')
		if !ok {
			return fmt.Errorf("invalid label name %q", name)
		}
	}
	return nil
}
