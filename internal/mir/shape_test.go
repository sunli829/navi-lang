package mir_test

import (
	"testing"
	"testing/fstest"

	"navi/internal/build"
	"navi/internal/mir"
	"navi/internal/resolve"
)

// TestShapeSharing checks the M2 acceptance criterion: Vec<&A> and Vec<&B>
// share one instance of each Vec method (with two dictionaries), while
// Vec<i32> and Vec<f64> get instances of their own.
func TestShapeSharing(t *testing.T) {
	src := `
struct A { x: i32 }
struct B { y: f64 }
fn main() {
    let a = A { x: 1 };
    let b = B { y: 2.0 };
    let mut va: Vec<&A> = Vec::new();
    va.push(&a);
    let mut vb: Vec<&B> = Vec::new();
    vb.push(&b);
    let mut vi: Vec<i32> = Vec::new();
    vi.push(1);
    let mut vf: Vec<f64> = Vec::new();
    vf.push(1.0);
}
`
	spec := &resolve.CrateSpec{Name: "app", FS: fstest.MapFS{"main.navi": {Data: []byte(src)}}, Root: "main.navi"}
	info, errs := build.CheckSpecs(resolve.Config{TargetOS: "linux", TargetArch: "x86_64", Debug: true}, []*resolve.CrateSpec{build.StdCrate(), spec})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	prog := mir.Build(info)
	main := info.Res.Crates[1].Root.Lookup("main", resolve.ValueNS).Res.Def
	insts := prog.CollectInstances(main)
	push := map[int]int{} // dictionaries per instance of Vec::push
	for _, in := range insts.ByKey {
		if in.Def.Name == "push" && in.Def.Crate.IsStd && in.Def.Parent.Kind == resolve.DefImpl {
			push[len(in.Dicts)]++
		}
	}
	// One pointer-shaped instance with 2 dictionaries (&A, &B), and one
	// instance each for i32 and f64.
	if push[2] != 1 || push[1] != 2 {
		t.Fatalf("Vec::push instances by dictionary count = %v, want {2:1, 1:2}\n%v", push, insts.Summary())
	}
}
