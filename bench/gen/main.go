// Command gen writes a synthetic project of about N lines in navi and the
// equivalent Go program, for comparing compile times (plan section 6).
//
//	go run ./bench/gen -lines 50000 -out /tmp/benchproj
//
// writes /tmp/benchproj/navi/src/main.navi (+ modules) and
// /tmp/benchproj/go/{go.mod,*.go}.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	lines := flag.Int("lines", 10000, "approximate number of source lines")
	out := flag.String("out", "benchproj", "output directory")
	flag.Parse()
	// Each unit (one struct, one enum, a few functions) is about 60 lines.
	units := *lines / 60
	if units < 1 {
		units = 1
	}
	perMod := 20
	nmods := (units + perMod - 1) / perMod
	naviDir := filepath.Join(*out, "navi", "src")
	goDir := filepath.Join(*out, "go")
	must(os.MkdirAll(naviDir, 0o755))
	must(os.MkdirAll(goDir, 0o755))
	must(os.WriteFile(filepath.Join(*out, "navi", "navi.toml"), []byte("[package]\nname = \"benchproj\"\n"), 0o644))
	must(os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module benchproj\n\ngo 1.22\n"), 0o644))

	var naviMain, goMain strings.Builder
	for m := 0; m < nmods; m++ {
		fmt.Fprintf(&naviMain, "mod m%d;\n", m)
	}
	naviMain.WriteString("\nfn main() {\n    let mut total = 0i64;\n")
	goMain.WriteString("package main\n\nimport \"fmt\"\n\nfunc main() {\n\ttotal := int64(0)\n")
	u := 0
	for m := 0; m < nmods; m++ {
		var nv, gv strings.Builder
		nv.WriteString("use std::collections::HashMap;\n\n")
		for i := 0; i < perMod && u < units; i++ {
			naviUnit(&nv, u)
			goUnit(&gv, u)
			fmt.Fprintf(&naviMain, "    total += m%d::run%d(%d);\n", m, u, u%7+3)
			fmt.Fprintf(&goMain, "\ttotal += run%d(%d)\n", u, u%7+3)
			u++
		}
		must(os.WriteFile(filepath.Join(naviDir, fmt.Sprintf("m%d.navi", m)), []byte(nv.String()), 0o644))
		must(os.WriteFile(filepath.Join(goDir, fmt.Sprintf("m%d.go", m)), []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nvar _ = strings.Repeat\nvar _ = fmt.Sprint\n\n"+gv.String()), 0o644))
	}
	naviMain.WriteString("    println(`total ${total}`);\n}\n")
	goMain.WriteString("\tfmt.Printf(\"total %d\\n\", total)\n}\n")
	must(os.WriteFile(filepath.Join(naviDir, "main.navi"), []byte(naviMain.String()), 0o644))
	must(os.WriteFile(filepath.Join(goDir, "main.go"), []byte(goMain.String()), 0o644))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// naviUnit writes a struct, an enum and functions using loops, vectors,
// maps, matching, closures and string formatting.
func naviUnit(b *strings.Builder, u int) {
	fmt.Fprintf(b, `#[derive(Clone, Debug)]
pub struct Point%[1]d {
    pub x: i64,
    pub y: i64,
    pub name: String,
}

pub enum Shape%[1]d {
    Circle(i64),
    Rect { w: i64, h: i64 },
    Empty,
}

impl Point%[1]d {
    pub fn new(x: i64, y: i64) -> Point%[1]d {
        Point%[1]d { x, y, name: `+"`p${x}`"+` }
    }

    pub fn dist2(&self, o: &Point%[1]d) -> i64 {
        let dx = self.x - o.x;
        let dy = self.y - o.y;
        dx * dx + dy * dy
    }
}

fn area%[1]d(s: &Shape%[1]d) -> i64 {
    match s {
        Shape%[1]d::Circle(r) => 3 * r * r,
        Shape%[1]d::Rect { w, h } => w * h,
        Shape%[1]d::Empty => 0,
    }
}

pub fn run%[1]d(n: i64) -> i64 {
    let mut pts: Vec<Point%[1]d> = Vec::new();
    let mut i = 0;
    while i < n {
        pts.push(Point%[1]d::new(i, i * %[2]d));
        i += 1;
    }
    let mut seen: HashMap<String, i64> = HashMap::new();
    let mut sum = 0i64;
    for p in pts.iter() {
        sum += p.dist2(&pts[0]);
        seen.insert(p.name.clone(), p.x);
    }
    let shapes = [Shape%[1]d::Circle(n), Shape%[1]d::Rect { w: n, h: 2 }, Shape%[1]d::Empty];
    for s in shapes.iter() {
        sum += area%[1]d(s);
    }
    let doubled: Vec<i64> = pts.iter().map(|p| p.x * 2).filter(|x| *x > 1).collect();
    sum + doubled.len() as i64 + seen.len() as i64
}

`, u, u%5+1)
}

func goUnit(b *strings.Builder, u int) {
	fmt.Fprintf(b, `type Point%[1]d struct {
	X, Y int64
	Name string
}

type Shape%[1]d interface{ area() int64 }

type Circle%[1]d struct{ R int64 }
type Rect%[1]d struct{ W, H int64 }
type Empty%[1]d struct{}

func (c Circle%[1]d) area() int64 { return 3 * c.R * c.R }
func (r Rect%[1]d) area() int64   { return r.W * r.H }
func (Empty%[1]d) area() int64    { return 0 }

func NewPoint%[1]d(x, y int64) Point%[1]d {
	return Point%[1]d{X: x, Y: y, Name: fmt.Sprintf("p%%d", x)}
}

func (p *Point%[1]d) Dist2(o *Point%[1]d) int64 {
	dx := p.X - o.X
	dy := p.Y - o.Y
	return dx*dx + dy*dy
}

func run%[1]d(n int64) int64 {
	var pts []Point%[1]d
	for i := int64(0); i < n; i++ {
		pts = append(pts, NewPoint%[1]d(i, i*%[2]d))
	}
	seen := map[string]int64{}
	sum := int64(0)
	for i := range pts {
		sum += pts[i].Dist2(&pts[0])
		seen[pts[i].Name] = pts[i].X
	}
	shapes := []Shape%[1]d{Circle%[1]d{n}, Rect%[1]d{n, 2}, Empty%[1]d{}}
	for _, s := range shapes {
		sum += s.area()
	}
	var doubled []int64
	for _, p := range pts {
		if x := p.X * 2; x > 1 {
			doubled = append(doubled, x)
		}
	}
	return sum + int64(len(doubled)) + int64(len(seen))
}

`, u, u%5+1)
}
