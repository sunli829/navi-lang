package syntax

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkParse parses the syntax corpus repeated into one large file and
// reports throughput in source bytes and lines.
func BenchmarkParse(b *testing.B) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "tests", "syntax", "*.navi"))
	var one bytes.Buffer
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}
		// Inner attributes are only valid at the start of a file.
		for _, line := range bytes.SplitAfter(src, []byte("\n")) {
			if !bytes.HasPrefix(line, []byte("#![")) {
				one.Write(line)
			}
		}
		one.WriteByte('\n')
	}
	src := bytes.Repeat(one.Bytes(), 200)
	lines := bytes.Count(src, []byte("\n"))
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, errs := ParseFile(NewFile("bench.navi", src))
		if errs.Len() > 0 {
			b.Fatal(errs)
		}
	}
	b.ReportMetric(float64(lines)*float64(b.N)/b.Elapsed().Seconds(), "lines/s")
}
