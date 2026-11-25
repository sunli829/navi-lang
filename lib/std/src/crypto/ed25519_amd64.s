// Ed25519's #[asm] functions for amd64 (crypto/ed25519.navi).

// lookup_affine(table, idx, neg): Ed25519's table[idx - 1] of 8 affine
// points (15 words each: y + x, y - x, 2dT), every entry read; idx 0 the
// identity (1, 1, 0). With neg 1, y + x and y - x swapped and 2dT negated
// (2p - 2dT: limbs below 2^52, not carried), by masks.
lookup_affine:
    mov rsi, qword ptr [rsp + 8]
    mov rcx, qword ptr [rsp + 16]
    mov rdx, qword ptr [rsp + 24]
    pxor xmm0, xmm0
    pxor xmm1, xmm1
    pxor xmm2, xmm2
    pxor xmm3, xmm3
    pxor xmm4, xmm4
    pxor xmm5, xmm5
    pxor xmm6, xmm6
    pxor xmm7, xmm7
    mov rax, 1
.loop:
    mov r8, rax
    xor r8, rcx
    mov r9, r8
    neg r9
    or r8, r9
    shr r8, 63
    sub r8, 1
    movq xmm15, r8
    pshufd xmm15, xmm15, 0x44
    movdqu xmm8, xmmword ptr [rsi + 0]
    movdqu xmm9, xmmword ptr [rsi + 16]
    movdqu xmm10, xmmword ptr [rsi + 32]
    movdqu xmm11, xmmword ptr [rsi + 48]
    pand xmm8, xmm15
    por xmm0, xmm8
    pand xmm9, xmm15
    por xmm1, xmm9
    pand xmm10, xmm15
    por xmm2, xmm10
    pand xmm11, xmm15
    por xmm3, xmm11
    movdqu xmm8, xmmword ptr [rsi + 64]
    movdqu xmm9, xmmword ptr [rsi + 80]
    movdqu xmm10, xmmword ptr [rsi + 96]
    movq xmm11, qword ptr [rsi + 112]
    pand xmm8, xmm15
    por xmm4, xmm8
    pand xmm9, xmm15
    por xmm5, xmm9
    pand xmm10, xmm15
    por xmm6, xmm10
    pand xmm11, xmm15
    por xmm7, xmm11
    add rsi, 120
    inc rax
    cmp rax, 9
    jne .loop
    movdqu xmmword ptr [r13 + 0], xmm0
    movdqu xmmword ptr [r13 + 16], xmm1
    movdqu xmmword ptr [r13 + 32], xmm2
    movdqu xmmword ptr [r13 + 48], xmm3
    movdqu xmmword ptr [r13 + 64], xmm4
    movdqu xmmword ptr [r13 + 80], xmm5
    movdqu xmmword ptr [r13 + 96], xmm6
    movq qword ptr [r13 + 112], xmm7
    mov r8, rcx
    mov r9, r8
    neg r9
    or r8, r9
    shr r8, 63
    xor r8, 1
    mov r9, qword ptr [r13]
    or r9, r8
    mov qword ptr [r13], r9
    mov r9, qword ptr [r13 + 40]
    or r9, r8
    mov qword ptr [r13 + 40], r9
    neg rdx
    mov r8, qword ptr [r13 + 0]
    mov r9, qword ptr [r13 + 40]
    mov r10, r8
    xor r10, r9
    and r10, rdx
    xor r8, r10
    xor r9, r10
    mov qword ptr [r13 + 0], r8
    mov qword ptr [r13 + 40], r9
    mov r8, qword ptr [r13 + 8]
    mov r9, qword ptr [r13 + 48]
    mov r10, r8
    xor r10, r9
    and r10, rdx
    xor r8, r10
    xor r9, r10
    mov qword ptr [r13 + 8], r8
    mov qword ptr [r13 + 48], r9
    mov r8, qword ptr [r13 + 16]
    mov r9, qword ptr [r13 + 56]
    mov r10, r8
    xor r10, r9
    and r10, rdx
    xor r8, r10
    xor r9, r10
    mov qword ptr [r13 + 16], r8
    mov qword ptr [r13 + 56], r9
    mov r8, qword ptr [r13 + 24]
    mov r9, qword ptr [r13 + 64]
    mov r10, r8
    xor r10, r9
    and r10, rdx
    xor r8, r10
    xor r9, r10
    mov qword ptr [r13 + 24], r8
    mov qword ptr [r13 + 64], r9
    mov r8, qword ptr [r13 + 32]
    mov r9, qword ptr [r13 + 72]
    mov r10, r8
    xor r10, r9
    and r10, rdx
    xor r8, r10
    xor r9, r10
    mov qword ptr [r13 + 32], r8
    mov qword ptr [r13 + 72], r9
    mov r8, qword ptr [r13 + 80]
    mov r9, 0xfffffffffffda
    sub r9, r8
    xor r9, r8
    and r9, rdx
    xor r8, r9
    mov qword ptr [r13 + 80], r8
    mov r8, qword ptr [r13 + 88]
    mov r9, 0xffffffffffffe
    sub r9, r8
    xor r9, r8
    and r9, rdx
    xor r8, r9
    mov qword ptr [r13 + 88], r8
    mov r8, qword ptr [r13 + 96]
    mov r9, 0xffffffffffffe
    sub r9, r8
    xor r9, r8
    and r9, rdx
    xor r8, r9
    mov qword ptr [r13 + 96], r8
    mov r8, qword ptr [r13 + 104]
    mov r9, 0xffffffffffffe
    sub r9, r8
    xor r9, r8
    and r9, rdx
    xor r8, r9
    mov qword ptr [r13 + 104], r8
    mov r8, qword ptr [r13 + 112]
    mov r9, 0xffffffffffffe
    sub r9, r8
    xor r9, r8
    and r9, rdx
    xor r8, r9
    mov qword ptr [r13 + 112], r8
    ret
