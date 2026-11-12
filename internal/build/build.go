// Package build turns command-line arguments into crates and runs the
// compiler phases over them.
package build

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"navi"
	"navi/internal/diag"
	"navi/internal/resolve"
	"navi/internal/types"
)

// HostConfig returns the #[cfg] settings for the machine navi runs on.
func HostConfig() resolve.Config {
	os := runtime.GOOS
	if os == "darwin" {
		os = "macos"
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	return resolve.Config{TargetOS: os, TargetArch: arch, Debug: true}
}

// StdCrate returns the spec of the embedded standard library.
func StdCrate() *resolve.CrateSpec {
	return &resolve.CrateSpec{Name: "std", FS: navi.StdFS(), Root: "src/lib.navi"}
}

// LoadCrate describes the crate at path: a directory containing navi.toml
// and src/main.navi or src/lib.navi, or a single .navi file.
func LoadCrate(path string) (*resolve.CrateSpec, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if filepath.Ext(path) != ".navi" {
			return nil, fmt.Errorf("%s: not a .navi file or a crate directory", path)
		}
		name := strings.TrimSuffix(filepath.Base(path), ".navi")
		return &resolve.CrateSpec{Name: crateName(name), FS: os.DirFS(filepath.Dir(path)), Root: filepath.Base(path), Dir: filepath.Dir(path)}, nil
	}
	fsys := os.DirFS(path)
	name := filepath.Base(path)
	if abs, err := filepath.Abs(path); err == nil {
		name = filepath.Base(abs)
	}
	if manifest, err := fs.ReadFile(fsys, "navi.toml"); err == nil {
		if n := packageName(manifest); n != "" {
			name = n
		}
	}
	for _, root := range []string{"src/main.navi", "src/lib.navi"} {
		if _, err := fs.Stat(fsys, root); err == nil {
			return &resolve.CrateSpec{Name: crateName(name), FS: fsys, Root: root, Dir: path}, nil
		}
	}
	return nil, fmt.Errorf("%s: no src/main.navi or src/lib.navi", path)
}

func crateName(s string) string { return strings.ReplaceAll(s, "-", "_") }

// packageName reads `name = "..."` from the [package] table of navi.toml.
// Full TOML support comes with dependencies.
func packageName(manifest []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(manifest))
	inPackage := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inPackage = line == "[package]"
			continue
		}
		if !inPackage {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "name" {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

// Check loads the crate at path together with std and runs every phase
// that exists so far (name resolution and signature collection).
func Check(path string, cfg resolve.Config) (diag.List, error) {
	spec, err := LoadCrate(path)
	if err != nil {
		return nil, err
	}
	_, errs := CheckSpecs(cfg, []*resolve.CrateSpec{StdCrate(), spec})
	return errs, nil
}

// CheckSpecs runs the front end over the given crates.
func CheckSpecs(cfg resolve.Config, specs []*resolve.CrateSpec) (*types.Info, diag.List) {
	res, rerrs := resolve.Resolve(cfg, specs)
	var sink diag.Sink
	for _, e := range rerrs {
		sink.Add(e)
	}
	info := types.Collect(res, &sink)
	return info, sink.List()
}
