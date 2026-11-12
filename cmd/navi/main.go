// Command navi is the stage0 compiler driver for the navi language.
package main

import (
	"fmt"
	"io"
	"os"
)

const version = "0.0.0-m0"

const usage = `usage: navi <command> [arguments]

commands:
  build     compile a crate into a native binary
  run       compile and run a crate
  check     type-check a crate without generating code
  test      build and run #[test] functions
  bench     build and run #[bench] functions
  version   print the navi version
`

// commands lists the subcommands that exist but are not implemented yet,
// with the milestone that delivers each of them.
var commands = map[string]string{
	"build": "M3",
	"run":   "M2",
	"check": "M1",
	"test":  "M2",
	"bench": "M9",
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch cmd := args[0]; cmd {
	case "version":
		fmt.Fprintf(stdout, "navi %s\n", version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		if milestone, ok := commands[cmd]; ok {
			fmt.Fprintf(stderr, "navi %s: not implemented yet (planned for %s)\n", cmd, milestone)
			return 1
		}
		fmt.Fprintf(stderr, "navi: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}
