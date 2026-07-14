package cli

import (
	"bytes"
	"fmt"
	"os"

	"github.com/JaydenCJ/promgold/internal/contract"
	"github.com/JaydenCJ/promgold/internal/expfmt"
	"github.com/JaydenCJ/promgold/internal/fetch"
)

// runSnap captures a golden contract from a source and writes it out.
func (e *env) runSnap(args []string) int {
	out := DefaultGolden
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
		case "--out", "-o":
			v, err := fs.value(tok)
			if err != nil {
				return e.usageErr("%v", err)
			}
			out = v
		default:
			return e.usageErr("snap: unknown flag %s", flagName(tok))
		}
	}
	if len(positional) != 1 {
		return e.usageErr("snap: exactly one <source> required (file, \"-\", or http URL)")
	}

	c, code := e.buildContract(positional[0], cf)
	if code != ExitOK {
		return code
	}
	data, err := contract.Marshal(c)
	if err != nil {
		return e.runtimeErr(err)
	}
	if out == "-" {
		if _, err := e.stdout.Write(data); err != nil {
			return e.runtimeErr(err)
		}
		return ExitOK
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return e.runtimeErr(err)
	}
	fmt.Fprintf(e.stdout, "wrote %s: %s locked (%s, %s)\n",
		out,
		countNoun(len(c.Families), "family", "families"),
		countNoun(len(c.Pinned), "pinned label", "pinned labels"),
		countNoun(len(c.Ignored), "ignore pattern", "ignore patterns"))
	return ExitOK
}

// buildContract fetches, parses and condenses one exposition source.
func (e *env) buildContract(source string, cf commonFlags) (contract.Contract, int) {
	raw, err := fetch.Read(source, e.stdin, cf.timeout)
	if err != nil {
		return contract.Contract{}, e.runtimeErr(err)
	}
	families, err := expfmt.Parse(bytes.NewReader(raw))
	if err != nil {
		return contract.Contract{}, e.runtimeErr(fmt.Errorf("%s: %w", source, err))
	}
	c := contract.Build(families, contract.Options{Pin: cf.pin, Ignore: cf.ignore})
	return c, ExitOK
}
