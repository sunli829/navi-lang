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
// incremented each block and written back. Eight blocks at a time through
// the rounds (the AES unit's latency hidden), then one at a time; the
// round keys read each round (sixteen xmm registers). The counter block is
// kept byte-reversed in xmm13 (the counter its first dword, paddd adds 1).
aes_ctr_xor:
    mov rax, qword ptr [rsp + 8]        // xk
    mov r8, qword ptr [rsp + 16]        // rounds
    mov r9, qword ptr [rsp + 24]        // ctr
    mov rsi, qword ptr [rsp + 32]       // src
    mov rdi, qword ptr [rsp + 40]       // dst
    mov rdx, qword ptr [rsp + 48]       // n
    test rdx, rdx
    jz .done
    movdqu xmm15, xmmword ptr [rip + .bswap]
    movdqu xmm14, xmmword ptr [rip + .one]
    movdqu xmm13, xmmword ptr [r9]
    pshufb xmm13, xmm15
    // The last round key's offset
    mov r10, r8
    shl r10, 4
.c8:
    cmp rdx, 8
    jb .c1
    movdqa xmm0, xmm13
    paddd xmm13, xmm14
    pshufb xmm0, xmm15
    movdqa xmm1, xmm13
    paddd xmm13, xmm14
    pshufb xmm1, xmm15
    movdqa xmm2, xmm13
    paddd xmm13, xmm14
    pshufb xmm2, xmm15
    movdqa xmm3, xmm13
    paddd xmm13, xmm14
    pshufb xmm3, xmm15
    movdqa xmm4, xmm13
    paddd xmm13, xmm14
    pshufb xmm4, xmm15
    movdqa xmm5, xmm13
    paddd xmm13, xmm14
    pshufb xmm5, xmm15
    movdqa xmm6, xmm13
    paddd xmm13, xmm14
    pshufb xmm6, xmm15
    movdqa xmm7, xmm13
    paddd xmm13, xmm14
    pshufb xmm7, xmm15
    movdqu xmm8, xmmword ptr [rax]
    pxor xmm0, xmm8
    pxor xmm1, xmm8
    pxor xmm2, xmm8
    pxor xmm3, xmm8
    pxor xmm4, xmm8
    pxor xmm5, xmm8
    pxor xmm6, xmm8
    pxor xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 16]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 32]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 48]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 64]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 80]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 96]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 112]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 128]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 144]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    cmp r8, 10
    je .c8last
    movdqu xmm8, xmmword ptr [rax + 160]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 176]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    cmp r8, 12
    je .c8last
    movdqu xmm8, xmmword ptr [rax + 192]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
    movdqu xmm8, xmmword ptr [rax + 208]
    aesenc xmm0, xmm8
    aesenc xmm1, xmm8
    aesenc xmm2, xmm8
    aesenc xmm3, xmm8
    aesenc xmm4, xmm8
    aesenc xmm5, xmm8
    aesenc xmm6, xmm8
    aesenc xmm7, xmm8
.c8last:
    movdqu xmm8, xmmword ptr [rax + r10]
    aesenclast xmm0, xmm8
    aesenclast xmm1, xmm8
    aesenclast xmm2, xmm8
    aesenclast xmm3, xmm8
    aesenclast xmm4, xmm8
    aesenclast xmm5, xmm8
    aesenclast xmm6, xmm8
    aesenclast xmm7, xmm8
    movdqu xmm9, xmmword ptr [rsi]
    pxor xmm0, xmm9
    movdqu xmmword ptr [rdi], xmm0
    movdqu xmm9, xmmword ptr [rsi + 16]
    pxor xmm1, xmm9
    movdqu xmmword ptr [rdi + 16], xmm1
    movdqu xmm9, xmmword ptr [rsi + 32]
    pxor xmm2, xmm9
    movdqu xmmword ptr [rdi + 32], xmm2
    movdqu xmm9, xmmword ptr [rsi + 48]
    pxor xmm3, xmm9
    movdqu xmmword ptr [rdi + 48], xmm3
    movdqu xmm9, xmmword ptr [rsi + 64]
    pxor xmm4, xmm9
    movdqu xmmword ptr [rdi + 64], xmm4
    movdqu xmm9, xmmword ptr [rsi + 80]
    pxor xmm5, xmm9
    movdqu xmmword ptr [rdi + 80], xmm5
    movdqu xmm9, xmmword ptr [rsi + 96]
    pxor xmm6, xmm9
    movdqu xmmword ptr [rdi + 96], xmm6
    movdqu xmm9, xmmword ptr [rsi + 112]
    pxor xmm7, xmm9
    movdqu xmmword ptr [rdi + 112], xmm7
    add rsi, 128
    add rdi, 128
    sub rdx, 8
    jmp .c8
.c1:
    test rdx, rdx
    jz .end
    movdqa xmm0, xmm13
    paddd xmm13, xmm14
    pshufb xmm0, xmm15
    movdqu xmm8, xmmword ptr [rax]
    pxor xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 16]
    aesenc xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 32]
    aesenc xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 48]
    aesenc xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 64]
    aesenc xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 80]
    aesenc xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 96]
    aesenc xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 112]
    aesenc xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 128]
    aesenc xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 144]
    aesenc xmm0, xmm8
    cmp r8, 10
    je .c1last
    movdqu xmm8, xmmword ptr [rax + 160]
    aesenc xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 176]
    aesenc xmm0, xmm8
    cmp r8, 12
    je .c1last
    movdqu xmm8, xmmword ptr [rax + 192]
    aesenc xmm0, xmm8
    movdqu xmm8, xmmword ptr [rax + 208]
    aesenc xmm0, xmm8
