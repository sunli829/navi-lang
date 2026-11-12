package macho

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLinkMinimal writes an object with a main that calls exit(42) and
// links and runs it (arm64 macOS only).
func TestLinkMinimal(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("needs arm64 macOS")
	}
	o := New("arm64")
	o.Define("_main", Text, 4, true)
	put := func(ins uint32) { o.Sects[Text] = binary.LittleEndian.AppendUint32(o.Sects[Text], ins) }
	// adrp x1, msg@PAGE ; add x1, x1, msg@PAGEOFF ; mov x0, #1 ; mov x2, #3 ; bl _write
	o.Reloc(Text, uint32(len(o.Sects[Text])), Page21, "_msg")
	put(0x90000001)
	o.Reloc(Text, uint32(len(o.Sects[Text])), PageOff12, "_msg")
	put(0x91000021)
	put(0xd2800020) // mov x0, #1
	put(0xd2800062) // mov x2, #3
	o.Reloc(Text, uint32(len(o.Sects[Text])), Branch26, "_write")
	put(0x94000000)
	put(0xd2800540) // mov x0, #42
	o.Reloc(Text, uint32(len(o.Sects[Text])), Branch26, "_exit")
	put(0x94000000)
	o.Undefined("_write")
	o.Undefined("_exit")
	o.Define("_msg", RoData, 1, false)
	o.Sects[RoData] = append(o.Sects[RoData], "hi\n"...)
	data, err := o.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	obj := filepath.Join(dir, "a.o")
	if err := os.WriteFile(obj, data, 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "a.out")
	if out, err := exec.Command("cc", "-o", exe, obj).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	out, err := exec.Command(exe).Output()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
		t.Fatalf("exit: %v", err)
	}
	if string(out) != "hi\n" {
		t.Fatalf("output %q", out)
	}
}
