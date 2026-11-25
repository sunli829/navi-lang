// crypto/nat.navi's #[asm] functions for amd64.

// add_mul_vvw(z, x, n, y): z += x * y over n limbs, returning the carry
// word, as Go's addMulVVW without ADX: each limb's product (mul) plus the
// carry, plus z's limb; four limbs at a time.
add_mul_vvw:
    mov rdi, qword ptr [rsp + 8]
    mov rsi, qword ptr [rsp + 16]
    mov rcx, qword ptr [rsp + 24]
    mov r8, qword ptr [rsp + 32]
    mov r9, 0
.loop4:
    cmp rcx, 4
    jb .tail
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
    jmp .loop4
.tail:
    test rcx, rcx
    jz .done
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
    jmp .tail
.done:
    mov qword ptr [r13], r9
    ret