.c1last:
    movdqu xmm8, xmmword ptr [rax + r10]
    aesenclast xmm0, xmm8
    movdqu xmm9, xmmword ptr [rsi]
    pxor xmm0, xmm9
    movdqu xmmword ptr [rdi], xmm0
    add rsi, 16
    add rdi, 16
    dec rdx
    jmp .c1
.end:
    pshufb xmm13, xmm15
    movdqu xmmword ptr [r9], xmm13
.done:
    ret
    .balign 16
.bswap:
    .byte 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0
.one:
    .quad 1, 0

// gcm_crypt8(xk, rounds, ctr, src, dst, n, ht, y, dec): as
// aes_gcm_arm64.s's, n times eight blocks of counter mode and GHASH in one
// pass, the GHASH of eight ciphertext blocks spread between the AES rounds
// of the next eight (as Go's gcmAesEnc; decrypting, of the eight being
// decrypted, read before they are written). The round keys read from
// memory, GHASH as ghash_blocks's (bits reversed by pshufb lookups) with
// ht's H^8..H and one reduction per eight blocks.
gcm_crypt8:
    mov rax, qword ptr [rsp + 8]        // xk
    mov r8, qword ptr [rsp + 16]        // rounds
    mov r9, qword ptr [rsp + 24]        // ctr
    mov rsi, qword ptr [rsp + 32]       // src
    mov rdi, qword ptr [rsp + 40]       // dst
    mov rdx, qword ptr [rsp + 48]       // n
    mov rcx, qword ptr [rsp + 56]       // ht
    mov r10, qword ptr [rsp + 64]       // y
    mov r11, qword ptr [rsp + 72]       // dec
    test rdx, rdx
    jz .gdone
    movdqu xmm12, xmmword ptr [r10]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm8, xmmword ptr [rip + .grev_lo]
    pshufb xmm8, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm8, xmm13
    movdqu xmm15, xmmword ptr [r9]
    pshufb xmm15, xmmword ptr [rip + .gbswap]
    shl r8, 4                           // the last round key's offset
    test r11, r11
    jnz .gdec
    // Encrypting: the first eight alone, then each eight with the GHASH
    // of the eight before, the last eight's after.
    movdqa xmm0, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm0, xmmword ptr [rip + .gbswap]
    movdqa xmm1, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm1, xmmword ptr [rip + .gbswap]
    movdqa xmm2, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm2, xmmword ptr [rip + .gbswap]
    movdqa xmm3, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm3, xmmword ptr [rip + .gbswap]
    movdqa xmm4, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm4, xmmword ptr [rip + .gbswap]
    movdqa xmm5, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm5, xmmword ptr [rip + .gbswap]
    movdqa xmm6, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm6, xmmword ptr [rip + .gbswap]
    movdqa xmm7, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm7, xmmword ptr [rip + .gbswap]
    pxor xmm0, xmmword ptr [rax]
    pxor xmm1, xmmword ptr [rax]
    pxor xmm2, xmmword ptr [rax]
    pxor xmm3, xmmword ptr [rax]
    pxor xmm4, xmmword ptr [rax]
    pxor xmm5, xmmword ptr [rax]
    pxor xmm6, xmmword ptr [rax]
    pxor xmm7, xmmword ptr [rax]
    aesenc xmm0, xmmword ptr [rax + 16]
    aesenc xmm1, xmmword ptr [rax + 16]
    aesenc xmm2, xmmword ptr [rax + 16]
    aesenc xmm3, xmmword ptr [rax + 16]
    aesenc xmm4, xmmword ptr [rax + 16]
    aesenc xmm5, xmmword ptr [rax + 16]
    aesenc xmm6, xmmword ptr [rax + 16]
    aesenc xmm7, xmmword ptr [rax + 16]
    aesenc xmm0, xmmword ptr [rax + 32]
    aesenc xmm1, xmmword ptr [rax + 32]
    aesenc xmm2, xmmword ptr [rax + 32]
    aesenc xmm3, xmmword ptr [rax + 32]
    aesenc xmm4, xmmword ptr [rax + 32]
    aesenc xmm5, xmmword ptr [rax + 32]
    aesenc xmm6, xmmword ptr [rax + 32]
    aesenc xmm7, xmmword ptr [rax + 32]
    aesenc xmm0, xmmword ptr [rax + 48]
    aesenc xmm1, xmmword ptr [rax + 48]
    aesenc xmm2, xmmword ptr [rax + 48]
    aesenc xmm3, xmmword ptr [rax + 48]
    aesenc xmm4, xmmword ptr [rax + 48]
    aesenc xmm5, xmmword ptr [rax + 48]
    aesenc xmm6, xmmword ptr [rax + 48]
    aesenc xmm7, xmmword ptr [rax + 48]
    aesenc xmm0, xmmword ptr [rax + 64]
    aesenc xmm1, xmmword ptr [rax + 64]
    aesenc xmm2, xmmword ptr [rax + 64]
    aesenc xmm3, xmmword ptr [rax + 64]
    aesenc xmm4, xmmword ptr [rax + 64]
    aesenc xmm5, xmmword ptr [rax + 64]
    aesenc xmm6, xmmword ptr [rax + 64]
    aesenc xmm7, xmmword ptr [rax + 64]
    aesenc xmm0, xmmword ptr [rax + 80]
    aesenc xmm1, xmmword ptr [rax + 80]
    aesenc xmm2, xmmword ptr [rax + 80]
    aesenc xmm3, xmmword ptr [rax + 80]
    aesenc xmm4, xmmword ptr [rax + 80]
    aesenc xmm5, xmmword ptr [rax + 80]
    aesenc xmm6, xmmword ptr [rax + 80]
    aesenc xmm7, xmmword ptr [rax + 80]
    aesenc xmm0, xmmword ptr [rax + 96]
    aesenc xmm1, xmmword ptr [rax + 96]
    aesenc xmm2, xmmword ptr [rax + 96]
    aesenc xmm3, xmmword ptr [rax + 96]
    aesenc xmm4, xmmword ptr [rax + 96]
    aesenc xmm5, xmmword ptr [rax + 96]
    aesenc xmm6, xmmword ptr [rax + 96]
    aesenc xmm7, xmmword ptr [rax + 96]
    aesenc xmm0, xmmword ptr [rax + 112]
    aesenc xmm1, xmmword ptr [rax + 112]
    aesenc xmm2, xmmword ptr [rax + 112]
    aesenc xmm3, xmmword ptr [rax + 112]
    aesenc xmm4, xmmword ptr [rax + 112]
    aesenc xmm5, xmmword ptr [rax + 112]
    aesenc xmm6, xmmword ptr [rax + 112]
    aesenc xmm7, xmmword ptr [rax + 112]
    aesenc xmm0, xmmword ptr [rax + 128]
    aesenc xmm1, xmmword ptr [rax + 128]
    aesenc xmm2, xmmword ptr [rax + 128]
    aesenc xmm3, xmmword ptr [rax + 128]
    aesenc xmm4, xmmword ptr [rax + 128]
    aesenc xmm5, xmmword ptr [rax + 128]
    aesenc xmm6, xmmword ptr [rax + 128]
    aesenc xmm7, xmmword ptr [rax + 128]
    aesenc xmm0, xmmword ptr [rax + 144]
    aesenc xmm1, xmmword ptr [rax + 144]
    aesenc xmm2, xmmword ptr [rax + 144]
    aesenc xmm3, xmmword ptr [rax + 144]
    aesenc xmm4, xmmword ptr [rax + 144]
    aesenc xmm5, xmmword ptr [rax + 144]
    aesenc xmm6, xmmword ptr [rax + 144]
    aesenc xmm7, xmmword ptr [rax + 144]
    cmp r8, 160
    je .galast
    aesenc xmm0, xmmword ptr [rax + 160]
    aesenc xmm1, xmmword ptr [rax + 160]
    aesenc xmm2, xmmword ptr [rax + 160]
    aesenc xmm3, xmmword ptr [rax + 160]
    aesenc xmm4, xmmword ptr [rax + 160]
    aesenc xmm5, xmmword ptr [rax + 160]
    aesenc xmm6, xmmword ptr [rax + 160]
    aesenc xmm7, xmmword ptr [rax + 160]
    aesenc xmm0, xmmword ptr [rax + 176]
    aesenc xmm1, xmmword ptr [rax + 176]
    aesenc xmm2, xmmword ptr [rax + 176]
    aesenc xmm3, xmmword ptr [rax + 176]
    aesenc xmm4, xmmword ptr [rax + 176]
    aesenc xmm5, xmmword ptr [rax + 176]
    aesenc xmm6, xmmword ptr [rax + 176]
    aesenc xmm7, xmmword ptr [rax + 176]
    cmp r8, 192
    je .galast
    aesenc xmm0, xmmword ptr [rax + 192]
    aesenc xmm1, xmmword ptr [rax + 192]
    aesenc xmm2, xmmword ptr [rax + 192]
    aesenc xmm3, xmmword ptr [rax + 192]
    aesenc xmm4, xmmword ptr [rax + 192]
    aesenc xmm5, xmmword ptr [rax + 192]
    aesenc xmm6, xmmword ptr [rax + 192]
    aesenc xmm7, xmmword ptr [rax + 192]
    aesenc xmm0, xmmword ptr [rax + 208]
    aesenc xmm1, xmmword ptr [rax + 208]
    aesenc xmm2, xmmword ptr [rax + 208]
    aesenc xmm3, xmmword ptr [rax + 208]
    aesenc xmm4, xmmword ptr [rax + 208]
    aesenc xmm5, xmmword ptr [rax + 208]
    aesenc xmm6, xmmword ptr [rax + 208]
    aesenc xmm7, xmmword ptr [rax + 208]
