// Command promgold snapshot-tests a Prometheus /metrics exposition: it
// locks metric names, types, label keys, buckets, and quantiles into a
// golden contract file and fails CI when the surface changes underneath
// existing dashboards and alerts.
package main

import (
	"os"

	"github.com/JaydenCJ/promgold/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
