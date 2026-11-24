// crypto/aes_gcm.navi's #[asm] functions for amd64 (AES-NI and
// PCLMULQDQ).

// ghash_blocks(h, y, data, n): y = (y ^ block) * H in GF(2^128), for each
// of the n 16-byte blocks; as aes_gcm_arm64.s (each byte's bits reversed
// with two pshufb lookups, then a Karatsuba product with pclmulqdq and two
// folds by x^128 = 0x87).
ghash_blocks:
    mov rax, qword ptr [rsp + 8]        // h
    mov rcx, qword ptr [rsp + 16]       // y
    mov rsi, qword ptr [rsp + 24]       // data
    mov rdx, qword ptr [rsp + 32]       // n
    test rdx, rdx
    jz .done
    movdqu xmm12, xmmword ptr [rip + .rev_lo]
    movdqu xmm13, xmmword ptr [rip + .rev_hi]
    movdqu xmm14, xmmword ptr [rip + .nibble]
    movdqu xmm15, xmmword ptr [rip + .k]
    movdqu xmm0, xmmword ptr [rax]
    movdqa xmm8, xmm0
    pand xmm8, xmm14
    psrlw xmm0, 4
    pand xmm0, xmm14
    movdqa xmm10, xmm12
    pshufb xmm10, xmm8
    movdqa xmm8, xmm13
    pshufb xmm8, xmm0
    por xmm10, xmm8
    pshufd xmm11, xmm10, 0x4e
    pxor xmm11, xmm10                   // q0: h0 ^ h1
    movdqu xmm0, xmmword ptr [rcx]
    movdqa xmm8, xmm0
    pand xmm8, xmm14
    psrlw xmm0, 4
    pand xmm0, xmm14
    movdqa xmm1, xmm12
    pshufb xmm1, xmm8
    movdqa xmm8, xmm13
    pshufb xmm8, xmm0
    por xmm1, xmm8
.loop:
    movdqu xmm0, xmmword ptr [rsi]
    movdqa xmm8, xmm0
    pand xmm8, xmm14
    psrlw xmm0, 4
    pand xmm0, xmm14
    movdqa xmm9, xmm12
    pshufb xmm9, xmm8
    movdqa xmm8, xmm13
    pshufb xmm8, xmm0
    por xmm9, xmm8
    pxor xmm1, xmm9                     // a = Y ^ X
    movdqa xmm2, xmm1
    pclmulqdq xmm2, xmm10, 0x00         // lo = a0 h0
    movdqa xmm3, xmm1
    pclmulqdq xmm3, xmm10, 0x11         // hi = a1 h1
    pshufd xmm4, xmm1, 0x4e
    pxor xmm4, xmm1
    pclmulqdq xmm4, xmm11, 0x00         // (a0 ^ a1)(h0 ^ h1)
    pxor xmm4, xmm2
    pxor xmm4, xmm3                     // mid
    movdqa xmm5, xmm4
    pslldq xmm5, 8
    pxor xmm2, xmm5                     // lo ^= [0, m0]
    psrldq xmm4, 8
    pxor xmm3, xmm4                     // hi ^= [m1, 0]
    movdqa xmm5, xmm3
    pclmulqdq xmm5, xmm15, 0x01         // P3 * 0x87, at x^64
    movdqa xmm6, xmm5
    pslldq xmm6, 8
    pxor xmm2, xmm6
    psrldq xmm5, 8
    pxor xmm3, xmm5
    movdqa xmm5, xmm3
    pclmulqdq xmm5, xmm15, 0x00         // P2 * 0x87
    pxor xmm2, xmm5
    movdqa xmm1, xmm2
    add rsi, 16
    dec rdx
    jnz .loop
    movdqa xmm8, xmm1
    pand xmm8, xmm14
    psrlw xmm1, 4
    pand xmm1, xmm14
    movdqa xmm0, xmm12
    pshufb xmm0, xmm8
    movdqa xmm8, xmm13
    pshufb xmm8, xmm1
    por xmm0, xmm8
    movdqu xmmword ptr [rcx], xmm0
