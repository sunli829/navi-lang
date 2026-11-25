// crypto/nat.navi's #[asm] functions for amd64.

// mont_mul(t, a, b, m, n, m0inv, out): as nat_arm64.s's, z += x * y as Go's
// addMulVVW without ADX (each limb's product (mul) plus the carry, plus
// z's limb; four limbs at a time); the rows left in r11.
mont_mul:
    mov rdi, qword ptr [rsp + 8]
    mov rcx, qword ptr [rsp + 40]
    add rcx, rcx
    inc rcx
    xor rax, rax
.z:
    mov qword ptr [rdi], rax
    add rdi, 8
    dec rcx
    jnz .z
    mov r11, qword ptr [rsp + 40]
    test r11, r11
    jz .rdone
.row:
    mov rdi, qword ptr [rsp + 8]
    mov rsi, qword ptr [rsp + 16]
    mov rcx, qword ptr [rsp + 40]
    mov rax, qword ptr [rsp + 24]
    mov r8, qword ptr [rax]
    add rax, 8
    mov qword ptr [rsp + 24], rax
    mov r9, 0
.a41:
    cmp rcx, 4
    jb .at1
    mov rax, qword ptr [rsi]
    mul r8
    add rax, r9
    adc rdx, 0
    mov r10, qword ptr [rdi]
    add r10, rax
    adc rdx, 0
    mov qword ptr [rdi], r10
    mov r9, rdx
    mov rax, qword ptr [rsi + 8]
    mul r8
    add rax, r9
    adc rdx, 0
    mov r10, qword ptr [rdi + 8]
    add r10, rax
    adc rdx, 0
    mov qword ptr [rdi + 8], r10
    mov r9, rdx
    mov rax, qword ptr [rsi + 16]
    mul r8
    add rax, r9
    adc rdx, 0
    mov r10, qword ptr [rdi + 16]
    add r10, rax
    adc rdx, 0
    mov qword ptr [rdi + 16], r10
    mov r9, rdx
    mov rax, qword ptr [rsi + 24]
    mul r8
    add rax, r9
    adc rdx, 0
    mov r10, qword ptr [rdi + 24]
    add r10, rax
    adc rdx, 0
    mov qword ptr [rdi + 24], r10
    mov r9, rdx
    add rsi, 32
    add rdi, 32
    sub rcx, 4
    jmp .a41
.at1:
    test rcx, rcx
    jz .ad1
    mov rax, qword ptr [rsi]
    mul r8
    add rax, r9
    adc rdx, 0
    mov r10, qword ptr [rdi]
    add r10, rax
    adc rdx, 0
    mov qword ptr [rdi], r10
    mov r9, rdx
    add rsi, 8
    add rdi, 8
    dec rcx
    jmp .at1
.ad1:
    mov rax, qword ptr [rdi]
    add rax, r9
    mov qword ptr [rdi], rax
    mov r10, 0
    adc r10, 0
    mov qword ptr [rdi + 8], r10
    mov rdi, qword ptr [rsp + 8]
    mov rax, qword ptr [rdi]
    mov r8, qword ptr [rsp + 48]
    mul r8
    mov r8, rax
    mov rsi, qword ptr [rsp + 32]
    mov rcx, qword ptr [rsp + 40]
    mov r9, 0
.a42:
    cmp rcx, 4
    jb .at2
    mov rax, qword ptr [rsi]
    mul r8
    add rax, r9
    adc rdx, 0
    mov r10, qword ptr [rdi]
    add r10, rax
    adc rdx, 0
    mov qword ptr [rdi], r10
    mov r9, rdx
    mov rax, qword ptr [rsi + 8]
    mul r8
    add rax, r9
    adc rdx, 0
    mov r10, qword ptr [rdi + 8]
    add r10, rax
    adc rdx, 0
    mov qword ptr [rdi + 8], r10
    mov r9, rdx
    mov rax, qword ptr [rsi + 16]
    mul r8
    add rax, r9
    adc rdx, 0
    mov r10, qword ptr [rdi + 16]
    add r10, rax
    adc rdx, 0
    mov qword ptr [rdi + 16], r10
    mov r9, rdx
    mov rax, qword ptr [rsi + 24]
    mul r8
    add rax, r9
    adc rdx, 0
    mov r10, qword ptr [rdi + 24]
    add r10, rax
    adc rdx, 0
    mov qword ptr [rdi + 24], r10
    mov r9, rdx
    add rsi, 32
    add rdi, 32
    sub rcx, 4
    jmp .a42
.at2:
    test rcx, rcx
    jz .ad2
    mov rax, qword ptr [rsi]
    mul r8
    add rax, r9
    adc rdx, 0
    mov r10, qword ptr [rdi]
    add r10, rax
    adc rdx, 0
    mov qword ptr [rdi], r10
    mov r9, rdx
    add rsi, 8
    add rdi, 8
    dec rcx
    jmp .at2
.ad2:
    mov rax, qword ptr [rdi]
    add rax, r9
    mov qword ptr [rdi], rax
    mov rax, qword ptr [rdi + 8]
    adc rax, 0
    mov qword ptr [rdi + 8], rax
    mov rdi, qword ptr [rsp + 8]
    add rdi, 8
    mov qword ptr [rsp + 8], rdi
    dec r11
    jnz .row
    mov rsi, qword ptr [rsp + 8]
    mov rdx, qword ptr [rsp + 32]
    mov rdi, qword ptr [rsp + 56]
    mov rcx, qword ptr [rsp + 40]
    xor r9, r9                  // (and no borrow)
.sb:
    mov rax, qword ptr [rsi + r9*8]
    mov r10, qword ptr [rdx + r9*8]
    sbb rax, r10
    mov qword ptr [rdi + r9*8], rax
    lea r9, [r9 + 1]
    dec rcx
    jnz .sb
    mov r8, 0
    adc r8, 0                   // the borrow
    mov r10, qword ptr [rsi + r9*8]
    xor r10, 1
    and r8, r10
    neg r8                      // all ones: t, below m
    mov rcx, qword ptr [rsp + 40]
    xor r9, r9
.ks:
    mov rax, qword ptr [rsi + r9*8]
    mov r10, qword ptr [rdi + r9*8]
    xor rax, r10
    and rax, r8
    xor r10, rax
    mov qword ptr [rdi + r9*8], r10
    inc r9
    dec rcx
    jnz .ks
.rdone:
    ret

// ct_select(table, n, count, idx, out): as nat_arm64.s's.
ct_select:
    mov rsi, qword ptr [rsp + 8]
    mov r8, qword ptr [rsp + 16]
    mov r9, qword ptr [rsp + 24]
    mov r10, qword ptr [rsp + 32]
    mov rdi, qword ptr [rsp + 40]
    xor rax, rax
    xor r11, r11
.cz:
    mov qword ptr [rdi + r11*8], rax
    inc r11
    cmp r11, r8
    jne .cz
    xor rdx, rdx
.ce:
    mov rax, rdx
    xor rax, r10
    mov r11, rax
    neg r11
    or rax, r11
    shr rax, 63
    sub rax, 1                  // all ones when j == idx
    xor r11, r11
.cl:
    mov rcx, qword ptr [rsi]
    and rcx, rax
    or rcx, qword ptr [rdi + r11*8]
    mov qword ptr [rdi + r11*8], rcx
    add rsi, 8
    inc r11
    cmp r11, r8
    jne .cl
    inc rdx
    cmp rdx, r9
    jne .ce
    ret
