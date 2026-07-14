// Package cli implements the promgold command-line interface. Run is
// invoked in-process by main and by the integration tests, so every code
// path is testable without spawning a binary.
//
// Exit codes are part of the contract: 0 the metrics contract holds,
// 1 the contract is broken, 2 usage error, 3 runtime error.
package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JaydenCJ/promgold/internal/version"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitBroken  = 1
	ExitUsage   = 2
	ExitRuntime = 3
)

// DefaultGolden is the conventional golden-file name, discovered by check
// and written by snap unless overridden.
const DefaultGolden = "promgold.golden.json"

const usage = `promgold — snapshot-test your Prometheus /metrics surface

Usage:
  promgold snap  [flags] <source>         capture a golden contract
  promgold check [flags] <source>         compare a live exposition to the golden
  promgold diff  [flags] <old> <new>      compare two expositions or goldens
  promgold version                        print the version

A <source> is a file path, "-" for stdin, or an http(s):// metrics URL.

Flags (snap):
  --out, -o FILE      golden file to write (default promgold.golden.json; "-" = stdout)
  --pin LABEL         also lock this label's value set (repeatable)
  --ignore PATTERN    skip metrics matching PATTERN, * wildcard (repeatable)
  --timeout DURATION  scrape timeout for http sources (default 10s)

Flags (check):
  --golden FILE       golden file to compare against (default promgold.golden.json)
  --format FORMAT     text, json, or markdown (default text)
  --fail-on LEVEL     breaking, risky, or info (default breaking)
  --update            rewrite the golden from the current exposition and exit 0
  --pin, --ignore, --timeout as above (added to the golden's recorded options)

Flags (diff):
  --format, --fail-on, --pin, --ignore, --timeout as above

Exit codes: 0 contract holds, 1 contract broken, 2 usage error, 3 runtime error.`

// env carries the I/O streams through every command.
type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// Run dispatches a full argv (without the program name) and returns the
// process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	e := &env{stdin: stdin, stdout: stdout, stderr: stderr}
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "snap":
		return e.runSnap(args[1:])
	case "check":
		return e.runCheck(args[1:])
	case "diff":
		return e.runDiff(args[1:])
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "promgold %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, usage)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "promgold: unknown command %q (try `promgold help`)\n", args[0])
		return ExitUsage
	}
}

func (e *env) usageErr(format string, args ...any) int {
	fmt.Fprintf(e.stderr, "promgold: "+format+"\n", args...)
	return ExitUsage
}

func (e *env) runtimeErr(err error) int {
	fmt.Fprintf(e.stderr, "promgold: %v\n", err)
	return ExitRuntime
}

// flagSet is a tiny argv walker supporting `--flag value`, `--flag=value`,
// repeatable flags, and positional arguments — enough surface for promgold
// without pulling flag-package quirks into three subcommands.
type flagSet struct {
	args []string
	i    int
}

// next returns the next raw token, or ok=false when exhausted.
func (fs *flagSet) next() (string, bool) {
	if fs.i >= len(fs.args) {
		return "", false
	}
	tok := fs.args[fs.i]
	fs.i++
	return tok, true
}

// value resolves the argument of a flag token: the part after "=", or the
// following token.
func (fs *flagSet) value(tok string) (string, error) {
	if _, v, ok := strings.Cut(tok, "="); ok {
		return v, nil
	}
	v, ok := fs.next()
	if !ok {
		return "", fmt.Errorf("flag %s needs a value", tok)
	}
	return v, nil
}

// flagName returns the flag name of a token ("--out=x" -> "--out"), or ""
// for positionals.
func flagName(tok string) string {
	if !strings.HasPrefix(tok, "-") || tok == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tok, "=")
	return name
}

// countNoun formats a count with the correctly pluralized noun ("1 family",
// "3 families").
func countNoun(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// commonFlags are shared by every subcommand.
type commonFlags struct {
	pin     []string
	ignore  []string
	timeout time.Duration
}

// parseCommon consumes a shared flag if tok matches one; handled reports
// whether it did.
func (e *env) parseCommon(fs *flagSet, tok string, cf *commonFlags) (handled bool, err error) {
	switch flagName(tok) {
	case "--pin":
		v, err := fs.value(tok)
		if err != nil {
			return true, err
		}
		cf.pin = append(cf.pin, v)
	case "--ignore":
		v, err := fs.value(tok)
		if err != nil {
			return true, err
		}
		cf.ignore = append(cf.ignore, v)
	case "--timeout":
		v, err := fs.value(tok)
		if err != nil {
			return true, err
		}
		d, perr := time.ParseDuration(v)
		if perr != nil || d <= 0 {
			return true, fmt.Errorf("--timeout: invalid duration %q", v)
		}
		cf.timeout = d
	default:
		return false, nil
	}
	return true, nil
}