.galast:
    aesenclast xmm0, xmmword ptr [rax + r8]
    aesenclast xmm1, xmmword ptr [rax + r8]
    aesenclast xmm2, xmmword ptr [rax + r8]
    aesenclast xmm3, xmmword ptr [rax + r8]
    aesenclast xmm4, xmmword ptr [rax + r8]
    aesenclast xmm5, xmmword ptr [rax + r8]
    aesenclast xmm6, xmmword ptr [rax + r8]
    aesenclast xmm7, xmmword ptr [rax + r8]
    movdqu xmm12, xmmword ptr [rsi + 0]
    pxor xmm0, xmm12
    movdqu xmmword ptr [rdi + 0], xmm0
    movdqu xmm12, xmmword ptr [rsi + 16]
    pxor xmm1, xmm12
    movdqu xmmword ptr [rdi + 16], xmm1
    movdqu xmm12, xmmword ptr [rsi + 32]
    pxor xmm2, xmm12
    movdqu xmmword ptr [rdi + 32], xmm2
    movdqu xmm12, xmmword ptr [rsi + 48]
    pxor xmm3, xmm12
    movdqu xmmword ptr [rdi + 48], xmm3
    movdqu xmm12, xmmword ptr [rsi + 64]
    pxor xmm4, xmm12
    movdqu xmmword ptr [rdi + 64], xmm4
    movdqu xmm12, xmmword ptr [rsi + 80]
    pxor xmm5, xmm12
    movdqu xmmword ptr [rdi + 80], xmm5
    movdqu xmm12, xmmword ptr [rsi + 96]
    pxor xmm6, xmm12
    movdqu xmmword ptr [rdi + 96], xmm6
    movdqu xmm12, xmmword ptr [rsi + 112]
    pxor xmm7, xmm12
    movdqu xmmword ptr [rdi + 112], xmm7
    add rsi, 128
    add rdi, 128
    dec rdx
    jz .gfinal
