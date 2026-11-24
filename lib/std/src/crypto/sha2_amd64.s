// crypto/sha2.navi's #[asm] functions for amd64 (the SHA extensions).

// sha256_blocks(h, data, n, k): the n 64-byte blocks of data into the
// state h[0..8] (k: the 64 round constants), on sha256rnds2 and
// sha256msg1/2 as Go's blockSHANI (Intel's reference): the state as ABEF
// and CDGH, the message in xmm0 (sha256rnds2 reads it there).
sha256_blocks:
    mov rdi, qword ptr [rsp + 8]        // h
    mov rsi, qword ptr [rsp + 16]       // data
    mov rdx, qword ptr [rsp + 24]       // n
    mov rax, qword ptr [rsp + 32]       // k
    test rdx, rdx
    jz .done
    movdqu xmm1, xmmword ptr [rdi]
    movdqu xmm2, xmmword ptr [rdi + 16]
    pshufd xmm1, xmm1, 0xb1             // CDAB
    pshufd xmm2, xmm2, 0x1b             // EFGH
    movdqa xmm7, xmm1
    palignr xmm1, xmm2, 8               // ABEF
    pblendw xmm2, xmm7, 0xf0            // CDGH
    movdqu xmm8, xmmword ptr [rip + .flip]
.loop:
    movdqa xmm9, xmm1
    movdqa xmm10, xmm2
    movdqu xmm0, xmmword ptr [rsi + 0]
    pshufb xmm0, xmm8
    movdqa xmm3, xmm0
    paddd xmm0, xmmword ptr [rax + 0]
    sha256rnds2 xmm2, xmm1, xmm0
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    movdqu xmm0, xmmword ptr [rsi + 16]
    pshufb xmm0, xmm8
    movdqa xmm4, xmm0
    paddd xmm0, xmmword ptr [rax + 16]
    sha256rnds2 xmm2, xmm1, xmm0
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm3, xmm4
    movdqu xmm0, xmmword ptr [rsi + 32]
    pshufb xmm0, xmm8
    movdqa xmm5, xmm0
    paddd xmm0, xmmword ptr [rax + 32]
    sha256rnds2 xmm2, xmm1, xmm0
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm4, xmm5
    movdqu xmm0, xmmword ptr [rsi + 48]
    pshufb xmm0, xmm8
    movdqa xmm6, xmm0
    paddd xmm0, xmmword ptr [rax + 48]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm6
    palignr xmm7, xmm5, 4
    paddd xmm3, xmm7
    sha256msg2 xmm3, xmm6
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm5, xmm6
    movdqa xmm0, xmm3
    paddd xmm0, xmmword ptr [rax + 64]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm3
    palignr xmm7, xmm6, 4
    paddd xmm4, xmm7
    sha256msg2 xmm4, xmm3
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm6, xmm3
    movdqa xmm0, xmm4
    paddd xmm0, xmmword ptr [rax + 80]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm4
    palignr xmm7, xmm3, 4
    paddd xmm5, xmm7
    sha256msg2 xmm5, xmm4
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm3, xmm4
    movdqa xmm0, xmm5
    paddd xmm0, xmmword ptr [rax + 96]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm5
    palignr xmm7, xmm4, 4
    paddd xmm6, xmm7
    sha256msg2 xmm6, xmm5
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm4, xmm5
    movdqa xmm0, xmm6
    paddd xmm0, xmmword ptr [rax + 112]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm6
    palignr xmm7, xmm5, 4
    paddd xmm3, xmm7
    sha256msg2 xmm3, xmm6
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm5, xmm6
    movdqa xmm0, xmm3
    paddd xmm0, xmmword ptr [rax + 128]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm3
    palignr xmm7, xmm6, 4
    paddd xmm4, xmm7
    sha256msg2 xmm4, xmm3
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm6, xmm3
    movdqa xmm0, xmm4
    paddd xmm0, xmmword ptr [rax + 144]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm4
    palignr xmm7, xmm3, 4
    paddd xmm5, xmm7
    sha256msg2 xmm5, xmm4
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm3, xmm4
    movdqa xmm0, xmm5
    paddd xmm0, xmmword ptr [rax + 160]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm5
    palignr xmm7, xmm4, 4
    paddd xmm6, xmm7
    sha256msg2 xmm6, xmm5
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm4, xmm5
    movdqa xmm0, xmm6
    paddd xmm0, xmmword ptr [rax + 176]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm6
    palignr xmm7, xmm5, 4
    paddd xmm3, xmm7
    sha256msg2 xmm3, xmm6
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm5, xmm6
    movdqa xmm0, xmm3
    paddd xmm0, xmmword ptr [rax + 192]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm3
    palignr xmm7, xmm6, 4
    paddd xmm4, xmm7
    sha256msg2 xmm4, xmm3
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    sha256msg1 xmm6, xmm3
    movdqa xmm0, xmm4
    paddd xmm0, xmmword ptr [rax + 208]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm4
    palignr xmm7, xmm3, 4
    paddd xmm5, xmm7
    sha256msg2 xmm5, xmm4
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    movdqa xmm0, xmm5
    paddd xmm0, xmmword ptr [rax + 224]
    sha256rnds2 xmm2, xmm1, xmm0
    movdqa xmm7, xmm5
    palignr xmm7, xmm4, 4
    paddd xmm6, xmm7
    sha256msg2 xmm6, xmm5
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    movdqa xmm0, xmm6
    paddd xmm0, xmmword ptr [rax + 240]
    sha256rnds2 xmm2, xmm1, xmm0
    pshufd xmm0, xmm0, 0x0e
    sha256rnds2 xmm1, xmm2, xmm0
    paddd xmm1, xmm9
    paddd xmm2, xmm10
    add rsi, 64
    dec rdx
    jnz .loop
    pshufd xmm1, xmm1, 0x1b             // FEBA
    pshufd xmm2, xmm2, 0xb1             // DCHG
    movdqa xmm7, xmm1
    pblendw xmm1, xmm2, 0xf0            // DCBA
    palignr xmm2, xmm7, 8               // HGFE
    movdqu xmmword ptr [rdi], xmm1
    movdqu xmmword ptr [rdi + 16], xmm2
.done:
    ret
// pshufb's mask: each 32-bit word's bytes reversed.
.flip:
    .quad 0x0405060700010203
    .quad 0x0c0d0e0f08090a0b

// cpuid7_ebx(): EBX of CPUID leaf 7 (SHA: bit 29).
cpuid7_ebx:
    push rbx
    mov eax, 7
    xor ecx, ecx
    cpuid
    mov eax, ebx
    pop rbx
    mov qword ptr [r13], rax
    ret

// cpuid1_ecx(): ECX of CPUID leaf 1 (SSSE3: bit 9, SSE4.1: bit 19).
cpuid1_ecx:
    push rbx
    mov eax, 1
    xor ecx, ecx
    cpuid
    mov eax, ecx
    pop rbx
    mov qword ptr [r13], rax
    ret
