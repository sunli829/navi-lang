package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"navi/internal/backend/arm64"
	"navi/internal/codegen"
	"navi/internal/mir"
	"navi/internal/resolve"
	"navi/internal/types"
)

// NativeSupported reports whether the host can build native binaries.
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

// Native compiles a checked program into the executable out: MIR, code
// generation plan, machine code, object file, then the system linker.
func Native(info *types.Info, main *resolve.Def, out string, debug bool) error {
	if !NativeSupported() {
		return fmt.Errorf("native code generation supports darwin/arm64 only for now (M5 adds the other targets)")
	}
	prog := mir.Build(info)
	plan := codegen.Build(prog, main)
	obj, err := arm64.Generate(plan, arm64.Options{Debug: debug})
	if err != nil {
		return err
	}
	data, err := obj.Bytes()
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
	cmd := exec.Command("cc", "-arch", "arm64", "-o", out, objPath)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("link: %v\n%s", err, msg)
	}
	return nil
}