.genc:
    mov r10, rdi
    sub r10, 128
    movdqa xmm0, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm0, xmmword ptr [rip + .gbswap]
    movdqa xmm1, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm1, xmmword ptr [rip + .gbswap]
    movdqa xmm2, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm2, xmmword ptr [rip + .gbswap]
    movdqa xmm3, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm3, xmmword ptr [rip + .gbswap]
    movdqa xmm4, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm4, xmmword ptr [rip + .gbswap]
    movdqa xmm5, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm5, xmmword ptr [rip + .gbswap]
    movdqa xmm6, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm6, xmmword ptr [rip + .gbswap]
    movdqa xmm7, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm7, xmmword ptr [rip + .gbswap]
    pxor xmm0, xmmword ptr [rax]
    pxor xmm1, xmmword ptr [rax]
    pxor xmm2, xmmword ptr [rax]
    pxor xmm3, xmmword ptr [rax]
    pxor xmm4, xmmword ptr [rax]
    pxor xmm5, xmmword ptr [rax]
    pxor xmm6, xmmword ptr [rax]
    pxor xmm7, xmmword ptr [rax]
    aesenc xmm0, xmmword ptr [rax + 16]
    aesenc xmm1, xmmword ptr [rax + 16]
    aesenc xmm2, xmmword ptr [rax + 16]
    aesenc xmm3, xmmword ptr [rax + 16]
    aesenc xmm4, xmmword ptr [rax + 16]
    aesenc xmm5, xmmword ptr [rax + 16]
    aesenc xmm6, xmmword ptr [rax + 16]
    aesenc xmm7, xmmword ptr [rax + 16]
    movdqu xmm12, xmmword ptr [r10 + 0]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    pxor xmm14, xmm8
    movdqa xmm9, xmm14
    pclmulqdq xmm9, xmmword ptr [rcx], 0x00
    movdqa xmm10, xmm14
    pclmulqdq xmm10, xmmword ptr [rcx], 0x11
    pshufd xmm11, xmm14, 0x4e
    pxor xmm11, xmm14
    pclmulqdq xmm11, xmmword ptr [rcx + 16], 0x00
    aesenc xmm0, xmmword ptr [rax + 32]
    aesenc xmm1, xmmword ptr [rax + 32]
    aesenc xmm2, xmmword ptr [rax + 32]
    aesenc xmm3, xmmword ptr [rax + 32]
    aesenc xmm4, xmmword ptr [rax + 32]
    aesenc xmm5, xmmword ptr [rax + 32]
    aesenc xmm6, xmmword ptr [rax + 32]
    aesenc xmm7, xmmword ptr [rax + 32]
    movdqu xmm12, xmmword ptr [r10 + 16]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 32], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 32], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 48], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 48]
    aesenc xmm1, xmmword ptr [rax + 48]
    aesenc xmm2, xmmword ptr [rax + 48]
    aesenc xmm3, xmmword ptr [rax + 48]
    aesenc xmm4, xmmword ptr [rax + 48]
    aesenc xmm5, xmmword ptr [rax + 48]
    aesenc xmm6, xmmword ptr [rax + 48]
    aesenc xmm7, xmmword ptr [rax + 48]
    movdqu xmm12, xmmword ptr [r10 + 32]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 64], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 64], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 80], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 64]
    aesenc xmm1, xmmword ptr [rax + 64]
    aesenc xmm2, xmmword ptr [rax + 64]
    aesenc xmm3, xmmword ptr [rax + 64]
    aesenc xmm4, xmmword ptr [rax + 64]
    aesenc xmm5, xmmword ptr [rax + 64]
    aesenc xmm6, xmmword ptr [rax + 64]
    aesenc xmm7, xmmword ptr [rax + 64]
    movdqu xmm12, xmmword ptr [r10 + 48]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 96], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 96], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 112], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 80]
    aesenc xmm1, xmmword ptr [rax + 80]
    aesenc xmm2, xmmword ptr [rax + 80]
    aesenc xmm3, xmmword ptr [rax + 80]
    aesenc xmm4, xmmword ptr [rax + 80]
    aesenc xmm5, xmmword ptr [rax + 80]
    aesenc xmm6, xmmword ptr [rax + 80]
    aesenc xmm7, xmmword ptr [rax + 80]
    movdqu xmm12, xmmword ptr [r10 + 64]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 128], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 128], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 144], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 96]
    aesenc xmm1, xmmword ptr [rax + 96]
    aesenc xmm2, xmmword ptr [rax + 96]
    aesenc xmm3, xmmword ptr [rax + 96]
    aesenc xmm4, xmmword ptr [rax + 96]
    aesenc xmm5, xmmword ptr [rax + 96]
    aesenc xmm6, xmmword ptr [rax + 96]
    aesenc xmm7, xmmword ptr [rax + 96]
    movdqu xmm12, xmmword ptr [r10 + 80]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 160], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 160], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 176], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 112]
    aesenc xmm1, xmmword ptr [rax + 112]
    aesenc xmm2, xmmword ptr [rax + 112]
    aesenc xmm3, xmmword ptr [rax + 112]
    aesenc xmm4, xmmword ptr [rax + 112]
    aesenc xmm5, xmmword ptr [rax + 112]
    aesenc xmm6, xmmword ptr [rax + 112]
    aesenc xmm7, xmmword ptr [rax + 112]
    movdqu xmm12, xmmword ptr [r10 + 96]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 192], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 192], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 208], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 128]
    aesenc xmm1, xmmword ptr [rax + 128]
    aesenc xmm2, xmmword ptr [rax + 128]
    aesenc xmm3, xmmword ptr [rax + 128]
    aesenc xmm4, xmmword ptr [rax + 128]
    aesenc xmm5, xmmword ptr [rax + 128]
    aesenc xmm6, xmmword ptr [rax + 128]
    aesenc xmm7, xmmword ptr [rax + 128]
    movdqu xmm12, xmmword ptr [r10 + 112]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 224], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 224], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 240], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 144]
    aesenc xmm1, xmmword ptr [rax + 144]
    aesenc xmm2, xmmword ptr [rax + 144]
    aesenc xmm3, xmmword ptr [rax + 144]
    aesenc xmm4, xmmword ptr [rax + 144]
    aesenc xmm5, xmmword ptr [rax + 144]
    aesenc xmm6, xmmword ptr [rax + 144]
    aesenc xmm7, xmmword ptr [rax + 144]
    pxor xmm11, xmm9
    pxor xmm11, xmm10                   // mid
    movdqa xmm12, xmm11
    pslldq xmm12, 8
    pxor xmm9, xmm12                    // lo ^= [0, m0]
    psrldq xmm11, 8
    pxor xmm10, xmm11                   // hi ^= [m1, 0]
    movdqa xmm12, xmm10
    pclmulqdq xmm12, xmmword ptr [rip + .gk], 0x01   // P3 * 0x87, at x^64
    movdqa xmm13, xmm12
    pslldq xmm13, 8
    pxor xmm9, xmm13
    psrldq xmm12, 8
    pxor xmm10, xmm12
    movdqa xmm12, xmm10
    pclmulqdq xmm12, xmmword ptr [rip + .gk], 0x00   // P2 * 0x87
    pxor xmm9, xmm12
    movdqa xmm8, xmm9
    cmp r8, 160
    je .gblast
    aesenc xmm0, xmmword ptr [rax + 160]
    aesenc xmm1, xmmword ptr [rax + 160]
    aesenc xmm2, xmmword ptr [rax + 160]
    aesenc xmm3, xmmword ptr [rax + 160]
    aesenc xmm4, xmmword ptr [rax + 160]
    aesenc xmm5, xmmword ptr [rax + 160]
    aesenc xmm6, xmmword ptr [rax + 160]
    aesenc xmm7, xmmword ptr [rax + 160]
    aesenc xmm0, xmmword ptr [rax + 176]
    aesenc xmm1, xmmword ptr [rax + 176]
    aesenc xmm2, xmmword ptr [rax + 176]
    aesenc xmm3, xmmword ptr [rax + 176]
    aesenc xmm4, xmmword ptr [rax + 176]
    aesenc xmm5, xmmword ptr [rax + 176]
    aesenc xmm6, xmmword ptr [rax + 176]
    aesenc xmm7, xmmword ptr [rax + 176]
    cmp r8, 192
    je .gblast
    aesenc xmm0, xmmword ptr [rax + 192]
    aesenc xmm1, xmmword ptr [rax + 192]
    aesenc xmm2, xmmword ptr [rax + 192]
    aesenc xmm3, xmmword ptr [rax + 192]
    aesenc xmm4, xmmword ptr [rax + 192]
    aesenc xmm5, xmmword ptr [rax + 192]
    aesenc xmm6, xmmword ptr [rax + 192]
    aesenc xmm7, xmmword ptr [rax + 192]
    aesenc xmm0, xmmword ptr [rax + 208]
    aesenc xmm1, xmmword ptr [rax + 208]
    aesenc xmm2, xmmword ptr [rax + 208]
    aesenc xmm3, xmmword ptr [rax + 208]
    aesenc xmm4, xmmword ptr [rax + 208]
    aesenc xmm5, xmmword ptr [rax + 208]
    aesenc xmm6, xmmword ptr [rax + 208]
    aesenc xmm7, xmmword ptr [rax + 208]
