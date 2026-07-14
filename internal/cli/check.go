package cli

import (
	"fmt"
	"os"

	"github.com/JaydenCJ/promgold/internal/contract"
	"github.com/JaydenCJ/promgold/internal/diffcore"
	"github.com/JaydenCJ/promgold/internal/render"
)

// runCheck compares a live exposition against the golden contract and gates
// on the configured severity.
func (e *env) runCheck(args []string) int {
	golden := DefaultGolden
	format := "text"
	failOn := diffcore.Breaking
	update := false
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
		case "--golden":
			v, err := fs.value(tok)
			if err != nil {
				return e.usageErr("%v", err)
			}
			golden = v
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
		case "--update":
			update = true
		default:
			return e.usageErr("check: unknown flag %s", flagName(tok))
		}
	}
	if !validFormat(format) {
		return e.usageErr("--format: want text, json, or markdown; got %q", format)
	}
	if len(positional) != 1 {
		return e.usageErr("check: exactly one <source> required (file, \"-\", or http URL)")
	}

	data, err := os.ReadFile(golden)
	if err != nil {
		if os.IsNotExist(err) && update {
			// First run with --update bootstraps the golden.
			return e.rewriteGolden(golden, positional[0], cf)
		}
		return e.runtimeErr(fmt.Errorf("no golden contract at %s (run `promgold snap` first): %w", golden, err))
	}
	gc, err := contract.Load(data)
	if err != nil {
		return e.runtimeErr(fmt.Errorf("%s: %w", golden, err))
	}

	// The golden remembers how it was captured; CLI flags only add to it,
	// so a check can never accidentally see a wider surface than the snap.
	cf.pin = append(cf.pin, gc.Pinned...)
	cf.ignore = append(cf.ignore, gc.Ignored...)

	if update {
		return e.rewriteGolden(golden, positional[0], cf)
	}

	cc, code := e.buildContract(positional[0], cf)
	if code != ExitOK {
		return code
	}
	res := diffcore.Diff(gc, cc)
	return e.report("check", res, format, failOn)
}

// rewriteGolden re-snaps the source over the golden file.
func (e *env) rewriteGolden(golden, source string, cf commonFlags) int {
	c, code := e.buildContract(source, cf)
	if code != ExitOK {
		return code
	}
	data, err := contract.Marshal(c)
	if err != nil {
		return e.runtimeErr(err)
	}
	if err := os.WriteFile(golden, data, 0o644); err != nil {
		return e.runtimeErr(err)
	}
	fmt.Fprintf(e.stdout, "updated %s: %s locked\n", golden, countNoun(len(c.Families), "family", "families"))
	return ExitOK
}

// report renders a diff result and converts it into the exit code.
func (e *env) report(cmd string, res diffcore.Result, format string, failOn diffcore.Severity) int {
	var out string
	var err error
	switch format {
	case "json":
		out, err = render.JSON(cmd, res, failOn)
	case "markdown":
		out = render.Markdown(cmd, res, failOn)
	default:
		out = render.Text(cmd, res, failOn)
	}
	if err != nil {
		return e.runtimeErr(err)
	}
	fmt.Fprint(e.stdout, out)
	if res.AtOrAbove(failOn) > 0 {
		return ExitBroken
	}
	return ExitOK
}

func validFormat(f string) bool {
	return f == "text" || f == "json" || f == "markdown"
}
