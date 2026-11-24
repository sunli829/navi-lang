// crypto/sha2.navi's #[asm] functions for arm64 (the SHA2 instructions).

// sha256_blocks(h, data, n, k): the n 64-byte blocks of data into the
// state h[0..8] (k: the 64 round constants). Each group of 4 rounds adds
// its constants to 4 message words, runs sha256h/sha256h2 and extends
// the schedule with sha256su0/sha256su1 (ARM's sequence).
sha256_blocks:
    ldr x0, [sp]                // h
    ldr x1, [sp, #8]            // data
    ldr x2, [sp, #16]           // n
    ldr x3, [sp, #24]           // k
    cbz x2, .done
    ld1 {v0.4s, v1.4s}, [x0]    // abcd, efgh
    ld1 {v16.4s, v17.4s, v18.4s, v19.4s}, [x3], #64
    ld1 {v20.4s, v21.4s, v22.4s, v23.4s}, [x3], #64
    ld1 {v24.4s, v25.4s, v26.4s, v27.4s}, [x3], #64
    ld1 {v28.4s, v29.4s, v30.4s, v31.4s}, [x3]
.loop:
    ld1 {v4.16b, v5.16b, v6.16b, v7.16b}, [x1], #64
    rev32 v4.16b, v4.16b
    rev32 v5.16b, v5.16b
    rev32 v6.16b, v6.16b
    rev32 v7.16b, v7.16b
    mov v2.16b, v0.16b
    mov v3.16b, v1.16b
    add v8.4s, v4.4s, v16.4s
    sha256su0 v4.4s, v5.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v4.4s, v6.4s, v7.4s
    add v8.4s, v5.4s, v17.4s
    sha256su0 v5.4s, v6.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v5.4s, v7.4s, v4.4s
    add v8.4s, v6.4s, v18.4s
    sha256su0 v6.4s, v7.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v6.4s, v4.4s, v5.4s
    add v8.4s, v7.4s, v19.4s
    sha256su0 v7.4s, v4.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v7.4s, v5.4s, v6.4s
    add v8.4s, v4.4s, v20.4s
    sha256su0 v4.4s, v5.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v4.4s, v6.4s, v7.4s
    add v8.4s, v5.4s, v21.4s
    sha256su0 v5.4s, v6.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v5.4s, v7.4s, v4.4s
    add v8.4s, v6.4s, v22.4s
    sha256su0 v6.4s, v7.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v6.4s, v4.4s, v5.4s
    add v8.4s, v7.4s, v23.4s
    sha256su0 v7.4s, v4.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v7.4s, v5.4s, v6.4s
    add v8.4s, v4.4s, v24.4s
    sha256su0 v4.4s, v5.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v4.4s, v6.4s, v7.4s
    add v8.4s, v5.4s, v25.4s
    sha256su0 v5.4s, v6.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v5.4s, v7.4s, v4.4s
    add v8.4s, v6.4s, v26.4s
    sha256su0 v6.4s, v7.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v6.4s, v4.4s, v5.4s
    add v8.4s, v7.4s, v27.4s
    sha256su0 v7.4s, v4.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    sha256su1 v7.4s, v5.4s, v6.4s
    add v8.4s, v4.4s, v28.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    add v8.4s, v5.4s, v29.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    add v8.4s, v6.4s, v30.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    add v8.4s, v7.4s, v31.4s
    mov v9.16b, v0.16b
    sha256h q0, q1, v8.4s
    sha256h2 q1, q9, v8.4s
    add v0.4s, v0.4s, v2.4s
    add v1.4s, v1.4s, v3.4s
    subs x2, x2, #1
    b.ne .loop
    st1 {v0.4s, v1.4s}, [x0]
.done:
    ret
