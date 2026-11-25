// crypto/nat.navi's #[asm] functions for arm64.

// mont_mul(t, a, b, m, n, m0inv, out): Go's montgomeryMul in one
// function (t, 2n + 1 words, zeroed here): for each limb b[i], t[i..] +=
// a * b[i] and then += m * (t[i] * m0inv), each z += x * y over n limbs
// as Go's addMulVVW (crypto/internal/fips140/bigmod/nat_arm64.s: four
// limbs at a time, the low halves and the high halves added by two carry
// chains), its carry and the last row's added into t[n + i] with the
// carry out in t[n + i + 1] (zero until then); then out = t[n..2n] (and
// the carry t[2n]) less m unless that borrows and there is no carry, by
// masks. The rows' state stays in the argument slots (t, b advanced; the
// caller does not read them back), the rows left in x8.
mont_mul:
    ldr x1, [sp]
    ldr x0, [sp, #32]
    lsl x2, x0, #1
    add x2, x2, #1
.z:
    str xzr, [x1], #8
    subs x2, x2, #1
    b.ne .z
    ldr x8, [sp, #32]
    cbz x8, .rdone
.row:
    ldr x1, [sp]                // t + 8i
    ldr x2, [sp, #8]            // a
    ldr x0, [sp, #32]           // n
    ldr x5, [sp, #16]
    ldr x3, [x5], #8            // b[i]
    str x5, [sp, #16]
    mov x4, #0
.a41:
    cmp x0, #4
    b.lo .at1
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
    b .a41
.at1:
    cbz x0, .ad1
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
    b .at1
.ad1:
    ldr x5, [x1]                // t[n + i] += c1, the carry above
    adds x5, x5, x4
    cset x6, hs
    stp x5, x6, [x1]
    ldr x1, [sp]
    ldr x3, [x1]
    ldr x5, [sp, #40]
    mul x3, x3, x5              // y = t[i] * m0inv
    ldr x2, [sp, #24]           // m
    ldr x0, [sp, #32]
    mov x4, #0
.a42:
    cmp x0, #4
    b.lo .at2
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
    b .a42
.at2:
    cbz x0, .ad2
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
    b .at2
.ad2:
    ldp x5, x6, [x1]            // t[n + i] += c2
    adds x5, x5, x4
    adc x6, x6, xzr
    stp x5, x6, [x1]
    ldr x1, [sp]
    add x1, x1, #8
    str x1, [sp]
    subs x8, x8, #1
    b.ne .row
    ldr x1, [sp]                // t + 8n
    ldr x2, [sp, #24]           // m
    ldr x4, [sp, #48]           // out
    ldr x0, [sp, #32]           // n
    mov x5, x1
    mov x6, x4
    mov x7, x0
    cmp x7, x7                  // no borrow
.sb:
    ldr x9, [x5], #8
    ldr x10, [x2], #8
    sbcs x9, x9, x10
    str x9, [x6], #8
    sub x7, x7, #1
    cbnz x7, .sb
    cset x11, lo                // the borrow
    ldr x12, [x5]               // the carry t[2n]
    eor x12, x12, #1
    and x11, x11, x12
    neg x11, x11                // all ones: t, below m
.ks:
    ldr x9, [x1], #8
    ldr x10, [x4]
    eor x9, x9, x10
    and x9, x9, x11
    eor x10, x10, x9
    str x10, [x4], #8
    sub x0, x0, #1
    cbnz x0, .ks
.rdone:
    ret

// ct_select(table, n, count, idx, out): out = the idx-th of count entries
// of n limbs, every entry read (masked).
ct_select:
    ldr x0, [sp]
    ldr x1, [sp, #8]
    ldr x2, [sp, #16]
    ldr x3, [sp, #24]
    ldr x4, [sp, #32]
    mov x5, x4
    mov x6, x1
.cz:
    str xzr, [x5], #8
    sub x6, x6, #1
    cbnz x6, .cz
    mov x7, #0
.ce:
    cmp x7, x3
    csetm x9, eq
    mov x5, x4
    mov x6, x1
.c2:
    cmp x6, #2
    b.lo .c1
    ldp x10, x11, [x0], #16
    ldp x12, x13, [x5]
    and x10, x10, x9
    and x11, x11, x9
    orr x12, x12, x10
    orr x13, x13, x11
    stp x12, x13, [x5], #16
    sub x6, x6, #2
    b .c2
.c1:
    cbz x6, .cn
    ldr x10, [x0], #8
    ldr x12, [x5]
    and x10, x10, x9
    orr x12, x12, x10
    str x12, [x5]
.cn:
    add x7, x7, #1
    cmp x7, x2
    b.ne .ce
    ret
