package build

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"navi/internal/resolve"
)

// TestFloatParse checks std's float parsing (str::parse::<f64>) bit for
// bit against strconv.ParseFloat, natively.
func TestFloatParse(t *testing.T) {
	if !NativeSupported() {
		t.Skip("native code generation is not available on this host")
	}
	r := rand.New(rand.NewSource(1))
	inputs := []string{"0", "-0", "1", "0.1", "0.3", "1e23", "9007199254740993", "2.2250738585072011e-308",
		"2.2250738585072014e-308", "4.9e-324", "2.4e-324", "2.5e-324", "1.7976931348623157e308", "1.7976931348623159e308",
		"123.456e-7", "3.14159265358979323846264338327950288", "1_000.5", "6.02214076e23", "1e-400", "1e400",
		"0.000001", "4503599627370496.5", "4503599627370497.5", "9007199254740992.0001"}
	for i := 0; i < 300; i++ {
		f := math.Float64frombits(r.Uint64())
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		inputs = append(inputs, strconv.FormatFloat(f, 'e', -1, 64))
		inputs = append(inputs, strconv.FormatFloat(f, 'e', r.Intn(20), 64))
	}
	for i := 0; i < 100; i++ {
		inputs = append(inputs, fmt.Sprintf("%d.%de%d", r.Intn(100000), r.Intn(1000000), r.Intn(60)-30))
	}
	var b strings.Builder
	b.WriteString("fn main() {\n")
	for _, s := range inputs {
		fmt.Fprintf(&b, "    println(\"%s\".parse::<f64>().unwrap().to_bits());\n", s)
	}
	b.WriteString("}\n")
	dir := t.TempDir()
	file := filepath.Join(dir, "main.navi")
	os.WriteFile(file, []byte(b.String()), 0o644)
	spec, err := LoadCrate(file)
	if err != nil {
		t.Fatal(err)
	}
	target := HostTarget()
	info, errs := CheckSpecs(target.Config(true), []*resolve.CrateSpec{StdCrate(), spec})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	exe := filepath.Join(dir, "prog")
	if err := Native(info, FindMain(info, spec.Name), exe, target, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i, s := range inputs {
		want, _ := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
		if i >= len(lines) || lines[i] != strconv.FormatUint(math.Float64bits(want), 10) {
			got := ""
			if i < len(lines) {
				g, _ := strconv.ParseUint(lines[i], 10, 64)
				got = strconv.FormatFloat(math.Float64frombits(g), 'e', -1, 64)
			}
			t.Errorf("parse(%q) = %s, want %s", s, got, strconv.FormatFloat(want, 'e', -1, 64))
		}
	}
}
