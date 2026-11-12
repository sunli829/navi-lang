// Command navi is the stage0 compiler driver for the navi language.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"navi/internal/build"
	"navi/internal/interp"
	"navi/internal/mir"
	"navi/internal/resolve"
	"navi/internal/syntax"
	"navi/internal/types"
)

const version = "0.0.0-m0"

const usage = `usage: navi <command> [arguments]

commands:
  build     compile a crate into a native binary
  run       build and run a crate (--interp: run it on the MIR
            interpreter instead)
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
	case "build":
		return buildCrate(args[1:], stdout, stderr)
	case "resolve-dump":
		return resolveDump(args[1:], stdout, stderr)
	case "types-dump":
		return typesDump(args[1:], stdout, stderr)
	case "mir-dump":
		return mirDump(args[1:], stdout, stderr)
	case "test":
		return testCrate(args[1:], stdout, stderr)
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
	var paths, progArgs []string
	useInterp := !build.NativeSupported()
	for i, a := range args {
		if a == "--" {
			progArgs = args[i+1:]
			break
		}
		if a == "--interp" {
			useInterp = true
		} else {
			paths = append(paths, a)
		}
	}
	if len(paths) != 1 {
		fmt.Fprintln(stderr, "usage: navi run [--interp] <crate or file> [-- args...]")
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
	if !useInterp {
		return runNative(info, main, progArgs, stdout, stderr)
	}
	m := interp.New(mir.Build(info))
	m.Stdout, m.Stderr = stdout, stderr
	m.Args = append([]string{paths[0]}, progArgs...)
	return m.Run(main)
}

// runNative builds a temporary executable and runs it.
func runNative(info *types.Info, main *resolve.Def, args []string, stdout, stderr io.Writer) int {
	dir, err := os.MkdirTemp("", "navi-run")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, "main")
	if err := build.Native(info, main, exe, build.HostTarget(), true); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				fmt.Fprintf(stderr, "navi run: program killed by %v\n", ws.Signal())
				return 128 + int(ws.Signal())
			}
			return ee.ExitCode()
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// buildCrate compiles a crate into a native executable.
func buildCrate(args []string, stdout, stderr io.Writer) int {
	out := ""
	release, objOnly := false, false
	target := build.HostTarget()
	var paths []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o":
			if i+1 < len(args) {
				out = args[i+1]
				i++
			}
		case "--release":
			release = true
		case "-c":
			objOnly = true
		case "--target":
			if i+1 < len(args) {
				t, err := build.ParseTarget(args[i+1])
				if err != nil {
					fmt.Fprintln(stderr, err)
					return 2
				}
				target = t
				i++
			}
		default:
			paths = append(paths, args[i])
		}
	}
	if len(paths) != 1 {
		fmt.Fprintln(stderr, "usage: navi build [-o output] [--release] [--target name] [-c] <crate or file>")
		return 2
	}
	spec, err := build.LoadCrate(paths[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	info, errs := build.CheckSpecs(target.Config(!release), []*resolve.CrateSpec{build.StdCrate(), spec})
	if len(errs) > 0 {
		fmt.Fprintln(stderr, errs)
		return 1
	}
	main := build.FindMain(info, spec.Name)
	if main == nil {
		fmt.Fprintf(stderr, "%s: no main function\n", paths[0])
		return 1
	}
	if objOnly {
		if out == "" {
			out = spec.Name + ".o"
		}
		data, err := build.Object(info, main, target, !release)
		if err == nil {
			err = os.WriteFile(out, data, 0o644)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if out == "" {
		out = spec.Name
		if target.OS == "windows" {
			out += ".exe"
		}
	}
	if err := build.Native(info, main, out, target, !release); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// testCrate builds a crate with cfg(test) and runs every #[test] function
// on the interpreter.
func testCrate(paths []string, stdout, stderr io.Writer) int {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	spec, err := build.LoadCrate(paths[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	cfg := build.HostConfig()
	cfg.Test = true
	info, errs := build.CheckSpecs(cfg, []*resolve.CrateSpec{build.StdCrate(), spec})
	if len(errs) > 0 {
		fmt.Fprintln(stderr, errs)
		return 1
	}
	prog := mir.Build(info)
	var tests []*resolve.Def
	for _, d := range info.Res.Defs {
		if d.Crate.Name != spec.Name || d.Kind != resolve.DefFn {
			continue
		}
		if fn, ok := d.Node.(*syntax.FnDecl); ok {
			for _, a := range fn.Attrs {
				if len(a.Path.Segments) == 1 && a.Path.Segments[0].Name.Value == "test" {
					tests = append(tests, d)
				}
			}
		}
	}
	failed := 0
	for _, d := range tests {
		m := interp.New(prog)
		var out strings.Builder
		m.Stdout, m.Stderr = &out, &out
		if code := m.Run(d); code != 0 {
			failed++
			fmt.Fprintf(stdout, "--- FAIL: %s\n%s", d.Name, out.String())
		} else {
			fmt.Fprintf(stdout, "ok   %s\n", d.Name)
		}
	}
	fmt.Fprintf(stdout, "%d passed, %d failed\n", len(tests)-failed, failed)
	if failed > 0 {
		return 1
	}
	return 0
}
