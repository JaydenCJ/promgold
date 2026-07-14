package cli

import (
	"bytes"
	"fmt"

	"github.com/JaydenCJ/promgold/internal/contract"
	"github.com/JaydenCJ/promgold/internal/diffcore"
	"github.com/JaydenCJ/promgold/internal/expfmt"
	"github.com/JaydenCJ/promgold/internal/fetch"
)

// runDiff compares two sources directly — each side may be a text
// exposition or a promgold golden file (sniffed by content), so you can
// diff staging against production, or a golden against a proposed one.
func (e *env) runDiff(args []string) int {
	format := "text"
	failOn := diffcore.Breaking
	var cf commonFlags
	var positional []string

	fs := &flagSet{args: args}
	for {
		tok, ok := fs.next()
		if !ok {
			break
		}
		if handled, err := e.parseCommon(fs, tok, &cf); handled {
			if err != nil {
				return e.usageErr("%v", err)
			}
			continue
		}
		switch flagName(tok) {
		case "":
			positional = append(positional, tok)
		case "--format":
			v, err := fs.value(tok)
			if err != nil {
				return e.usageErr("%v", err)
			}
			format = v
		case "--fail-on":
			v, err := fs.value(tok)
			if err != nil {
				return e.usageErr("%v", err)
			}
			sev, ok := diffcore.ParseSeverity(v)
			if !ok {
				return e.usageErr("--fail-on: want breaking, risky, or info; got %q", v)
			}
			failOn = sev
		default:
			return e.usageErr("diff: unknown flag %s", flagName(tok))
		}
	}
	if !validFormat(format) {
		return e.usageErr("--format: want text, json, or markdown; got %q", format)
	}
	if len(positional) != 2 {
		return e.usageErr("diff: exactly two sources required: <old> <new>")
	}
	if positional[0] == "-" && positional[1] == "-" {
		return e.usageErr("diff: only one side may read stdin")
	}

	// Goldens carry their own capture options; adopt them for the text
	// sides so both sides are condensed through the same lens.
	sides := make([]contract.Contract, 2)
	raws := make([][]byte, 2)
	for i, src := range positional {
		raw, err := fetch.Read(src, e.stdin, cf.timeout)
		if err != nil {
			return e.runtimeErr(err)
		}
		raws[i] = raw
	}
	for i := range raws {
		if contract.LooksLikeGolden(raws[i]) {
			c, err := contract.Load(raws[i])
			if err != nil {
				return e.runtimeErr(fmt.Errorf("%s: %w", positional[i], err))
			}
			cf.pin = append(cf.pin, c.Pinned...)
			cf.ignore = append(cf.ignore, c.Ignored...)
			sides[i] = c
		}
	}
	for i := range raws {
		if sides[i].Tool != "" {
			continue // already loaded as a golden
		}
		families, err := expfmt.Parse(bytes.NewReader(raws[i]))
		if err != nil {
			return e.runtimeErr(fmt.Errorf("%s: %w", positional[i], err))
		}
		sides[i] = contract.Build(families, contract.Options{Pin: cf.pin, Ignore: cf.ignore})
	}

	res := diffcore.Diff(sides[0], sides[1])
	return e.report("diff", res, format, failOn)
}
