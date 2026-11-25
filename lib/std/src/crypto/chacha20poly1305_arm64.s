// crypto/chacha20poly1305.navi's #[asm] functions for arm64: ChaCha20 on
// four blocks at a time in NEON, as Go's x/crypto/chacha20/chacha_arm64.s;
// Poly1305's blocks with the carry instructions.

// chacha20_blocks(state, dst, src, n): dst = src ^ the key stream of n
// 256-byte chunks. state: the 16 words of the block, each repeated in
// 4 lanes (one per block, the counter's lanes c, c+1, c+2, c+3), 256
// bytes; the counters advance by 4 per chunk (written back).
chacha20_blocks:
    ldr x0, [sp]
    ldr x1, [sp, #8]
    ldr x2, [sp, #16]
    ldr x3, [sp, #24]
    cbz x3, .done
    adr x6, .rot8
    mov w9, #4
.chunk:
    mov x4, x0
    ld1 {v0.4s, v1.4s, v2.4s, v3.4s}, [x4], #64
    ld1 {v4.4s, v5.4s, v6.4s, v7.4s}, [x4], #64
    ld1 {v8.4s, v9.4s, v10.4s, v11.4s}, [x4], #64
    ld1 {v12.4s, v13.4s, v14.4s, v15.4s}, [x4]
    ld1 {v30.16b}, [x6]
    mov x5, #10
.round:
    // V0..V3 += V4..V7
    // V12..V15 <<<= ((V12..V15 XOR V0..V3), 16)
    add v0.4s, v4.4s, v0.4s
    add v1.4s, v5.4s, v1.4s
    add v2.4s, v6.4s, v2.4s
    add v3.4s, v7.4s, v3.4s
    eor v12.16b, v0.16b, v12.16b
    eor v13.16b, v1.16b, v13.16b
    eor v14.16b, v2.16b, v14.16b
    eor v15.16b, v3.16b, v15.16b
    rev32 v12.8h, v12.8h
    rev32 v13.8h, v13.8h
    rev32 v14.8h, v14.8h
    rev32 v15.8h, v15.8h
    // V8..V11 += V12..V15
    // V4..V7 <<<= ((V4..V7 XOR V8..V11), 12)
    add v8.4s, v12.4s, v8.4s
    add v9.4s, v13.4s, v9.4s
    add v10.4s, v14.4s, v10.4s
    add v11.4s, v15.4s, v11.4s
    eor v16.16b, v4.16b, v8.16b
    eor v17.16b, v5.16b, v9.16b
    eor v18.16b, v6.16b, v10.16b
    eor v19.16b, v7.16b, v11.16b
    shl v4.4s, v16.4s, #12
    shl v5.4s, v17.4s, #12
    shl v6.4s, v18.4s, #12
    shl v7.4s, v19.4s, #12
    sri v4.4s, v16.4s, #20
    sri v5.4s, v17.4s, #20
    sri v6.4s, v18.4s, #20
    sri v7.4s, v19.4s, #20
    // V0..V3 += V4..V7
    // V12..V15 <<<= ((V12..V15 XOR V0..V3), 8)
    add v0.4s, v4.4s, v0.4s
    add v1.4s, v5.4s, v1.4s
    add v2.4s, v6.4s, v2.4s
    add v3.4s, v7.4s, v3.4s
    eor v12.16b, v0.16b, v12.16b
    eor v13.16b, v1.16b, v13.16b
    eor v14.16b, v2.16b, v14.16b
    eor v15.16b, v3.16b, v15.16b
    tbl v12.16b, {v12.16b}, v30.16b
    tbl v13.16b, {v13.16b}, v30.16b
    tbl v14.16b, {v14.16b}, v30.16b
    tbl v15.16b, {v15.16b}, v30.16b
    // V8..V11 += V12..V15
    // V4..V7 <<<= ((V4..V7 XOR V8..V11), 7)
    add v8.4s, v8.4s, v12.4s
    add v9.4s, v9.4s, v13.4s
    add v10.4s, v10.4s, v14.4s
    add v11.4s, v11.4s, v15.4s
    eor v16.16b, v4.16b, v8.16b
    eor v17.16b, v5.16b, v9.16b
    eor v18.16b, v6.16b, v10.16b
    eor v19.16b, v7.16b, v11.16b
    shl v4.4s, v16.4s, #7
    shl v5.4s, v17.4s, #7
    shl v6.4s, v18.4s, #7
    shl v7.4s, v19.4s, #7
    sri v4.4s, v16.4s, #25
    sri v5.4s, v17.4s, #25
    sri v6.4s, v18.4s, #25
    sri v7.4s, v19.4s, #25
    // V0..V3 += V5..V7, V4
    // V15,V12-V14 <<<= ((V15,V12-V14 XOR V0..V3), 16)
    add v0.4s, v5.4s, v0.4s
    add v1.4s, v6.4s, v1.4s
    add v2.4s, v7.4s, v2.4s
    add v3.4s, v4.4s, v3.4s
    eor v15.16b, v0.16b, v15.16b
    eor v12.16b, v1.16b, v12.16b
    eor v13.16b, v2.16b, v13.16b
    eor v14.16b, v3.16b, v14.16b
    rev32 v12.8h, v12.8h
    rev32 v13.8h, v13.8h
    rev32 v14.8h, v14.8h
    rev32 v15.8h, v15.8h
    // V10 += V15; V5 <<<= ((V10 XOR V5), 12)
    // ...
    add v10.4s, v10.4s, v15.4s
    add v11.4s, v11.4s, v12.4s
    add v8.4s, v8.4s, v13.4s
    add v9.4s, v9.4s, v14.4s
    eor v16.16b, v5.16b, v10.16b
    eor v17.16b, v6.16b, v11.16b
    eor v18.16b, v7.16b, v8.16b
    eor v19.16b, v4.16b, v9.16b
    shl v5.4s, v16.4s, #12
    shl v6.4s, v17.4s, #12
    shl v7.4s, v18.4s, #12
    shl v4.4s, v19.4s, #12
    sri v5.4s, v16.4s, #20
    sri v6.4s, v17.4s, #20
    sri v7.4s, v18.4s, #20
    sri v4.4s, v19.4s, #20
    // V0 += V5; V15 <<<= ((V0 XOR V15), 8)
    // ...
    add v0.4s, v0.4s, v5.4s
    add v1.4s, v1.4s, v6.4s
    add v2.4s, v2.4s, v7.4s
    add v3.4s, v3.4s, v4.4s
    eor v15.16b, v15.16b, v0.16b
    eor v12.16b, v12.16b, v1.16b
    eor v13.16b, v13.16b, v2.16b
    eor v14.16b, v14.16b, v3.16b
    tbl v12.16b, {v12.16b}, v30.16b
    tbl v13.16b, {v13.16b}, v30.16b
    tbl v14.16b, {v14.16b}, v30.16b
    tbl v15.16b, {v15.16b}, v30.16b
    // V10 += V15; V5 <<<= ((V10 XOR V5), 7)
    // ...
    add v10.4s, v10.4s, v15.4s
    add v11.4s, v11.4s, v12.4s
    add v8.4s, v8.4s, v13.4s
    add v9.4s, v9.4s, v14.4s
    eor v16.16b, v5.16b, v10.16b
    eor v17.16b, v6.16b, v11.16b
    eor v18.16b, v7.16b, v8.16b
    eor v19.16b, v4.16b, v9.16b
    shl v5.4s, v16.4s, #7
    shl v6.4s, v17.4s, #7
    shl v7.4s, v18.4s, #7
    shl v4.4s, v19.4s, #7
    sri v5.4s, v16.4s, #25
    sri v6.4s, v17.4s, #25
    sri v7.4s, v18.4s, #25
    sri v4.4s, v19.4s, #25
    subs x5, x5, #1
    b.ne .round
    // Plus the input, then transposed to the blocks' order
    mov x4, x0
    ld1 {v16.4s, v17.4s, v18.4s, v19.4s}, [x4], #64
    ld1 {v20.4s, v21.4s, v22.4s, v23.4s}, [x4], #64
    ld1 {v24.4s, v25.4s, v26.4s, v27.4s}, [x4], #64
    ld1 {v28.4s, v29.4s, v30.4s, v31.4s}, [x4]
    add v0.4s, v0.4s, v16.4s
    add v1.4s, v1.4s, v17.4s
    add v2.4s, v2.4s, v18.4s
    add v3.4s, v3.4s, v19.4s
    add v4.4s, v4.4s, v20.4s
    add v5.4s, v5.4s, v21.4s
    add v6.4s, v6.4s, v22.4s
    add v7.4s, v7.4s, v23.4s
    add v8.4s, v8.4s, v24.4s
    add v9.4s, v9.4s, v25.4s
    add v10.4s, v10.4s, v26.4s
    add v11.4s, v11.4s, v27.4s
    add v12.4s, v12.4s, v28.4s
    add v13.4s, v13.4s, v29.4s
    add v14.4s, v14.4s, v30.4s
    add v15.4s, v15.4s, v31.4s
    zip1 v16.4s, v0.4s, v1.4s
    zip2 v17.4s, v0.4s, v1.4s
    zip1 v18.4s, v2.4s, v3.4s
    zip2 v19.4s, v2.4s, v3.4s
    zip1 v20.4s, v4.4s, v5.4s
    zip2 v21.4s, v4.4s, v5.4s
    zip1 v22.4s, v6.4s, v7.4s
    zip2 v23.4s, v6.4s, v7.4s
    zip1 v24.4s, v8.4s, v9.4s
    zip2 v25.4s, v8.4s, v9.4s
    zip1 v26.4s, v10.4s, v11.4s
    zip2 v27.4s, v10.4s, v11.4s
    zip1 v28.4s, v12.4s, v13.4s
    zip2 v29.4s, v12.4s, v13.4s
    zip1 v30.4s, v14.4s, v15.4s
    zip2 v31.4s, v14.4s, v15.4s
    zip1 v0.2d, v16.2d, v18.2d
    zip2 v4.2d, v16.2d, v18.2d
    zip1 v8.2d, v17.2d, v19.2d
    zip2 v12.2d, v17.2d, v19.2d
    ld1 {v16.16b, v17.16b, v18.16b, v19.16b}, [x2], #64
    zip1 v1.2d, v20.2d, v22.2d
    zip2 v5.2d, v20.2d, v22.2d
    zip1 v9.2d, v21.2d, v23.2d
    zip2 v13.2d, v21.2d, v23.2d
    ld1 {v20.16b, v21.16b, v22.16b, v23.16b}, [x2], #64
    zip1 v2.2d, v24.2d, v26.2d
    zip2 v6.2d, v24.2d, v26.2d
    zip1 v10.2d, v25.2d, v27.2d
    zip2 v14.2d, v25.2d, v27.2d
    ld1 {v24.16b, v25.16b, v26.16b, v27.16b}, [x2], #64
    zip1 v3.2d, v28.2d, v30.2d
    zip2 v7.2d, v28.2d, v30.2d
    zip1 v11.2d, v29.2d, v31.2d
    zip2 v15.2d, v29.2d, v31.2d
    ld1 {v28.16b, v29.16b, v30.16b, v31.16b}, [x2], #64
    eor v16.16b, v16.16b, v0.16b
    eor v17.16b, v17.16b, v1.16b
    eor v18.16b, v18.16b, v2.16b
    eor v19.16b, v19.16b, v3.16b
    st1 {v16.16b, v17.16b, v18.16b, v19.16b}, [x1], #64
    eor v20.16b, v20.16b, v4.16b
    eor v21.16b, v21.16b, v5.16b
    eor v22.16b, v22.16b, v6.16b
    eor v23.16b, v23.16b, v7.16b
    st1 {v20.16b, v21.16b, v22.16b, v23.16b}, [x1], #64
    eor v24.16b, v24.16b, v8.16b
    eor v25.16b, v25.16b, v9.16b
    eor v26.16b, v26.16b, v10.16b
    eor v27.16b, v27.16b, v11.16b
    st1 {v24.16b, v25.16b, v26.16b, v27.16b}, [x1], #64
    eor v28.16b, v28.16b, v12.16b
    eor v29.16b, v29.16b, v13.16b
    eor v30.16b, v30.16b, v14.16b
    eor v31.16b, v31.16b, v15.16b
    st1 {v28.16b, v29.16b, v30.16b, v31.16b}, [x1], #64
    // The next 4 counters
    add x4, x0, #192
    ld1 {v16.4s}, [x4]
    dup v17.4s, w9
    add v16.4s, v16.4s, v17.4s
    st1 {v16.4s}, [x4]
    subs x3, x3, #1
    b.ne .chunk
.done:
    ret
.rot8:
    .word 0x02010003, 0x06050407, 0x0a09080b, 0x0e0d0c0f

// poly1305_blocks(h, r, m, n, hibit): h = (h + block + hibit 2^128) r,
// partly reduced mod 2^130 - 5, for the n 16-byte blocks at m (the
// algorithm of poly1305_generic in chacha20poly1305.navi, Go's).
poly1305_blocks:
    ldr x0, [sp]
    ldr x1, [sp, #8]
    ldr x2, [sp, #16]
    ldr x3, [sp, #24]
    ldr x4, [sp, #32]
    cbz x3, .pdone
    ldp x5, x6, [x0]
    ldr x7, [x0, #16]
    ldp x9, x10, [x1]
.ploop:
    // h += m (+ hibit at 2^128)
    ldp x11, x12, [x2], #16
    adds x5, x5, x11
    adcs x6, x6, x12
    adc x7, x7, x4
    // m1 = h1 r0 + h0 r1, m2 = h2 r0 + h1 r1, m3 = h2 r1, m0 = h0 r0
    mul x11, x6, x9
    umulh x12, x6, x9
    mul x13, x5, x10
    umulh x14, x5, x10
    adds x11, x11, x13
    adc x12, x12, x14
    mul x13, x7, x9
    mul x14, x6, x10
    umulh x15, x6, x10
    adds x13, x13, x14
    adc x14, x15, xzr
    mul x15, x7, x10
    mul x16, x5, x9
    umulh x17, x5, x9
    // t = (x16, x1, x8, x15)
    adds x1, x11, x17
    adcs x8, x13, x12
    adc x15, x15, x14
    // h = t mod 2^130, plus 5 times the rest: cc = t - h (4c), then cc >> 2
    and x7, x8, #3
    and x8, x8, #0xfffffffffffffffc
    adds x5, x16, x8
    adcs x6, x1, x15
    adc x7, x7, xzr
    lsr x8, x8, #2
    orr x8, x8, x15, lsl #62
    lsr x15, x15, #2
    adds x5, x5, x8
    adcs x6, x6, x15
    adc x7, x7, xzr
    subs x3, x3, #1
    b.ne .ploop
    stp x5, x6, [x0]
    str x7, [x0, #16]
.pdone:
    ret
