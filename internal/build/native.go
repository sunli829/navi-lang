package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"navi/internal/backend/arm64"
	"navi/internal/backend/lower"
	"navi/internal/codegen"
	"navi/internal/mir"
	"navi/internal/obj"
	"navi/internal/obj/macho"
	"navi/internal/resolve"
	"navi/internal/types"
)

// Target is a platform navi compiles for.
type Target struct {
	OS   string // "macos", "linux", "windows"
	Arch string // "arm64", "amd64"
}

// Targets lists the supported targets by name.
var Targets = map[string]Target{
	"macos-arm64": {"macos", "arm64"},
	"macos-x64":   {"macos", "amd64"},
	"linux-x64":   {"linux", "amd64"},
	"linux-arm64": {"linux", "arm64"},
	"windows-x64": {"windows", "amd64"},
}

func (t Target) String() string {
	arch := t.Arch
	if arch == "amd64" {
		arch = "x64"
	}
	return t.OS + "-" + arch
}

// ParseTarget parses a target name such as "linux-x64".
func ParseTarget(name string) (Target, error) {
	if t, ok := Targets[name]; ok {
		return t, nil
	}
	var names []string
	for n := range Targets {
		names = append(names, n)
	}
	return Target{}, fmt.Errorf("unknown target %q (supported: %s)", name, strings.Join(names, ", "))
}

// HostTarget is the machine navi runs on.
func HostTarget() Target {
	os := runtime.GOOS
	if os == "darwin" {
		os = "macos"
	}
	return Target{os, runtime.GOARCH}
}

// Config returns the #[cfg] settings of the target.
func (t Target) Config(debug bool) resolve.Config {
	arch := "x86_64"
	if t.Arch == "arm64" {
		arch = "aarch64"
	}
	return resolve.Config{TargetOS: t.OS, TargetArch: arch, Debug: debug}
}

// NativeSupported reports whether the host can build and run native
// binaries for itself.
func NativeSupported() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" }

// FindMain returns the main function of the crate named name.
func FindMain(info *types.Info, name string) *resolve.Def {
	for _, c := range info.Res.Crates {
		if c.Name != name {
			continue
		}
		if b := c.Root.Lookup("main", resolve.ValueNS); b != nil && b.Res.Def != nil && b.Res.Def.Kind == resolve.DefFn {
			return b.Res.Def
		}
	}
	return nil
}

// Object compiles a checked program into an object file for the target.
func Object(info *types.Info, main *resolve.Def, t Target, debug bool) ([]byte, error) {
	prog := mir.Build(info)
	plan := codegen.Build(prog, main)
	var m lower.Machine
	switch t.Arch {
	case "arm64":
		m = arm64.NewMachine()
	default:
		return nil, fmt.Errorf("code generation for %s is not implemented", t)
	}
	tgt := lower.Target{}
	if t.OS == "macos" {
		tgt.CPrefix = "_"
	}
	o, err := lower.Generate(plan, t.Arch, m, tgt, lower.Options{Debug: debug})
	if err != nil {
		return nil, err
	}
	return writeObject(o, t)
}

func writeObject(o *obj.Object, t Target) ([]byte, error) {
	switch t.OS {
	case "macos":
		return macho.Write(o)
	}
	return nil, fmt.Errorf("object files for %s are not implemented", t)
}

// Native compiles a checked program into the executable out for the
// target: MIR, code generation plan, machine code, object file, then the
// system linker.
func Native(info *types.Info, main *resolve.Def, out string, t Target, debug bool) error {
	data, err := Object(info, main, t, debug)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "navi-build")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	objPath := filepath.Join(dir, "main.o")
	if err := os.WriteFile(objPath, data, 0o644); err != nil {
		return err
	}
	return link(objPath, out, t)
}

// link runs the system linker (through the C compiler driver).
func link(objPath, out string, t Target) error {
	host := HostTarget()
	var cmd *exec.Cmd
	switch {
	case t.OS == "macos" && host.OS == "macos":
		arch := "arm64"
		if t.Arch == "amd64" {
			arch = "x86_64"
		}
		cmd = exec.Command("cc", "-arch", arch, "-o", out, objPath)
	case t == host:
		cmd = exec.Command("cc", "-o", out, objPath, "-lm")
	default:
		return fmt.Errorf("cross-linking for %s from %s needs a cross linker; the object file was generated (use -c)", t, host)
	}
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("link: %v\n%s", err, msg)
	}
	return nil
}