.gblast:
    aesenclast xmm0, xmmword ptr [rax + r8]
    aesenclast xmm1, xmmword ptr [rax + r8]
    aesenclast xmm2, xmmword ptr [rax + r8]
    aesenclast xmm3, xmmword ptr [rax + r8]
    aesenclast xmm4, xmmword ptr [rax + r8]
    aesenclast xmm5, xmmword ptr [rax + r8]
    aesenclast xmm6, xmmword ptr [rax + r8]
    aesenclast xmm7, xmmword ptr [rax + r8]
    movdqu xmm12, xmmword ptr [rsi + 0]
    pxor xmm0, xmm12
    movdqu xmmword ptr [rdi + 0], xmm0
    movdqu xmm12, xmmword ptr [rsi + 16]
    pxor xmm1, xmm12
    movdqu xmmword ptr [rdi + 16], xmm1
    movdqu xmm12, xmmword ptr [rsi + 32]
    pxor xmm2, xmm12
    movdqu xmmword ptr [rdi + 32], xmm2
    movdqu xmm12, xmmword ptr [rsi + 48]
    pxor xmm3, xmm12
    movdqu xmmword ptr [rdi + 48], xmm3
    movdqu xmm12, xmmword ptr [rsi + 64]
    pxor xmm4, xmm12
    movdqu xmmword ptr [rdi + 64], xmm4
    movdqu xmm12, xmmword ptr [rsi + 80]
    pxor xmm5, xmm12
    movdqu xmmword ptr [rdi + 80], xmm5
    movdqu xmm12, xmmword ptr [rsi + 96]
    pxor xmm6, xmm12
    movdqu xmmword ptr [rdi + 96], xmm6
    movdqu xmm12, xmmword ptr [rsi + 112]
    pxor xmm7, xmm12
    movdqu xmmword ptr [rdi + 112], xmm7
    add rsi, 128
    add rdi, 128
    dec rdx
    jnz .genc
