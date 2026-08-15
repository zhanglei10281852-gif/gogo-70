// Command cablemend is the offline CableMend command line entry point.
//
// CableMend is an independent original project. It has no affiliation with, and
// no endorsement or sponsorship from, any company, vessel operator, product or
// organization. All bundled sample data is fictional.
package main

import (
	"os"

	"CableMend/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Stdout, os.Stderr, os.Args[1:]))
}
