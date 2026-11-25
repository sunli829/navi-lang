// crypto/chacha20poly1305.navi's #[asm] functions for amd64: ChaCha20 a
// block at a time in SSE2 (the rows in xmm0-xmm3, the diagonals by
// pshufd), Poly1305's blocks with mul and the carry instructions.

// chacha20_blocks(state, dst, src, n): dst = src ^ the key stream of n
// 64-byte blocks; state: the block's 16 words, its counter advanced.
chacha20_blocks:
    mov rsi, qword ptr [rsp + 8]
    mov rdi, qword ptr [rsp + 16]
    mov rdx, qword ptr [rsp + 24]
    mov rcx, qword ptr [rsp + 32]
    cmp rcx, 0
    je .done
.block:
    movdqu xmm0, xmmword ptr [rsi]
    movdqu xmm1, xmmword ptr [rsi + 16]
    movdqu xmm2, xmmword ptr [rsi + 32]
    movdqu xmm3, xmmword ptr [rsi + 48]
    mov r8, 10
.round:
    // The column round
    paddd xmm0, xmm1
    pxor xmm3, xmm0
    movdqa xmm4, xmm3
    pslld xmm3, 16
    psrld xmm4, 16
    por xmm3, xmm4
    paddd xmm2, xmm3
    pxor xmm1, xmm2
    movdqa xmm4, xmm1
    pslld xmm1, 12
    psrld xmm4, 20
    por xmm1, xmm4
    paddd xmm0, xmm1
    pxor xmm3, xmm0
    movdqa xmm4, xmm3
    pslld xmm3, 8
    psrld xmm4, 24
    por xmm3, xmm4
    paddd xmm2, xmm3
    pxor xmm1, xmm2
    movdqa xmm4, xmm1
    pslld xmm1, 7
    psrld xmm4, 25
    por xmm1, xmm4
    // The diagonal round: rows 1-3 turned left by 1-3 words
    pshufd xmm1, xmm1, 0x39
    pshufd xmm2, xmm2, 0x4e
    pshufd xmm3, xmm3, 0x93
    paddd xmm0, xmm1
    pxor xmm3, xmm0
    movdqa xmm4, xmm3
    pslld xmm3, 16
    psrld xmm4, 16
    por xmm3, xmm4
    paddd xmm2, xmm3
    pxor xmm1, xmm2
    movdqa xmm4, xmm1
    pslld xmm1, 12
    psrld xmm4, 20
    por xmm1, xmm4
    paddd xmm0, xmm1
    pxor xmm3, xmm0
    movdqa xmm4, xmm3
    pslld xmm3, 8
    psrld xmm4, 24
    por xmm3, xmm4
    paddd xmm2, xmm3
    pxor xmm1, xmm2
    movdqa xmm4, xmm1
    pslld xmm1, 7
    psrld xmm4, 25
    por xmm1, xmm4
    pshufd xmm1, xmm1, 0x93
    pshufd xmm2, xmm2, 0x4e
    pshufd xmm3, xmm3, 0x39
    dec r8
    jne .round
    // Plus the input (movdqu: legacy SSE operands must be aligned), xor
    movdqu xmm4, xmmword ptr [rsi]
    paddd xmm0, xmm4
    movdqu xmm4, xmmword ptr [rsi + 16]
    paddd xmm1, xmm4
    movdqu xmm4, xmmword ptr [rsi + 32]
    paddd xmm2, xmm4
    movdqu xmm4, xmmword ptr [rsi + 48]
    paddd xmm3, xmm4
    movdqu xmm4, xmmword ptr [rdx]
    pxor xmm0, xmm4
    movdqu xmm4, xmmword ptr [rdx + 16]
    pxor xmm1, xmm4
    movdqu xmm4, xmmword ptr [rdx + 32]
    pxor xmm2, xmm4
    movdqu xmm4, xmmword ptr [rdx + 48]
    pxor xmm3, xmm4
    movdqu xmmword ptr [rdi], xmm0
    movdqu xmmword ptr [rdi + 16], xmm1
    movdqu xmmword ptr [rdi + 32], xmm2
    movdqu xmmword ptr [rdi + 48], xmm3
    add rdx, 64
    add rdi, 64
    mov eax, dword ptr [rsi + 48]
    add eax, 1
    mov dword ptr [rsi + 48], eax
    dec rcx
    jne .block
.done:
    ret

// poly1305_blocks(h, r, m, n, hibit): as the arm64 one (Go's
// updateGeneric); rbx, rbp and r12 pushed for the duration.
poly1305_blocks:
    mov rax, qword ptr [rsp + 8]
    mov rsi, qword ptr [rsp + 16]
    mov r9, qword ptr [rsp + 24]
    mov r10, qword ptr [rsp + 32]
    mov r11, qword ptr [rsp + 40]
    cmp r10, 0
    je .pdone
    push rbx
    push rbp
    push r12
    mov rbx, qword ptr [rax]
    mov rbp, qword ptr [rax + 8]
    mov r12, qword ptr [rax + 16]
.ploop:
    // h += m (+ hibit at 2^128)
    add rbx, qword ptr [r9]
    adc rbp, qword ptr [r9 + 8]
    adc r12, r11
    // t (rcx, rdi, r8, r15) = h r
    mov rax, qword ptr [rsi]
    mul rbx
    mov rcx, rax
    mov rdi, rdx
    mov rax, qword ptr [rsi]
    mul rbp
    add rdi, rax
    adc rdx, 0
    mov r8, rdx
    mov r15, 0
    mov rax, qword ptr [rsi + 8]
    mul rbx
    add rdi, rax
    adc r8, rdx
    adc r15, 0
    mov rax, qword ptr [rsi + 8]
    mul rbp
    add r8, rax
    adc r15, rdx
    mov rax, qword ptr [rsi]
    mul r12
    add r8, rax
    adc r15, 0
    mov rax, qword ptr [rsi + 8]
    mul r12
    add r15, rax
    // h = t mod 2^130, plus 5 times the rest: cc = t - h (4c), then cc >> 2
    mov r12, r8
    and r12, 3
    and r8, -4
    mov rbx, rcx
    add rbx, r8
    mov rbp, rdi
    adc rbp, r15
    adc r12, 0
    shr r8, 2
    mov rax, r15
    shl rax, 62
    or r8, rax
    shr r15, 2
    add rbx, r8
    adc rbp, r15
    adc r12, 0
    add r9, 16
    dec r10
    jne .ploop
    mov rax, qword ptr [rsp + 32]
    mov qword ptr [rax], rbx
    mov qword ptr [rax + 8], rbp
    mov qword ptr [rax + 16], r12
    pop r12
    pop rbp
    pop rbx
.pdone:
    ret
