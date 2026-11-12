package codegen_test

import (
	"path/filepath"
	"testing"

	"navi/internal/build"
	"navi/internal/codegen"
	"navi/internal/mir"
	"navi/internal/resolve"
)

// TestPlanGolden plans every golden program and checks that resolution
// finds a target for every reference.
func TestPlanGolden(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "tests", "run", "*.navi"))
	for _, f := range files {
		spec, err := build.LoadCrate(f)
		if err != nil {
			t.Fatal(err)
		}
		info, errs := build.CheckSpecs(resolve.Config{TargetOS: "macos", TargetArch: "aarch64", Debug: true}, []*resolve.CrateSpec{build.StdCrate(), spec})
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		main := info.Res.Crates[1].Root.Lookup("main", resolve.ValueNS).Res.Def
		p := codegen.Build(mir.Build(info), main)
		if len(p.Errors) > 0 {
			t.Errorf("%s: %v", f, p.Errors)
		}
		shared := 0
		for _, g := range p.Groups {
			if len(g.Insts) > 1 {
				shared++
			}
		}
		t.Logf("%s: %d instances in %d groups (%d shared), %d vtables", filepath.Base(f), len(p.Insts), len(p.Groups), shared, len(p.Vtables))
	}
}
