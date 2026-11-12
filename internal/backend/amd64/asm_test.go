package amd64

import (
	"bytes"
	"debug/macho"
	"fmt"
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
	type tc struct {
		text string
		emit func(a *Asm)
	}
	cases := []tc{
		{"mov rax, rcx", func(a *Asm) { a.MovRR(RAX, RCX) }},
		{"mov r10, rbp", func(a *Asm) { a.MovRR(R10, RBP) }},
		{"mov r12, r9", func(a *Asm) { a.MovRR(R12, R9) }},
		{"xor eax, eax", func(a *Asm) { a.MovImm(RAX, 0) }},
		{"mov r9d, 1000", func(a *Asm) { a.MovImm(R9, 1000) }},
		{"mov rax, -5", func(a *Asm) { a.MovImm(RAX, -5) }},
		{"movabs rcx, 0x123456789", func(a *Asm) { a.MovImm(RCX, 0x123456789) }},
		{"movzx rax, byte ptr [r10]", func(a *Asm) { a.Load(1, RAX, R10, 0) }},
		{"movzx rcx, word ptr [rbp - 8]", func(a *Asm) { a.Load(2, RCX, RBP, -8) }},
		{"mov eax, dword ptr [rsp + 16]", func(a *Asm) { a.Load(4, RAX, RSP, 16) }},
		{"mov r10, qword ptr [rbp - 1000]", func(a *Asm) { a.Load(8, R10, RBP, -1000) }},
		{"mov r11, qword ptr [r10 + 8]", func(a *Asm) { a.Load(8, R11, R10, 8) }},
		{"mov r12, qword ptr [r13]", func(a *Asm) { a.Load(8, R12, R13, 0) }},
		{"mov byte ptr [r10], al", func(a *Asm) { a.Store(1, RAX, R10, 0) }},
		{"mov byte ptr [rbp - 1], sil", func(a *Asm) { a.Store(1, RSI, RBP, -1) }},
		{"mov word ptr [rsp + 2], cx", func(a *Asm) { a.Store(2, RCX, RSP, 2) }},
		{"mov dword ptr [rbx + 4], r9d", func(a *Asm) { a.Store(4, R9, RBX, 4) }},
		{"mov qword ptr [rsp], rax", func(a *Asm) { a.Store(8, RAX, RSP, 0) }},
		{"mov byte ptr [rsp + 8], 0", func(a *Asm) { a.StoreZero(1, RSP, 8) }},
		{"mov word ptr [rbp - 2], 0", func(a *Asm) { a.StoreZero(2, RBP, -2) }},
		{"mov dword ptr [r10], 0", func(a *Asm) { a.StoreZero(4, R10, 0) }},
		{"mov qword ptr [rbp - 32], 0", func(a *Asm) { a.StoreZero(8, RBP, -32) }},
		{"lea r10, [rbp - 40]", func(a *Asm) { a.Lea(R10, RBP, -40) }},
		{"lea rbx, [rsp + 300]", func(a *Asm) { a.Lea(RBX, RSP, 300) }},
		{"add rax, rcx", func(a *Asm) { a.Add(RAX, RCX) }},
		{"sub r10, r8", func(a *Asm) { a.Sub(R10, R8) }},
		{"and rax, rcx", func(a *Asm) { a.And(RAX, RCX) }},
		{"or rax, rcx", func(a *Asm) { a.Or(RAX, RCX) }},
		{"xor rax, rcx", func(a *Asm) { a.Xor(RAX, RCX) }},
		{"cmp rax, rcx", func(a *Asm) { a.Cmp(RAX, RCX) }},
		{"test r12, r12", func(a *Asm) { a.Test(R12, R12) }},
		{"test r9d, r9d", func(a *Asm) { a.Test32(R9, R9) }},
		{"imul rax, rcx", func(a *Asm) { a.Imul(RAX, RCX) }},
		{"imul rdx, r9", func(a *Asm) { a.Imul(RDX, R9) }},
		{"mul rcx", func(a *Asm) { a.MulRAX(RCX) }},
		{"div rcx", func(a *Asm) { a.DivRAX(RCX) }},
		{"idiv r9", func(a *Asm) { a.IdivRAX(R9) }},
		{"cqo", func(a *Asm) { a.Cqo() }},
		{"neg rax", func(a *Asm) { a.Neg(RAX) }},
		{"not r10", func(a *Asm) { a.Not(R10) }},
		{"shl rax, cl", func(a *Asm) { a.ShlCL(RAX) }},
		{"shr rax, cl", func(a *Asm) { a.ShrCL(RAX) }},
		{"sar r9, cl", func(a *Asm) { a.SarCL(R9) }},
		{"shr rax, 4", func(a *Asm) { a.ShrImm(RAX, 4) }},
		{"shl rax, 4", func(a *Asm) { a.ShlImm(RAX, 4) }},
		{"btc r9, 63", func(a *Asm) { a.Btc(R9, 63) }},
		{"add rsp, 32", func(a *Asm) { a.AddImm(RSP, 32) }},
		{"sub rsp, 4096", func(a *Asm) { a.SubImm(RSP, 4096) }},
		{"and rax, -16", func(a *Asm) { a.AndImm(RAX, -16) }},
		{"cmp rax, -1", func(a *Asm) { a.CmpImm(RAX, -1) }},
		{"cmp r10, 100000", func(a *Asm) { a.CmpImm(R10, 100000) }},
		{"movsx rax, al", func(a *Asm) { a.Movsx(RAX, RAX, 1) }},
		{"movsx rcx, si", func(a *Asm) { a.Movsx(RCX, RSI, 2) }},
		{"movsxd rax, ecx", func(a *Asm) { a.Movsx(RAX, RCX, 4) }},
		{"movzx rax, sil", func(a *Asm) { a.Movzx(RAX, RSI, 1) }},
		{"movzx rax, r9w", func(a *Asm) { a.Movzx(RAX, R9, 2) }},
		{"mov eax, eax", func(a *Asm) { a.Movzx(RAX, RAX, 4) }},
		{"sete al\nmovzx rax, al", func(a *Asm) { a.Setcc(CE, RAX) }},
		{"setl dil\nmovzx rdi, dil", func(a *Asm) { a.Setcc(CL, RDI) }},
		{"setnp r9b", func(a *Asm) { a.SetccByte(CNP, R9) }},
		{"cmovl rax, rcx", func(a *Asm) { a.Cmov(CL, RAX, RCX) }},
		{"cmovne r8, r10", func(a *Asm) { a.Cmov(CNE, R8, R10) }},
		{"call rbx", func(a *Asm) { a.CallR(RBX) }},
		{"call r12", func(a *Asm) { a.CallR(R12) }},
		{"ret", func(a *Asm) { a.Ret() }},
		{"ud2", func(a *Asm) { a.Ud2() }},
		{"push rbp", func(a *Asm) { a.Push(RBP) }},
		{"push r15", func(a *Asm) { a.Push(R15) }},
		{"pop r12", func(a *Asm) { a.Pop(R12) }},
		{"rep movsq", func(a *Asm) { a.RepMovsq() }},
		{"movq xmm0, rax", func(a *Asm) { a.MovqToX(0, RAX, true) }},
		{"movd xmm1, ecx", func(a *Asm) { a.MovqToX(1, RCX, false) }},
		{"movq rax, xmm0", func(a *Asm) { a.MovqFromX(RAX, 0, true) }},
		{"movd r9d, xmm0", func(a *Asm) { a.MovqFromX(R9, 0, false) }},
		{"addsd xmm0, xmm1", func(a *Asm) { a.Addsd(0, 1, true) }},
		{"subss xmm0, xmm1", func(a *Asm) { a.Subsd(0, 1, false) }},
		{"mulsd xmm0, xmm1", func(a *Asm) { a.Mulsd(0, 1, true) }},
		{"divss xmm0, xmm1", func(a *Asm) { a.Divsd(0, 1, false) }},
		{"ucomisd xmm0, xmm1", func(a *Asm) { a.Ucomisd(0, 1, true) }},
		{"ucomiss xmm1, xmm0", func(a *Asm) { a.Ucomisd(1, 0, false) }},
		{"cvtsi2sd xmm0, rax", func(a *Asm) { a.Cvtsi2sd(0, RAX, true) }},
		{"cvtsi2ss xmm0, r9", func(a *Asm) { a.Cvtsi2sd(0, R9, false) }},
		{"cvttsd2si rax, xmm0", func(a *Asm) { a.Cvttsd2si(RAX, 0, true) }},
		{"cvttss2si rax, xmm0", func(a *Asm) { a.Cvttsd2si(RAX, 0, false) }},
		{"cvtss2sd xmm0, xmm0", func(a *Asm) { a.Cvtss2sd(0, 0) }},
		{"cvtsd2ss xmm0, xmm1", func(a *Asm) { a.Cvtsd2ss(0, 1) }},
	}
	var src strings.Builder
	src.WriteString(".intel_syntax noprefix\n")
	var want [][]byte
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
	if out, err := exec.Command("cc", "-c", "-arch", "x86_64", "-o", o, s).CombinedOutput(); err != nil {
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
		w := want[i]
		if pos+len(w) > len(data) || !bytes.Equal(data[pos:pos+len(w)], w) {
			end := pos + len(w) + 4
			if end > len(data) {
				end = len(data)
			}
			t.Fatalf("%q: encoder % x, assembler % x", c.text, w, data[pos:end])
		}
		pos += len(w)
	}
	if pos != len(data) {
		t.Errorf("length mismatch: %d encoded, %d assembled", pos, len(data))
	}
	_ = fmt.Sprint
}