.gfinal:
    mov r10, rdi
    sub r10, 128
    movdqu xmm12, xmmword ptr [r10 + 0]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    pxor xmm14, xmm8
    movdqa xmm9, xmm14
    pclmulqdq xmm9, xmmword ptr [rcx], 0x00
    movdqa xmm10, xmm14
    pclmulqdq xmm10, xmmword ptr [rcx], 0x11
    pshufd xmm11, xmm14, 0x4e
    pxor xmm11, xmm14
    pclmulqdq xmm11, xmmword ptr [rcx + 16], 0x00
    movdqu xmm12, xmmword ptr [r10 + 16]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 32], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 32], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 48], 0x00
    pxor xmm11, xmm12
    movdqu xmm12, xmmword ptr [r10 + 32]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 64], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 64], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 80], 0x00
    pxor xmm11, xmm12
    movdqu xmm12, xmmword ptr [r10 + 48]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 96], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 96], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 112], 0x00
    pxor xmm11, xmm12
    movdqu xmm12, xmmword ptr [r10 + 64]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 128], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 128], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 144], 0x00
    pxor xmm11, xmm12
    movdqu xmm12, xmmword ptr [r10 + 80]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 160], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 160], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 176], 0x00
    pxor xmm11, xmm12
    movdqu xmm12, xmmword ptr [r10 + 96]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 192], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 192], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 208], 0x00
    pxor xmm11, xmm12
    movdqu xmm12, xmmword ptr [r10 + 112]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 224], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 224], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 240], 0x00
    pxor xmm11, xmm12
    pxor xmm11, xmm9
    pxor xmm11, xmm10                   // mid
    movdqa xmm12, xmm11
    pslldq xmm12, 8
    pxor xmm9, xmm12                    // lo ^= [0, m0]
    psrldq xmm11, 8
    pxor xmm10, xmm11                   // hi ^= [m1, 0]
    movdqa xmm12, xmm10
    pclmulqdq xmm12, xmmword ptr [rip + .gk], 0x01   // P3 * 0x87, at x^64
    movdqa xmm13, xmm12
    pslldq xmm13, 8
    pxor xmm9, xmm13
    psrldq xmm12, 8
    pxor xmm10, xmm12
    movdqa xmm12, xmm10
    pclmulqdq xmm12, xmmword ptr [rip + .gk], 0x00   // P2 * 0x87
    pxor xmm9, xmm12
    movdqa xmm8, xmm9
    jmp .gend
