// crypto/nat.navi's #[asm] functions for arm64.

// add_mul_vvw(z, x, n, y): z += x * y over n limbs, returning the carry
// word, as Go's addMulVVW (crypto/internal/fips140/bigmod/nat_arm64.s):
// four limbs at a time, the low halves and the high halves added by two
// carry chains.
add_mul_vvw:
    ldr x1, [sp]
    ldr x2, [sp, #8]
    ldr x0, [sp, #16]
    ldr x3, [sp, #24]
    mov x4, #0
.loop4:
    cmp x0, #4
    b.lo .tail
    ldp x5, x6, [x2], #16
    ldp x7, x16, [x2], #16
    ldp x9, x10, [x1]
    ldp x11, x12, [x1, #16]
    mul x13, x5, x3
    mul x14, x6, x3
    mul x15, x7, x3
    mul x17, x16, x3
    adds x9, x9, x4
    adcs x10, x10, x14
    adcs x11, x11, x15
    adcs x12, x12, x17
    umulh x4, x16, x3
    adc x4, x4, xzr
    umulh x14, x5, x3
    umulh x15, x6, x3
    umulh x17, x7, x3
    adds x9, x9, x13
    adcs x10, x10, x14
    adcs x11, x11, x15
    adcs x12, x12, x17
    adc x4, x4, xzr
    stp x9, x10, [x1], #16
    stp x11, x12, [x1], #16
    sub x0, x0, #4
    b .loop4
.tail:
    cbz x0, .done
    ldr x5, [x2], #8
    ldr x9, [x1]
    mul x13, x5, x3
    umulh x17, x5, x3
    adds x9, x9, x13
    adc x17, x17, xzr
    adds x9, x9, x4
    adc x4, x17, xzr
    str x9, [x1], #8
    sub x0, x0, #1
    b .tail
.done:
    str x4, [x8]
    ret
