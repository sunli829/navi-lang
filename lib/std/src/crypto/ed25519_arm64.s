// Ed25519's #[asm] functions for arm64 (crypto/ed25519.navi).

// lookup_affine(table, idx, neg): Ed25519's table[idx - 1] of 8 affine
// points (15 words each: y + x, y - x, 2dT), every entry read; idx 0 the
// identity (1, 1, 0). With neg 1, y + x and y - x swapped and 2dT negated
// (2p - 2dT: limbs below 2^52, not carried), by masks.
lookup_affine:
    ldr x0, [sp]
    ldr x1, [sp, #8]
    ldr x2, [sp, #16]
    movi v0.16b, #0
    movi v1.16b, #0
    movi v2.16b, #0
    movi v3.16b, #0
    movi v4.16b, #0
    movi v5.16b, #0
    movi v6.16b, #0
    movi v7.16b, #0
    mov x3, #1
.loop:
    cmp x3, x1
    csetm x4, eq
    dup v31.2d, x4
    ld1 {v16.16b-v19.16b}, [x0], #64
    ld1 {v20.16b, v21.16b, v22.16b}, [x0], #48
    ldr d23, [x0]
    add x0, x0, #8
    and v16.16b, v16.16b, v31.16b
    and v17.16b, v17.16b, v31.16b
    and v18.16b, v18.16b, v31.16b
    and v19.16b, v19.16b, v31.16b
    and v20.16b, v20.16b, v31.16b
    and v21.16b, v21.16b, v31.16b
    and v22.16b, v22.16b, v31.16b
    and v23.16b, v23.16b, v31.16b
    orr v0.16b, v0.16b, v16.16b
    orr v1.16b, v1.16b, v17.16b
    orr v2.16b, v2.16b, v18.16b
    orr v3.16b, v3.16b, v19.16b
    orr v4.16b, v4.16b, v20.16b
    orr v5.16b, v5.16b, v21.16b
    orr v6.16b, v6.16b, v22.16b
    orr v7.16b, v7.16b, v23.16b
    add x3, x3, #1
    cmp x3, #9
    b.ne .loop
    st1 {v0.16b-v3.16b}, [x8]
    add x9, x8, #64
    st1 {v4.16b, v5.16b, v6.16b}, [x9]
    str d7, [x8, #112]
    cmp x1, #0
    cset x3, eq
    ldr x4, [x8]
    orr x4, x4, x3
    str x4, [x8]
    ldr x4, [x8, #40]
    orr x4, x4, x3
    str x4, [x8, #40]
    neg x2, x2
    ldr x4, [x8, #0]
    ldr x5, [x8, #40]
    eor x6, x4, x5
    and x6, x6, x2
    eor x4, x4, x6
    eor x5, x5, x6
    str x4, [x8, #0]
    str x5, [x8, #40]
    ldr x4, [x8, #8]
    ldr x5, [x8, #48]
    eor x6, x4, x5
    and x6, x6, x2
    eor x4, x4, x6
    eor x5, x5, x6
    str x4, [x8, #8]
    str x5, [x8, #48]
    ldr x4, [x8, #16]
    ldr x5, [x8, #56]
    eor x6, x4, x5
    and x6, x6, x2
    eor x4, x4, x6
    eor x5, x5, x6
    str x4, [x8, #16]
    str x5, [x8, #56]
    ldr x4, [x8, #24]
    ldr x5, [x8, #64]
    eor x6, x4, x5
    and x6, x6, x2
    eor x4, x4, x6
    eor x5, x5, x6
    str x4, [x8, #24]
    str x5, [x8, #64]
    ldr x4, [x8, #32]
    ldr x5, [x8, #72]
    eor x6, x4, x5
    and x6, x6, x2
    eor x4, x4, x6
    eor x5, x5, x6
    str x4, [x8, #32]
    str x5, [x8, #72]
    ldr x4, [x8, #80]
    movz x5, #0xffda
    movk x5, #0xffff, lsl #16
    movk x5, #0xffff, lsl #32
    movk x5, #0xf, lsl #48
    sub x5, x5, x4
    eor x5, x5, x4
    and x5, x5, x2
    eor x4, x4, x5
    str x4, [x8, #80]
    ldr x4, [x8, #88]
    movz x5, #0xfffe
    movk x5, #0xffff, lsl #16
    movk x5, #0xffff, lsl #32
    movk x5, #0xf, lsl #48
    sub x5, x5, x4
    eor x5, x5, x4
    and x5, x5, x2
    eor x4, x4, x5
    str x4, [x8, #88]
    ldr x4, [x8, #96]
    movz x5, #0xfffe
    movk x5, #0xffff, lsl #16
    movk x5, #0xffff, lsl #32
    movk x5, #0xf, lsl #48
    sub x5, x5, x4
    eor x5, x5, x4
    and x5, x5, x2
    eor x4, x4, x5
    str x4, [x8, #96]
    ldr x4, [x8, #104]
    movz x5, #0xfffe
    movk x5, #0xffff, lsl #16
    movk x5, #0xffff, lsl #32
    movk x5, #0xf, lsl #48
    sub x5, x5, x4
    eor x5, x5, x4
    and x5, x5, x2
    eor x4, x4, x5
    str x4, [x8, #104]
    ldr x4, [x8, #112]
    movz x5, #0xfffe
    movk x5, #0xffff, lsl #16
    movk x5, #0xffff, lsl #32
    movk x5, #0xf, lsl #48
    sub x5, x5, x4
    eor x5, x5, x4
    and x5, x5, x2
    eor x4, x4, x5
    str x4, [x8, #112]
    ret
