package arm64

import (
	"debug/macho"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEncoding compares the encoder with the system assembler.
func TestEncoding(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("needs the macOS assembler")
	}
	cases := []struct {
		text string
		emit func(a *Asm)
	}{
		{"add x10, x29, #16", func(a *Asm) { a.AddImm(X10, FP, 16) }},
		{"add sp, sp, #4096", func(a *Asm) { a.AddImm(SP, SP, 4096) }},
		{"sub sp, sp, #48", func(a *Asm) { a.SubImm(SP, SP, 48) }},
		{"mov x29, sp", func(a *Asm) { a.MovSP(FP, SP) }},
		{"cmp x12, #10", func(a *Asm) { a.CmpImm(X12, 10) }},
		{"add x12, x12, x13", func(a *Asm) { a.Add(X12, X12, X13) }},
		{"sub x12, x12, x13", func(a *Asm) { a.Sub(X12, X12, X13) }},
		{"adds x12, x12, x13", func(a *Asm) { a.Adds(X12, X12, X13) }},
		{"subs x12, x12, x13", func(a *Asm) { a.Subs(X12, X12, X13) }},
		{"cmp x12, x13", func(a *Asm) { a.Cmp(X12, X13) }},
		{"cmp x15, x14, asr #63", func(a *Asm) { a.CmpAsr63(X15, X14) }},
		{"add x10, sp, x16", func(a *Asm) { a.AddExt(X10, SP, X16) }},
		{"sub sp, sp, x16", func(a *Asm) { a.SubExt(SP, SP, X16) }},
		{"mul x12, x12, x13", func(a *Asm) { a.Mul(X12, X12, X13) }},
		{"madd x10, x16, x17, x10", func(a *Asm) { a.Madd(X10, X16, X17, X10) }},
		{"msub x12, x14, x13, x12", func(a *Asm) { a.Msub(X12, X14, X13, X12) }},
		{"smulh x15, x12, x13", func(a *Asm) { a.Smulh(X15, X12, X13) }},
		{"umulh x15, x12, x13", func(a *Asm) { a.Umulh(X15, X12, X13) }},
		{"sdiv x12, x12, x13", func(a *Asm) { a.Sdiv(X12, X12, X13) }},
		{"udiv x12, x12, x13", func(a *Asm) { a.Udiv(X12, X12, X13) }},
		{"lsl x12, x12, x13", func(a *Asm) { a.Lslv(X12, X12, X13) }},
		{"lsr x12, x12, x13", func(a *Asm) { a.Lsrv(X12, X12, X13) }},
		{"asr x12, x12, x13", func(a *Asm) { a.Asrv(X12, X12, X13) }},
		{"and x12, x12, x13", func(a *Asm) { a.And(X12, X12, X13) }},
		{"orr x12, x12, x13", func(a *Asm) { a.Orr(X12, X12, X13) }},
		{"eor x12, x12, x13", func(a *Asm) { a.Eor(X12, X12, X13) }},
		{"mvn x12, x13", func(a *Asm) { a.Mvn(X12, X13) }},
		{"mov x0, x12", func(a *Asm) { a.Mov(X0, X12) }},
		{"csel x12, x13, x14, lt", func(a *Asm) { a.Csel(X12, X13, X14, LT) }},
		{"cset x12, ge", func(a *Asm) { a.Cset(X12, GE) }},
		{"sxtb x12, w12", func(a *Asm) { a.SignExt(X12, X12, 1) }},
		{"sxth x12, w13", func(a *Asm) { a.SignExt(X12, X13, 2) }},
		{"sxtw x12, w13", func(a *Asm) { a.SignExt(X12, X13, 4) }},
		{"ubfx x12, x13, #0, #8", func(a *Asm) { a.ZeroExt(X12, X13, 1) }},
		{"ubfx x12, x13, #0, #32", func(a *Asm) { a.ZeroExt(X12, X13, 4) }},
		{"lsr x12, x13, #8", func(a *Asm) { a.LsrImm(X12, X13, 8) }},
		{"lsl x12, x13, #3", func(a *Asm) { a.LslImm(X12, X13, 3) }},
		{"mov x12, #0", func(a *Asm) { a.MovImm(X12, 0) }},
		{"mov x12, #-1", func(a *Asm) { a.MovImm(X12, -1) }},
		{"mov x12, #-100", func(a *Asm) { a.MovImm(X12, -100) }},
		{"movz x12, #0x5678\nmovk x12, #0x1234, lsl #16", func(a *Asm) { a.MovImm(X12, 0x12345678) }},
		{"movz x12, #0x1, lsl #48", func(a *Asm) { a.MovImm(X12, 1<<48) }},
		{"ldrb w12, [x10, #3]", func(a *Asm) { a.Ldr(1, X12, X10, 3) }},
		{"ldrh w12, [x10, #6]", func(a *Asm) { a.Ldr(2, X12, X10, 6) }},
		{"ldr w12, [x10, #8]", func(a *Asm) { a.Ldr(4, X12, X10, 8) }},
		{"ldr x12, [x10, #16]", func(a *Asm) { a.Ldr(8, X12, X10, 16) }},
		{"ldur x12, [x29, #-8]", func(a *Asm) { a.Ldr(8, X12, FP, -8) }},
		{"ldur w12, [x10, #3]", func(a *Asm) { a.Ldr(4, X12, X10, 3) }},
		{"strb w12, [x10, #3]", func(a *Asm) { a.Str(1, X12, X10, 3) }},
		{"strh w12, [x10]", func(a *Asm) { a.Str(2, X12, X10, 0) }},
		{"str w12, [sp, #4]", func(a *Asm) { a.Str(4, X12, SP, 4) }},
		{"str x12, [sp, #8]", func(a *Asm) { a.Str(8, X12, SP, 8) }},
		{"stur x12, [x29, #-40]", func(a *Asm) { a.Str(8, X12, FP, -40) }},
		{"sturb w12, [x29, #-1]", func(a *Asm) { a.Str(1, X12, FP, -1) }},
		{"sub x17, x29, #1000\nldr x12, [x17]", func(a *Asm) { a.Ldr(8, X12, FP, -1000) }},
		{"stp x29, x30, [sp, #-16]!", func(a *Asm) { a.StpPre(FP, LR, SP, -16) }},
		{"ldp x29, x30, [sp], #16", func(a *Asm) { a.LdpPost(FP, LR, SP, 16) }},
		{"ldr x14, [x16], #8", func(a *Asm) { a.LdrPost(X14, X16, 8) }},
		{"str x14, [x17], #8", func(a *Asm) { a.StrPost(X14, X17, 8) }},
		{"subs x13, x13, #1", func(a *Asm) { a.SubsImm(X13, X13, 1) }},
		{"cmn x13, #1", func(a *Asm) { a.CmnImm(X13, 1) }},
		{"sub x10, x29, #200", func(a *Asm) { a.AddrOf(X10, FP, -200) }},
		{"mov x10, #5000\nadd x10, x29, x10, uxtx", func(a *Asm) { a.AddrOf(X10, FP, 5000) }},
		{"blr x16", func(a *Asm) { a.Blr(X16) }},
		{"br x16", func(a *Asm) { a.Br(X16) }},
		{"ret", func(a *Asm) { a.Ret() }},
		{"brk #1", func(a *Asm) { a.Brk(1) }},
		{"fmov d0, x12", func(a *Asm) { a.FmovToF(0, X12, true) }},
		{"fmov s0, w12", func(a *Asm) { a.FmovToF(0, X12, false) }},
		{"fmov x12, d0", func(a *Asm) { a.FmovFromF(X12, 0, true) }},
		{"fmov w12, s0", func(a *Asm) { a.FmovFromF(X12, 0, false) }},
		{"fadd d0, d0, d1", func(a *Asm) { a.Fadd(0, 0, 1, true) }},
		{"fsub s0, s0, s1", func(a *Asm) { a.Fsub(0, 0, 1, false) }},
		{"fmul d0, d0, d1", func(a *Asm) { a.Fmul(0, 0, 1, true) }},
		{"fdiv d0, d0, d1", func(a *Asm) { a.Fdiv(0, 0, 1, true) }},
		{"fneg d0, d1", func(a *Asm) { a.Fneg(0, 1, true) }},
		{"fneg s0, s1", func(a *Asm) { a.Fneg(0, 1, false) }},
		{"fcmp d0, d1", func(a *Asm) { a.Fcmp(0, 1, true) }},
		{"fcmp s0, s1", func(a *Asm) { a.Fcmp(0, 1, false) }},
		{"fcvt d0, s1", func(a *Asm) { a.FcvtDS(0, 1) }},
		{"fcvt s0, d1", func(a *Asm) { a.FcvtSD(0, 1) }},
		{"scvtf d0, x12", func(a *Asm) { a.Scvtf(0, X12, true) }},
		{"ucvtf s0, x12", func(a *Asm) { a.Ucvtf(0, X12, false) }},
		{"fcvtzs x12, d0", func(a *Asm) { a.Fcvtzs(X12, 0, true) }},
		{"fcvtzu x12, s0", func(a *Asm) { a.Fcvtzu(X12, 0, false) }},
	}
	var src strings.Builder
	var want [][]uint32
	for _, c := range cases {
		src.WriteString(c.text + "\n")
		var a Asm
		c.emit(&a)
		want = append(want, a.Code)
	}
	dir := t.TempDir()
	s := filepath.Join(dir, "x.s")
	o := filepath.Join(dir, "x.o")
	os.WriteFile(s, []byte(src.String()), 0o644)
	if out, err := exec.Command("cc", "-c", "-arch", "arm64", "-o", o, s).CombinedOutput(); err != nil {
		t.Fatalf("assembler: %v\n%s", err, out)
	}
	f, err := macho.Open(o)
	if err != nil {
		t.Fatal(err)
	}
	data, err := f.Section("__text").Data()
	if err != nil {
		t.Fatal(err)
	}
	pos := 0
	for i, c := range cases {
		for j, w := range want[i] {
			if pos+4 > len(data) {
				t.Fatalf("assembler output too short at %q", c.text)
			}
			got := binary.LittleEndian.Uint32(data[pos:])
			if got != w {
				t.Errorf("%q (word %d): encoder %08x, assembler %08x", c.text, j, w, got)
			}
			pos += 4
		}
	}
	if pos != len(data) {
		t.Errorf("instruction count mismatch: %d bytes encoded, %d assembled", pos, len(data))
	}
}
