// crypto/aes.navi's #[asm] functions for amd64 (AES-NI).

// aes_encrypt_blocks(xk, rounds, src, dst, n): n blocks from src to dst
// with the round keys xk (16 bytes each, rounds + 1).
aes_encrypt_blocks:
    mov rax, qword ptr [rsp + 8]        // xk
    mov rcx, qword ptr [rsp + 16]       // rounds: 10, 12 or 14
    mov rsi, qword ptr [rsp + 24]       // src
    mov rdi, qword ptr [rsp + 32]       // dst
    mov rdx, qword ptr [rsp + 40]       // n
    test rdx, rdx
    jz .done
    // The round keys k0-k14 in xmm1-xmm15 (as many as there are).
    movdqu xmm1, xmmword ptr [rax]
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
    cmp rcx, 10
    je .loop
    movdqu xmm12, xmmword ptr [rax + 176]
    movdqu xmm13, xmmword ptr [rax + 192]
    cmp rcx, 12
    je .loop
    movdqu xmm14, xmmword ptr [rax + 208]
    movdqu xmm15, xmmword ptr [rax + 224]
.loop:
    movdqu xmm0, xmmword ptr [rsi]
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
    cmp rcx, 10
    je .last10
    aesenc xmm0, xmm11
    aesenc xmm0, xmm12
    cmp rcx, 12
    je .last12
    aesenc xmm0, xmm13
    aesenc xmm0, xmm14
    aesenclast xmm0, xmm15
    jmp .store
.last12:
    aesenclast xmm0, xmm13
    jmp .store
.last10:
    aesenclast xmm0, xmm11
.store:
    movdqu xmmword ptr [rdi], xmm0
    add rsi, 16
    add rdi, 16
    dec rdx
    jnz .loop
.done:
    ret

// cpuid1_ecx(): ECX of CPUID leaf 1.
cpuid1_ecx:
    push rbx                            // (cpuid writes rbx)
    mov eax, 1
    xor ecx, ecx
    cpuid
    mov eax, ecx
    pop rbx
    mov qword ptr [r13], rax
    ret