.gdec:
    mov r10, rsi
    movdqa xmm0, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm0, xmmword ptr [rip + .gbswap]
    movdqa xmm1, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm1, xmmword ptr [rip + .gbswap]
    movdqa xmm2, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm2, xmmword ptr [rip + .gbswap]
    movdqa xmm3, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm3, xmmword ptr [rip + .gbswap]
    movdqa xmm4, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm4, xmmword ptr [rip + .gbswap]
    movdqa xmm5, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm5, xmmword ptr [rip + .gbswap]
    movdqa xmm6, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm6, xmmword ptr [rip + .gbswap]
    movdqa xmm7, xmm15
    paddd xmm15, xmmword ptr [rip + .gone]
    pshufb xmm7, xmmword ptr [rip + .gbswap]
    pxor xmm0, xmmword ptr [rax]
    pxor xmm1, xmmword ptr [rax]
    pxor xmm2, xmmword ptr [rax]
    pxor xmm3, xmmword ptr [rax]
    pxor xmm4, xmmword ptr [rax]
    pxor xmm5, xmmword ptr [rax]
    pxor xmm6, xmmword ptr [rax]
    pxor xmm7, xmmword ptr [rax]
    aesenc xmm0, xmmword ptr [rax + 16]
    aesenc xmm1, xmmword ptr [rax + 16]
    aesenc xmm2, xmmword ptr [rax + 16]
    aesenc xmm3, xmmword ptr [rax + 16]
    aesenc xmm4, xmmword ptr [rax + 16]
    aesenc xmm5, xmmword ptr [rax + 16]
    aesenc xmm6, xmmword ptr [rax + 16]
    aesenc xmm7, xmmword ptr [rax + 16]
    movdqu xmm12, xmmword ptr [r10 + 0]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    pxor xmm14, xmm8
    movdqa xmm9, xmm14
    pclmulqdq xmm9, xmmword ptr [rcx], 0x00
    movdqa xmm10, xmm14
    pclmulqdq xmm10, xmmword ptr [rcx], 0x11
    pshufd xmm11, xmm14, 0x4e
    pxor xmm11, xmm14
    pclmulqdq xmm11, xmmword ptr [rcx + 16], 0x00
    aesenc xmm0, xmmword ptr [rax + 32]
    aesenc xmm1, xmmword ptr [rax + 32]
    aesenc xmm2, xmmword ptr [rax + 32]
    aesenc xmm3, xmmword ptr [rax + 32]
    aesenc xmm4, xmmword ptr [rax + 32]
    aesenc xmm5, xmmword ptr [rax + 32]
    aesenc xmm6, xmmword ptr [rax + 32]
    aesenc xmm7, xmmword ptr [rax + 32]
    movdqu xmm12, xmmword ptr [r10 + 16]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 32], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 32], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 48], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 48]
    aesenc xmm1, xmmword ptr [rax + 48]
    aesenc xmm2, xmmword ptr [rax + 48]
    aesenc xmm3, xmmword ptr [rax + 48]
    aesenc xmm4, xmmword ptr [rax + 48]
    aesenc xmm5, xmmword ptr [rax + 48]
    aesenc xmm6, xmmword ptr [rax + 48]
    aesenc xmm7, xmmword ptr [rax + 48]
    movdqu xmm12, xmmword ptr [r10 + 32]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 64], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 64], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 80], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 64]
    aesenc xmm1, xmmword ptr [rax + 64]
    aesenc xmm2, xmmword ptr [rax + 64]
    aesenc xmm3, xmmword ptr [rax + 64]
    aesenc xmm4, xmmword ptr [rax + 64]
    aesenc xmm5, xmmword ptr [rax + 64]
    aesenc xmm6, xmmword ptr [rax + 64]
    aesenc xmm7, xmmword ptr [rax + 64]
    movdqu xmm12, xmmword ptr [r10 + 48]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 96], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 96], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 112], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 80]
    aesenc xmm1, xmmword ptr [rax + 80]
    aesenc xmm2, xmmword ptr [rax + 80]
    aesenc xmm3, xmmword ptr [rax + 80]
    aesenc xmm4, xmmword ptr [rax + 80]
    aesenc xmm5, xmmword ptr [rax + 80]
    aesenc xmm6, xmmword ptr [rax + 80]
    aesenc xmm7, xmmword ptr [rax + 80]
    movdqu xmm12, xmmword ptr [r10 + 64]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 128], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 128], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 144], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 96]
    aesenc xmm1, xmmword ptr [rax + 96]
    aesenc xmm2, xmmword ptr [rax + 96]
    aesenc xmm3, xmmword ptr [rax + 96]
    aesenc xmm4, xmmword ptr [rax + 96]
    aesenc xmm5, xmmword ptr [rax + 96]
    aesenc xmm6, xmmword ptr [rax + 96]
    aesenc xmm7, xmmword ptr [rax + 96]
    movdqu xmm12, xmmword ptr [r10 + 80]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 160], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 160], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 176], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 112]
    aesenc xmm1, xmmword ptr [rax + 112]
    aesenc xmm2, xmmword ptr [rax + 112]
    aesenc xmm3, xmmword ptr [rax + 112]
    aesenc xmm4, xmmword ptr [rax + 112]
    aesenc xmm5, xmmword ptr [rax + 112]
    aesenc xmm6, xmmword ptr [rax + 112]
    aesenc xmm7, xmmword ptr [rax + 112]
    movdqu xmm12, xmmword ptr [r10 + 96]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 192], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 192], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 208], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 128]
    aesenc xmm1, xmmword ptr [rax + 128]
    aesenc xmm2, xmmword ptr [rax + 128]
    aesenc xmm3, xmmword ptr [rax + 128]
    aesenc xmm4, xmmword ptr [rax + 128]
    aesenc xmm5, xmmword ptr [rax + 128]
    aesenc xmm6, xmmword ptr [rax + 128]
    aesenc xmm7, xmmword ptr [rax + 128]
    movdqu xmm12, xmmword ptr [r10 + 112]
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 224], 0x00
    pxor xmm9, xmm12
    movdqa xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 224], 0x11
    pxor xmm10, xmm12
    pshufd xmm12, xmm14, 0x4e
    pxor xmm12, xmm14
    pclmulqdq xmm12, xmmword ptr [rcx + 240], 0x00
    pxor xmm11, xmm12
    aesenc xmm0, xmmword ptr [rax + 144]
    aesenc xmm1, xmmword ptr [rax + 144]
    aesenc xmm2, xmmword ptr [rax + 144]
    aesenc xmm3, xmmword ptr [rax + 144]
    aesenc xmm4, xmmword ptr [rax + 144]
    aesenc xmm5, xmmword ptr [rax + 144]
    aesenc xmm6, xmmword ptr [rax + 144]
    aesenc xmm7, xmmword ptr [rax + 144]
    pxor xmm11, xmm9
    pxor xmm11, xmm10                   // mid
    movdqa xmm12, xmm11
    pslldq xmm12, 8
    pxor xmm9, xmm12                    // lo ^= [0, m0]
    psrldq xmm11, 8
    pxor xmm10, xmm11                   // hi ^= [m1, 0]
    movdqa xmm12, xmm10
    pclmulqdq xmm12, xmmword ptr [rip + .gk], 0x01   // P3 * 0x87, at x^64
    movdqa xmm13, xmm12
    pslldq xmm13, 8
    pxor xmm9, xmm13
    psrldq xmm12, 8
    pxor xmm10, xmm12
    movdqa xmm12, xmm10
    pclmulqdq xmm12, xmmword ptr [rip + .gk], 0x00   // P2 * 0x87
    pxor xmm9, xmm12
    movdqa xmm8, xmm9
    cmp r8, 160
    je .gclast
    aesenc xmm0, xmmword ptr [rax + 160]
    aesenc xmm1, xmmword ptr [rax + 160]
    aesenc xmm2, xmmword ptr [rax + 160]
    aesenc xmm3, xmmword ptr [rax + 160]
    aesenc xmm4, xmmword ptr [rax + 160]
    aesenc xmm5, xmmword ptr [rax + 160]
    aesenc xmm6, xmmword ptr [rax + 160]
    aesenc xmm7, xmmword ptr [rax + 160]
    aesenc xmm0, xmmword ptr [rax + 176]
    aesenc xmm1, xmmword ptr [rax + 176]
    aesenc xmm2, xmmword ptr [rax + 176]
    aesenc xmm3, xmmword ptr [rax + 176]
    aesenc xmm4, xmmword ptr [rax + 176]
    aesenc xmm5, xmmword ptr [rax + 176]
    aesenc xmm6, xmmword ptr [rax + 176]
    aesenc xmm7, xmmword ptr [rax + 176]
    cmp r8, 192
    je .gclast
    aesenc xmm0, xmmword ptr [rax + 192]
    aesenc xmm1, xmmword ptr [rax + 192]
    aesenc xmm2, xmmword ptr [rax + 192]
    aesenc xmm3, xmmword ptr [rax + 192]
    aesenc xmm4, xmmword ptr [rax + 192]
    aesenc xmm5, xmmword ptr [rax + 192]
    aesenc xmm6, xmmword ptr [rax + 192]
    aesenc xmm7, xmmword ptr [rax + 192]
    aesenc xmm0, xmmword ptr [rax + 208]
    aesenc xmm1, xmmword ptr [rax + 208]
    aesenc xmm2, xmmword ptr [rax + 208]
    aesenc xmm3, xmmword ptr [rax + 208]
    aesenc xmm4, xmmword ptr [rax + 208]
    aesenc xmm5, xmmword ptr [rax + 208]
    aesenc xmm6, xmmword ptr [rax + 208]
    aesenc xmm7, xmmword ptr [rax + 208]
