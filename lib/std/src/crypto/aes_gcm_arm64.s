// crypto/aes_gcm.navi's #[asm] functions for arm64 (AES and PMULL).

// ghash_blocks(h, y, data, n): y = (y ^ block) * H in GF(2^128), for each
// of the n 16-byte blocks. rbit makes GCM's reflected bits an ordinary
// polynomial (bit i of the little-endian vector is the coefficient of
// x^i); a Karatsuba product with pmull, then two folds by x^128 = 0x87.
ghash_blocks:
    ldr x0, [sp]                // h
    ldr x1, [sp, #8]            // y
    ldr x2, [sp, #16]           // data
    ldr x3, [sp, #24]           // n
    cbz x3, .done
    ld1 {v20.16b}, [x0]
    rbit v20.16b, v20.16b       // H
    ext v21.16b, v20.16b, v20.16b, #8
    eor v21.16b, v21.16b, v20.16b       // d[0]: h0 ^ h1
    movi v22.16b, #0
    mov x4, #0x87
    dup v23.2d, x4
    ld1 {v0.16b}, [x1]
    rbit v0.16b, v0.16b         // Y
.loop:
    ld1 {v1.16b}, [x2], #16
    rbit v1.16b, v1.16b
    eor v0.16b, v0.16b, v1.16b          // a = Y ^ X
    pmull v2.1q, v0.1d, v20.1d          // lo = a0 h0
    pmull2 v3.1q, v0.2d, v20.2d         // hi = a1 h1
    ext v4.16b, v0.16b, v0.16b, #8
    eor v4.16b, v4.16b, v0.16b
    pmull v4.1q, v4.1d, v21.1d          // (a0 ^ a1)(h0 ^ h1)
    eor v4.16b, v4.16b, v2.16b
    eor v4.16b, v4.16b, v3.16b          // mid
    ext v5.16b, v22.16b, v4.16b, #8
    eor v2.16b, v2.16b, v5.16b          // lo ^= [0, m0]
    ext v5.16b, v4.16b, v22.16b, #8
    eor v3.16b, v3.16b, v5.16b          // hi ^= [m1, 0]
    // [hi:lo] = P3:P2:P1:P0 mod x^128 + x^7 + x^2 + x + 1.
    pmull2 v5.1q, v3.2d, v23.2d         // P3 * 0x87, at x^64
    ext v6.16b, v22.16b, v5.16b, #8
    eor v2.16b, v2.16b, v6.16b
    ext v6.16b, v5.16b, v22.16b, #8
    eor v3.16b, v3.16b, v6.16b
    pmull v5.1q, v3.1d, v23.1d          // P2 * 0x87
    eor v0.16b, v2.16b, v5.16b
    subs x3, x3, #1
    b.ne .loop
    rbit v0.16b, v0.16b
    st1 {v0.16b}, [x1]
.done:
    ret

// aes_ctr_xor(xk, rounds, ctr, src, dst, n): dst = src ^ E(ctr) for n
// blocks, the counter (the big-endian last word of the 16 bytes at ctr)
// incremented each block and written back.
aes_ctr_xor:
    ldr x0, [sp]                // xk
    ldr x1, [sp, #8]            // rounds
    ldr x5, [sp, #16]           // ctr
    ldr x2, [sp, #24]           // src
    ldr x3, [sp, #32]           // dst
    ldr x4, [sp, #40]           // n
    cbz x4, .done
    ld1 {v16.16b-v19.16b}, [x0], #64
    ld1 {v20.16b-v23.16b}, [x0], #64
    ld1 {v24.16b-v26.16b}, [x0], #48
    cmp x1, #10
    b.eq .keys
    ld1 {v27.16b, v28.16b}, [x0], #32
    cmp x1, #12
    b.eq .keys
    ld1 {v29.16b, v30.16b}, [x0], #32
.keys:
    ld1 {v31.16b}, [x5]
    ldr w6, [x5, #12]
    rev w6, w6
.loop:
    mov v0.16b, v31.16b
    aese v0.16b, v16.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v17.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v18.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v19.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v20.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v21.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v22.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v23.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v24.16b
    aesmc v0.16b, v0.16b
    cmp x1, #10
    b.eq .last10
    aese v0.16b, v25.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v26.16b
    aesmc v0.16b, v0.16b
    cmp x1, #12
    b.eq .last12
    aese v0.16b, v27.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v28.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v29.16b
    eor v0.16b, v0.16b, v30.16b
    b .xor
.last12:
    aese v0.16b, v27.16b
    eor v0.16b, v0.16b, v28.16b
    b .xor
.last10:
    aese v0.16b, v25.16b
    eor v0.16b, v0.16b, v26.16b
.xor:
    ld1 {v1.16b}, [x2], #16
    eor v0.16b, v0.16b, v1.16b
    st1 {v0.16b}, [x3], #16
    add w6, w6, #1
    rev w7, w6
    mov v31.s[3], w7
    subs x4, x4, #1
    b.ne .loop
    st1 {v31.16b}, [x5]
.done:
    ret
