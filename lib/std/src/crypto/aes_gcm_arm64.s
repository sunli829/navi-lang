// crypto/aes_gcm.navi's #[asm] functions for arm64 (AES and PMULL), as
// Go's gcm_arm64.s: eight blocks at a time.

// ghash_blocks(hp, y, data, n): y = (y ^ block) * H in GF(2^128), for each
// of the n 16-byte blocks; hp: H, H^2, ..., H^8 (16 bytes each). rbit
// makes GCM's reflected bits an ordinary polynomial (bit i of the
// little-endian vector is the coefficient of x^i). Eight blocks at a time:
// (y ^ x0) H^8 ^ x1 H^7 ^ ... ^ x7 H, Karatsuba products summed, then
// reduced once (two folds by x^128 = 0x87).
ghash_blocks:
    ldr x0, [sp]                // hp
    ldr x1, [sp, #8]            // y
    ldr x2, [sp, #16]           // data
    ldr x3, [sp, #24]           // n
    cbz x3, .done
    // H^k in v15+k, its halves xored (Karatsuba) in v23+k
    ld1 {v16.16b, v17.16b, v18.16b, v19.16b}, [x0], #64
    ld1 {v20.16b, v21.16b, v22.16b, v23.16b}, [x0]
    rbit v16.16b, v16.16b
    ext v24.16b, v16.16b, v16.16b, #8
    eor v24.16b, v24.16b, v16.16b
    rbit v17.16b, v17.16b
    ext v25.16b, v17.16b, v17.16b, #8
    eor v25.16b, v25.16b, v17.16b
    rbit v18.16b, v18.16b
    ext v26.16b, v18.16b, v18.16b, #8
    eor v26.16b, v26.16b, v18.16b
    rbit v19.16b, v19.16b
    ext v27.16b, v19.16b, v19.16b, #8
    eor v27.16b, v27.16b, v19.16b
    rbit v20.16b, v20.16b
    ext v28.16b, v20.16b, v20.16b, #8
    eor v28.16b, v28.16b, v20.16b
    rbit v21.16b, v21.16b
    ext v29.16b, v21.16b, v21.16b, #8
    eor v29.16b, v29.16b, v21.16b
    rbit v22.16b, v22.16b
    ext v30.16b, v22.16b, v22.16b, #8
    eor v30.16b, v30.16b, v22.16b
    rbit v23.16b, v23.16b
    ext v31.16b, v23.16b, v23.16b, #8
    eor v31.16b, v31.16b, v23.16b
    movi v7.16b, #0
    mov x4, #0x87
    dup v8.2d, x4
    ld1 {v0.16b}, [x1]
    rbit v0.16b, v0.16b         // Y
.loop8:
    cmp x3, #8
    b.lo .loop1
    ld1 {v9.16b}, [x2], #16
    rbit v9.16b, v9.16b
    eor v9.16b, v9.16b, v0.16b
    pmull v1.1q, v9.1d, v23.1d
    pmull2 v2.1q, v9.2d, v23.2d
    ext v4.16b, v9.16b, v9.16b, #8
    eor v4.16b, v4.16b, v9.16b
    pmull v3.1q, v4.1d, v31.1d
    ld1 {v9.16b}, [x2], #16
    rbit v9.16b, v9.16b
    pmull v4.1q, v9.1d, v22.1d
    eor v1.16b, v1.16b, v4.16b
    pmull2 v4.1q, v9.2d, v22.2d
    eor v2.16b, v2.16b, v4.16b
    ext v4.16b, v9.16b, v9.16b, #8
    eor v4.16b, v4.16b, v9.16b
    pmull v4.1q, v4.1d, v30.1d
    eor v3.16b, v3.16b, v4.16b
    ld1 {v9.16b}, [x2], #16
    rbit v9.16b, v9.16b
    pmull v4.1q, v9.1d, v21.1d
    eor v1.16b, v1.16b, v4.16b
    pmull2 v4.1q, v9.2d, v21.2d
    eor v2.16b, v2.16b, v4.16b
    ext v4.16b, v9.16b, v9.16b, #8
    eor v4.16b, v4.16b, v9.16b
    pmull v4.1q, v4.1d, v29.1d
    eor v3.16b, v3.16b, v4.16b
    ld1 {v9.16b}, [x2], #16
    rbit v9.16b, v9.16b
    pmull v4.1q, v9.1d, v20.1d
    eor v1.16b, v1.16b, v4.16b
    pmull2 v4.1q, v9.2d, v20.2d
    eor v2.16b, v2.16b, v4.16b
    ext v4.16b, v9.16b, v9.16b, #8
    eor v4.16b, v4.16b, v9.16b
    pmull v4.1q, v4.1d, v28.1d
    eor v3.16b, v3.16b, v4.16b
    ld1 {v9.16b}, [x2], #16
    rbit v9.16b, v9.16b
    pmull v4.1q, v9.1d, v19.1d
    eor v1.16b, v1.16b, v4.16b
    pmull2 v4.1q, v9.2d, v19.2d
    eor v2.16b, v2.16b, v4.16b
    ext v4.16b, v9.16b, v9.16b, #8
    eor v4.16b, v4.16b, v9.16b
    pmull v4.1q, v4.1d, v27.1d
    eor v3.16b, v3.16b, v4.16b
    ld1 {v9.16b}, [x2], #16
    rbit v9.16b, v9.16b
    pmull v4.1q, v9.1d, v18.1d
    eor v1.16b, v1.16b, v4.16b
    pmull2 v4.1q, v9.2d, v18.2d
    eor v2.16b, v2.16b, v4.16b
    ext v4.16b, v9.16b, v9.16b, #8
    eor v4.16b, v4.16b, v9.16b
    pmull v4.1q, v4.1d, v26.1d
    eor v3.16b, v3.16b, v4.16b
    ld1 {v9.16b}, [x2], #16
    rbit v9.16b, v9.16b
    pmull v4.1q, v9.1d, v17.1d
    eor v1.16b, v1.16b, v4.16b
    pmull2 v4.1q, v9.2d, v17.2d
    eor v2.16b, v2.16b, v4.16b
    ext v4.16b, v9.16b, v9.16b, #8
    eor v4.16b, v4.16b, v9.16b
    pmull v4.1q, v4.1d, v25.1d
    eor v3.16b, v3.16b, v4.16b
    ld1 {v9.16b}, [x2], #16
    rbit v9.16b, v9.16b
    pmull v4.1q, v9.1d, v16.1d
    eor v1.16b, v1.16b, v4.16b
    pmull2 v4.1q, v9.2d, v16.2d
    eor v2.16b, v2.16b, v4.16b
    ext v4.16b, v9.16b, v9.16b, #8
    eor v4.16b, v4.16b, v9.16b
    pmull v4.1q, v4.1d, v24.1d
    eor v3.16b, v3.16b, v4.16b
    eor v3.16b, v3.16b, v1.16b
    eor v3.16b, v3.16b, v2.16b          // mid
    ext v5.16b, v7.16b, v3.16b, #8
    eor v1.16b, v1.16b, v5.16b          // lo ^= [0, m0]
    ext v5.16b, v3.16b, v7.16b, #8
    eor v2.16b, v2.16b, v5.16b          // hi ^= [m1, 0]
    // [hi:lo] = P3:P2:P1:P0 mod x^128 + x^7 + x^2 + x + 1.
    pmull2 v5.1q, v2.2d, v8.2d          // P3 * 0x87, at x^64
    ext v6.16b, v7.16b, v5.16b, #8
    eor v1.16b, v1.16b, v6.16b
    ext v6.16b, v5.16b, v7.16b, #8
    eor v2.16b, v2.16b, v6.16b
    pmull v5.1q, v2.1d, v8.1d           // P2 * 0x87
    eor v0.16b, v1.16b, v5.16b
    sub x3, x3, #8
    b .loop8
.loop1:
    cbz x3, .end
    ld1 {v9.16b}, [x2], #16
    rbit v9.16b, v9.16b
    eor v9.16b, v9.16b, v0.16b          // a = Y ^ X
    pmull v1.1q, v9.1d, v16.1d          // lo = a0 h0
    pmull2 v2.1q, v9.2d, v16.2d         // hi = a1 h1
    ext v4.16b, v9.16b, v9.16b, #8
    eor v4.16b, v4.16b, v9.16b
    pmull v3.1q, v4.1d, v24.1d          // (a0 ^ a1)(h0 ^ h1)
    eor v3.16b, v3.16b, v1.16b
    eor v3.16b, v3.16b, v2.16b          // mid
    ext v5.16b, v7.16b, v3.16b, #8
    eor v1.16b, v1.16b, v5.16b          // lo ^= [0, m0]
    ext v5.16b, v3.16b, v7.16b, #8
    eor v2.16b, v2.16b, v5.16b          // hi ^= [m1, 0]
    // [hi:lo] = P3:P2:P1:P0 mod x^128 + x^7 + x^2 + x + 1.
    pmull2 v5.1q, v2.2d, v8.2d          // P3 * 0x87, at x^64
    ext v6.16b, v7.16b, v5.16b, #8
    eor v1.16b, v1.16b, v6.16b
    ext v6.16b, v5.16b, v7.16b, #8
    eor v2.16b, v2.16b, v6.16b
    pmull v5.1q, v2.1d, v8.1d           // P2 * 0x87
    eor v0.16b, v1.16b, v5.16b
    sub x3, x3, #1
    b .loop1
.end:
    rbit v0.16b, v0.16b
    st1 {v0.16b}, [x1]
.done:
    ret

// aes_ctr_xor(xk, rounds, ctr, src, dst, n): dst = src ^ E(ctr) for n
// blocks, the counter (the big-endian last word of the 16 bytes at ctr)
// incremented each block and written back. Eight blocks at a time through
// the rounds (the AES unit's latency hidden), then one at a time.
aes_ctr_xor:
    ldr x0, [sp]                // xk
    ldr x1, [sp, #8]            // rounds
    ldr x5, [sp, #16]           // ctr
    ldr x2, [sp, #24]           // src
    ldr x3, [sp, #32]           // dst
    ldr x4, [sp, #40]           // n
    cbz x4, .cdone
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
.c8:
    cmp x4, #8
    b.lo .c1
    mov v0.16b, v31.16b
    mov v1.16b, v31.16b
    add w7, w6, #1
    rev w7, w7
    mov v1.s[3], w7
    mov v2.16b, v31.16b
    add w7, w6, #2
    rev w7, w7
    mov v2.s[3], w7
    mov v3.16b, v31.16b
    add w7, w6, #3
    rev w7, w7
    mov v3.s[3], w7
    mov v4.16b, v31.16b
    add w7, w6, #4
    rev w7, w7
    mov v4.s[3], w7
    mov v5.16b, v31.16b
    add w7, w6, #5
    rev w7, w7
    mov v5.s[3], w7
    mov v6.16b, v31.16b
    add w7, w6, #6
    rev w7, w7
    mov v6.s[3], w7
    mov v7.16b, v31.16b
    add w7, w6, #7
    rev w7, w7
    mov v7.s[3], w7
    add w6, w6, #8
    aese v0.16b, v16.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v16.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v16.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v16.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v16.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v16.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v16.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v16.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v17.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v17.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v17.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v17.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v17.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v17.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v17.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v17.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v18.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v18.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v18.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v18.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v18.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v18.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v18.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v18.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v19.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v19.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v19.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v19.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v19.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v19.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v19.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v19.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v20.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v20.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v20.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v20.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v20.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v20.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v20.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v20.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v21.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v21.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v21.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v21.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v21.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v21.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v21.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v21.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v22.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v22.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v22.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v22.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v22.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v22.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v22.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v22.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v23.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v23.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v23.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v23.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v23.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v23.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v23.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v23.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v24.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v24.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v24.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v24.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v24.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v24.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v24.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v24.16b
    aesmc v7.16b, v7.16b
    cmp x1, #10
    b.eq .c8last10
    aese v0.16b, v25.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v25.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v25.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v25.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v25.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v25.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v25.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v25.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v26.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v26.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v26.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v26.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v26.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v26.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v26.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v26.16b
    aesmc v7.16b, v7.16b
    cmp x1, #12
    b.eq .c8last12
    aese v0.16b, v27.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v27.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v27.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v27.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v27.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v27.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v27.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v27.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v28.16b
    aesmc v0.16b, v0.16b
    aese v1.16b, v28.16b
    aesmc v1.16b, v1.16b
    aese v2.16b, v28.16b
    aesmc v2.16b, v2.16b
    aese v3.16b, v28.16b
    aesmc v3.16b, v3.16b
    aese v4.16b, v28.16b
    aesmc v4.16b, v4.16b
    aese v5.16b, v28.16b
    aesmc v5.16b, v5.16b
    aese v6.16b, v28.16b
    aesmc v6.16b, v6.16b
    aese v7.16b, v28.16b
    aesmc v7.16b, v7.16b
    aese v0.16b, v29.16b
    aese v1.16b, v29.16b
    aese v2.16b, v29.16b
    aese v3.16b, v29.16b
    aese v4.16b, v29.16b
    aese v5.16b, v29.16b
    aese v6.16b, v29.16b
    aese v7.16b, v29.16b
    eor v0.16b, v0.16b, v30.16b
    eor v1.16b, v1.16b, v30.16b
    eor v2.16b, v2.16b, v30.16b
    eor v3.16b, v3.16b, v30.16b
    eor v4.16b, v4.16b, v30.16b
    eor v5.16b, v5.16b, v30.16b
    eor v6.16b, v6.16b, v30.16b
    eor v7.16b, v7.16b, v30.16b
    b .c8xor
.c8last12:
    aese v0.16b, v27.16b
    aese v1.16b, v27.16b
    aese v2.16b, v27.16b
    aese v3.16b, v27.16b
    aese v4.16b, v27.16b
    aese v5.16b, v27.16b
    aese v6.16b, v27.16b
    aese v7.16b, v27.16b
    eor v0.16b, v0.16b, v28.16b
    eor v1.16b, v1.16b, v28.16b
    eor v2.16b, v2.16b, v28.16b
    eor v3.16b, v3.16b, v28.16b
    eor v4.16b, v4.16b, v28.16b
    eor v5.16b, v5.16b, v28.16b
    eor v6.16b, v6.16b, v28.16b
    eor v7.16b, v7.16b, v28.16b
    b .c8xor
.c8last10:
    aese v0.16b, v25.16b
    aese v1.16b, v25.16b
    aese v2.16b, v25.16b
    aese v3.16b, v25.16b
    aese v4.16b, v25.16b
    aese v5.16b, v25.16b
    aese v6.16b, v25.16b
    aese v7.16b, v25.16b
    eor v0.16b, v0.16b, v26.16b
    eor v1.16b, v1.16b, v26.16b
    eor v2.16b, v2.16b, v26.16b
    eor v3.16b, v3.16b, v26.16b
    eor v4.16b, v4.16b, v26.16b
    eor v5.16b, v5.16b, v26.16b
    eor v6.16b, v6.16b, v26.16b
    eor v7.16b, v7.16b, v26.16b
.c8xor:
    ld1 {v8.16b-v11.16b}, [x2], #64
    ld1 {v12.16b-v15.16b}, [x2], #64
    eor v0.16b, v0.16b, v8.16b
    eor v1.16b, v1.16b, v9.16b
    eor v2.16b, v2.16b, v10.16b
    eor v3.16b, v3.16b, v11.16b
    eor v4.16b, v4.16b, v12.16b
    eor v5.16b, v5.16b, v13.16b
    eor v6.16b, v6.16b, v14.16b
    eor v7.16b, v7.16b, v15.16b
    st1 {v0.16b-v3.16b}, [x3], #64
    st1 {v4.16b-v7.16b}, [x3], #64
    rev w7, w6
    mov v31.s[3], w7
    sub x4, x4, #8
    b .c8
.c1:
    cbz x4, .cend
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
    b.eq .c1last10
    aese v0.16b, v25.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v26.16b
    aesmc v0.16b, v0.16b
    cmp x1, #12
    b.eq .c1last12
    aese v0.16b, v27.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v28.16b
    aesmc v0.16b, v0.16b
    aese v0.16b, v29.16b
    eor v0.16b, v0.16b, v30.16b
    b .c1xor
.c1last12:
    aese v0.16b, v27.16b
    eor v0.16b, v0.16b, v28.16b
    b .c1xor
.c1last10:
    aese v0.16b, v25.16b
    eor v0.16b, v0.16b, v26.16b
.c1xor:
    ld1 {v1.16b}, [x2], #16
    eor v0.16b, v0.16b, v1.16b
    st1 {v0.16b}, [x3], #16
    add w6, w6, #1
    rev w7, w6
    mov v31.s[3], w7
    subs x4, x4, #1
    b.ne .c1

.cend:
    st1 {v31.16b}, [x5]
.cdone:
    ret