.done:
    ret
    .balign 16
.rev_lo:
    // The bits of the low nibble reversed, as the high nibble.
    .byte 0x00, 0x80, 0x40, 0xc0, 0x20, 0xa0, 0x60, 0xe0, 0x10, 0x90, 0x50, 0xd0, 0x30, 0xb0, 0x70, 0xf0
.rev_hi:
    .byte 0x00, 0x08, 0x04, 0x0c, 0x02, 0x0a, 0x06, 0x0e, 0x01, 0x09, 0x05, 0x0d, 0x03, 0x0b, 0x07, 0x0f
.nibble:
    .byte 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f
.k:
    .quad 0x87, 0x87

// aes_ctr_xor(xk, rounds, ctr, src, dst, n): dst = src ^ E(ctr) for n
// blocks, the counter (the big-endian last word of the 16 bytes at ctr)
// incremented each block and written back. The round keys k1.. are in
// xmm2.., k0 is loaded each block (AES-256 needs all the registers).
aes_ctr_xor:
    mov rax, qword ptr [rsp + 8]        // xk
    mov r8, qword ptr [rsp + 16]        // rounds
    mov r9, qword ptr [rsp + 24]        // ctr
    mov rsi, qword ptr [rsp + 32]       // src
    mov rdi, qword ptr [rsp + 40]       // dst
    mov rdx, qword ptr [rsp + 48]       // n
    test rdx, rdx
    jz .done
    movdqu xmm2, xmmword ptr [rax + 16]
    movdqu xmm3, xmmword ptr [rax + 32]
    movdqu xmm4, xmmword ptr [rax + 48]
    movdqu xmm5, xmmword ptr [rax + 64]
    movdqu xmm6, xmmword ptr [rax + 80]
    movdqu xmm7, xmmword ptr [rax + 96]
    movdqu xmm8, xmmword ptr [rax + 112]
    movdqu xmm9, xmmword ptr [rax + 128]
    movdqu xmm10, xmmword ptr [rax + 144]
    movdqu xmm11, xmmword ptr [rax + 160]
    cmp r8, 10
    je .keys
    movdqu xmm12, xmmword ptr [rax + 176]
    movdqu xmm13, xmmword ptr [rax + 192]
    cmp r8, 12
    je .keys
    movdqu xmm14, xmmword ptr [rax + 208]
    movdqu xmm15, xmmword ptr [rax + 224]
.keys:
    mov r10d, dword ptr [r9 + 12]
    bswap r10d
.loop:
    mov r11d, r10d
    bswap r11d
    mov dword ptr [r9 + 12], r11d
    movdqu xmm0, xmmword ptr [r9]
    movdqu xmm1, xmmword ptr [rax]
    pxor xmm0, xmm1
    aesenc xmm0, xmm2
    aesenc xmm0, xmm3
    aesenc xmm0, xmm4
    aesenc xmm0, xmm5
    aesenc xmm0, xmm6
    aesenc xmm0, xmm7
    aesenc xmm0, xmm8
    aesenc xmm0, xmm9
    aesenc xmm0, xmm10
    cmp r8, 10
    je .last10
    aesenc xmm0, xmm11
    aesenc xmm0, xmm12
    cmp r8, 12
    je .last12
    aesenc xmm0, xmm13
    aesenc xmm0, xmm14
    aesenclast xmm0, xmm15
    jmp .xor
.last12:
    aesenclast xmm0, xmm13
    jmp .xor
.last10:
    aesenclast xmm0, xmm11
.xor:
    movdqu xmm1, xmmword ptr [rsi]
    pxor xmm0, xmm1
    movdqu xmmword ptr [rdi], xmm0
    inc r10d
    add rsi, 16
    add rdi, 16
    dec rdx
    jnz .loop
    mov r11d, r10d
    bswap r11d
    mov dword ptr [r9 + 12], r11d
.done:
    ret