.gclast:
    aesenclast xmm0, xmmword ptr [rax + r8]
    aesenclast xmm1, xmmword ptr [rax + r8]
    aesenclast xmm2, xmmword ptr [rax + r8]
    aesenclast xmm3, xmmword ptr [rax + r8]
    aesenclast xmm4, xmmword ptr [rax + r8]
    aesenclast xmm5, xmmword ptr [rax + r8]
    aesenclast xmm6, xmmword ptr [rax + r8]
    aesenclast xmm7, xmmword ptr [rax + r8]
    movdqu xmm12, xmmword ptr [rsi + 0]
    pxor xmm0, xmm12
    movdqu xmmword ptr [rdi + 0], xmm0
    movdqu xmm12, xmmword ptr [rsi + 16]
    pxor xmm1, xmm12
    movdqu xmmword ptr [rdi + 16], xmm1
    movdqu xmm12, xmmword ptr [rsi + 32]
    pxor xmm2, xmm12
    movdqu xmmword ptr [rdi + 32], xmm2
    movdqu xmm12, xmmword ptr [rsi + 48]
    pxor xmm3, xmm12
    movdqu xmmword ptr [rdi + 48], xmm3
    movdqu xmm12, xmmword ptr [rsi + 64]
    pxor xmm4, xmm12
    movdqu xmmword ptr [rdi + 64], xmm4
    movdqu xmm12, xmmword ptr [rsi + 80]
    pxor xmm5, xmm12
    movdqu xmmword ptr [rdi + 80], xmm5
    movdqu xmm12, xmmword ptr [rsi + 96]
    pxor xmm6, xmm12
    movdqu xmmword ptr [rdi + 96], xmm6
    movdqu xmm12, xmmword ptr [rsi + 112]
    pxor xmm7, xmm12
    movdqu xmmword ptr [rdi + 112], xmm7
    add rsi, 128
    add rdi, 128
    dec rdx
    jnz .gdec
.gend:
    mov r10, qword ptr [rsp + 64]
    movdqa xmm12, xmm8
    movdqa xmm13, xmm12
    pand xmm13, xmmword ptr [rip + .gnibble]
    psrlw xmm12, 4
    pand xmm12, xmmword ptr [rip + .gnibble]
    movdqu xmm14, xmmword ptr [rip + .grev_lo]
    pshufb xmm14, xmm13
    movdqu xmm13, xmmword ptr [rip + .grev_hi]
    pshufb xmm13, xmm12
    por xmm14, xmm13
    movdqu xmmword ptr [r10], xmm14
    pshufb xmm15, xmmword ptr [rip + .gbswap]
    movdqu xmmword ptr [r9], xmm15
.gdone:
    ret
    .balign 16
.gbswap:
    .byte 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0
.gone:
    .quad 1, 0
.grev_lo:
    .byte 0x00, 0x80, 0x40, 0xc0, 0x20, 0xa0, 0x60, 0xe0, 0x10, 0x90, 0x50, 0xd0, 0x30, 0xb0, 0x70, 0xf0
.grev_hi:
    .byte 0x00, 0x08, 0x04, 0x0c, 0x02, 0x0a, 0x06, 0x0e, 0x01, 0x09, 0x05, 0x0d, 0x03, 0x0b, 0x07, 0x0f
.gnibble:
    .byte 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f, 0x0f
.gk:
    .quad 0x87, 0x87
