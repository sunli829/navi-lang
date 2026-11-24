// crypto/aes.navi's #[asm] functions for arm64 (the crypto extension).

// aes_encrypt_blocks(xk, rounds, src, dst, n): n blocks from src to dst
// with the round keys xk (16 bytes each, rounds + 1).
aes_encrypt_blocks:
    ldr x0, [sp]                // xk
    ldr x1, [sp, #8]            // rounds: 10, 12 or 14
    ldr x2, [sp, #16]           // src
    ldr x3, [sp, #24]           // dst
    ldr x4, [sp, #32]           // n
    cbz x4, .done
    // The round keys: k0-k8 in v16-v24, the rest in v25-v30.
    ld1 {v16.16b-v19.16b}, [x0], #64
    ld1 {v20.16b-v23.16b}, [x0], #64
    ld1 {v24.16b-v26.16b}, [x0], #48
    cmp x1, #10
    b.eq .loop
    ld1 {v27.16b, v28.16b}, [x0], #32
    cmp x1, #12
    b.eq .loop
    ld1 {v29.16b, v30.16b}, [x0], #32
.loop:
    ld1 {v0.16b}, [x2], #16
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
    b .store
.last12:
    aese v0.16b, v27.16b
    eor v0.16b, v0.16b, v28.16b
    b .store
.last10:
    aese v0.16b, v25.16b
    eor v0.16b, v0.16b, v26.16b
.store:
    st1 {v0.16b}, [x3], #16
    subs x4, x4, #1
    b.ne .loop
.done:
    ret
