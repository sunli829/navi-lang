// tests/run/asm.navi's #[asm] functions for arm64.

add3:
    ldr x0, [sp]
    ldr x1, [sp, #8]
    ldr x2, [sp, #16]
    add x0, x0, x1
    add x0, x0, x2
    str x0, [x8]
    ret

sum_bytes:
    ldr x0, [sp]            // p
    ldr x1, [sp, #8]        // n
    mov x2, #0
    cbz x1, .done
.loop:
    ldrb w3, [x0], #1
    add x2, x2, x3
    subs x1, x1, #1
    b.ne .loop
.done:
    str x2, [x8]
    ret

aes_round:
    ldr x0, [sp]
    ldr x1, [sp, #8]
    ld1 {v0.16b}, [x0]
    ld1 {v1.16b}, [x1]
    aese v0.16b, v1.16b
    aesmc v0.16b, v0.16b
    st1 {v0.16b}, [x0]
    ret

consts:
    adr x0, .k
    ldr x1, [x0]
    ldr x2, [x0, #8]
    eor x1, x1, x2
    mov x3, #0xff00ff00ff00ff00
    and x1, x1, x3
    str x1, [x8]
    ret
    .balign 8
.k:
    .quad 0x1234567890abcdef
    .quad 0x0f0f0f0f0f0f0f0f
