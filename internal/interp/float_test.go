package interp

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"navi/internal/build"
	"navi/internal/mir"
	"navi/internal/resolve"
)

// TestNativeFloat checks std/src/rt/float.navi (the native float
// formatter) against the interpreter's strconv-based formatting.
func TestNativeFloat(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "lib", "std", "src", "rt", "float.navi"))
	if err != nil {
		t.Fatal(err)
	}
	vals := []float64{0, math.Copysign(0, -1), 1, -1, 0.1, 0.3, 1.5, 2.5, 0.5, 9.5, 100, 1e21, 1e22, 1e23,
		123456789.125, 5e-324, math.MaxFloat64, 3.14159, 2.0 / 3.0, 1e-7, 0.000123, 1.0 / 3.0,
		2.2250738585072014e-308, 0.125, 1234.5678, 1e15, 9.999999999999999e22, 4.35, 0.045}
	f32s := []float32{0.1, 7.0685773, 16777216, 3.4028235e38, 1e-45, 1.5, 0.3}
	type cse struct {
		x       float64
		prec    int
		hasPrec bool
		style   byte
	}
	var cases []cse
	for _, v := range vals {
		for style := byte(0); style < 4; style++ {
			cases = append(cases, cse{v, 0, false, style})
			for _, p := range []int{0, 2, 17} {
				cases = append(cases, cse{v, p, true, style})
			}
		}
	}
	for _, v := range f32s {
		for style := byte(0); style < 4; style++ {
			cases = append(cases, cse{float64(v), 0, false, style + 16})
			cases = append(cases, cse{float64(v), 3, true, style + 16})
		}
	}
	var b strings.Builder
	b.Write(src)
	b.WriteString(`
fn show(x: f64, prec: usize, has: bool, style: u8) {
    let mut buf = [0u8; 1100];
    let n = format(x, prec, has, style, &mut buf as *mut [u8; 1100] as *mut u8);
    let mut s = String::new();
    let mut i = 0;
    while i < n { s.push(buf[i] as char); i += 1; }
    println(s);
}

fn main() {
`)
	var want []string
	for _, c := range cases {
		lit := strconv.FormatFloat(c.x, 'e', -1, 64)
		if c.x == 0 && math.Signbit(c.x) {
			lit = "-0.0"
		}
		fmt.Fprintf(&b, "    show(%s, %d, %v, %d);\n", lit, c.prec, c.hasPrec, c.style)
		bits := 64
		style := c.style
		if style >= 16 {
			bits, style = 32, style-16
		}
		want = append(want, formatFloatBits(c.x, c.prec, c.hasPrec, style, bits))
	}
	b.WriteString("}\n")
	dir := t.TempDir()
	file := filepath.Join(dir, "main.navi")
	os.WriteFile(file, []byte(b.String()), 0o644)
	spec, err := build.LoadCrate(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg := resolve.Config{TargetOS: "linux", TargetArch: "x86_64", Debug: true}
	info, errs := build.CheckSpecs(cfg, []*resolve.CrateSpec{build.StdCrate(), spec})
	if len(errs) > 0 {
		t.Fatalf("compile errors:\n%v", errs)
	}
	main := info.Res.Crates[1].Root.Lookup("main", resolve.ValueNS).Res.Def
	m := New(mir.Build(info))
	var stdout, stderr bytes.Buffer
	m.Stdout, m.Stderr = &stdout, &stderr
	if code := m.Run(main); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	got := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	bad := 0
	for i, c := range cases {
		g := ""
		if i < len(got) {
			g = got[i]
		}
		if g != want[i] {
			bad++
			if bad < 30 {
				t.Errorf("format(%v, prec %d/%v, style %d) = %q, want %q", c.x, c.prec, c.hasPrec, c.style, g, want[i])
			}
		}
	}
}
