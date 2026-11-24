// tests/run/asm.navi's #[asm] functions for amd64 (Intel syntax).

add3:
    mov rax, qword ptr [rsp + 8]
    add rax, qword ptr [rsp + 16]
    add rax, qword ptr [rsp + 24]
    mov qword ptr [r13], rax
    ret

sum_bytes:
    mov rsi, qword ptr [rsp + 8]        // p
    mov rcx, qword ptr [rsp + 16]       // n
    xor eax, eax
    test rcx, rcx
    jz .done
.loop:
    movzx edx, byte ptr [rsi]
    add rax, rdx
    inc rsi
    dec rcx
    jnz .loop
.done:
    mov qword ptr [r13], rax
    ret

aes_round:
    mov rax, qword ptr [rsp + 8]
    mov rcx, qword ptr [rsp + 16]
    movdqu xmm0, xmmword ptr [rax]
    movdqu xmm1, xmmword ptr [rcx]
    // aese xors the key first; aesenc xors its key last (here 0).
    pxor xmm0, xmm1
    pxor xmm2, xmm2
    aesenc xmm0, xmm2
    movdqu xmmword ptr [rax], xmm0
    ret

consts:
    mov rax, qword ptr [rip + .k]
    xor rax, qword ptr [rip + .k + 8]
    mov rcx, 0xff00ff00ff00ff00
    and rax, rcx
    mov qword ptr [r13], rax
    ret
    .balign 8
.k:
    .quad 0x1234567890abcdef
    .quad 0x0f0f0f0f0f0f0f0f
