// Command navi is the stage0 compiler driver for the navi language.
package main

import (
	"fmt"
	"io"
	"os"

	"navi/internal/build"
	"navi/internal/interp"
	"navi/internal/mir"
	"navi/internal/resolve"
	"navi/internal/syntax"
)

const version = "0.0.0-m0"

const usage = `usage: navi <command> [arguments]

commands:
  build     compile a crate into a native binary
  run       run a crate (with the MIR interpreter until native code
            generation lands)
  check     check a crate for errors without generating code
            (currently: syntax and name resolution)
  test      build and run #[test] functions
  bench     build and run #[bench] functions
  parse     parse files and print their syntax trees (debugging)
  version   print the navi version
`

// commands lists the subcommands that exist but are not implemented yet,
// with the milestone that delivers each of them.
var commands = map[string]string{
	"build": "M3",
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
	case "parse":
		return parseFiles(args[1:], stdout, stderr)
	case "check":
		return check(args[1:], stdout, stderr)
	case "mir":
		return dumpMIR(args[1:], stdout, stderr)
	case "run":
		return runCrate(args[1:], stdout, stderr)
	default:
		if milestone, ok := commands[cmd]; ok {
			fmt.Fprintf(stderr, "navi %s: not implemented yet (planned for %s)\n", cmd, milestone)
			return 1
		}
		fmt.Fprintf(stderr, "navi: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func parseFiles(paths []string, stdout, stderr io.Writer) int {
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "usage: navi parse <file.navi>...")
		return 2
	}
	code := 0
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			code = 1
			continue
		}
		sf, errs := syntax.ParseFile(syntax.NewFile(path, src))
		if errs.Len() > 0 {
			fmt.Fprintln(stderr, errs)
			code = 1
			continue
		}
		fmt.Fprintln(stdout, syntax.Dump(sf))
	}
	return code
}

func check(paths []string, stdout, stderr io.Writer) int {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	code := 0
	for _, path := range paths {
		errs, err := build.Check(path, build.HostConfig())
		if err != nil {
			fmt.Fprintln(stderr, err)
			code = 1
			continue
		}
		if len(errs) > 0 {
			fmt.Fprintln(stderr, errs)
			code = 1
		}
	}
	return code
}

// dumpMIR prints the MIR of the functions in the given crate (debugging).
func dumpMIR(paths []string, stdout, stderr io.Writer) int {
	if len(paths) != 1 {
		fmt.Fprintln(stderr, "usage: navi mir <crate or file>")
		return 2
	}
	spec, err := build.LoadCrate(paths[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, errs := build.CheckSpecs(build.HostConfig(), []*resolve.CrateSpec{build.StdCrate(), spec})
	if len(errs) > 0 {
		fmt.Fprintln(stderr, errs)
		return 1
	}
	prog := mir.Build(info)
	for def, f := range prog.Funcs {
		if def.Crate.Name == spec.Name {
			fmt.Fprint(stdout, f)
		}
	}
	return 0
}

// runCrate type-checks a crate and runs its main function in the MIR
// interpreter.
func runCrate(args []string, stdout, stderr io.Writer) int {
	var paths []string
	for _, a := range args {
		if a != "--interp" {
			paths = append(paths, a)
		}
	}
	if len(paths) != 1 {
		fmt.Fprintln(stderr, "usage: navi run [--interp] <crate or file>")
		return 2
	}
	spec, err := build.LoadCrate(paths[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, errs := build.CheckSpecs(build.HostConfig(), []*resolve.CrateSpec{build.StdCrate(), spec})
	if len(errs) > 0 {
		fmt.Fprintln(stderr, errs)
		return 1
	}
	var main *resolve.Def
	for _, c := range info.Res.Crates {
		if c.Name != spec.Name {
			continue
		}
		if b := c.Root.Lookup("main", resolve.ValueNS); b != nil && b.Res.Def != nil && b.Res.Def.Kind == resolve.DefFn {
			main = b.Res.Def
		}
	}
	if main == nil {
		fmt.Fprintf(stderr, "%s: no main function\n", paths[0])
		return 1
	}
	m := interp.New(mir.Build(info))
	m.Stdout, m.Stderr = stdout, stderr
	return m.Run(main)
}
