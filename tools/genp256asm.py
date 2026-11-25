#!/usr/bin/env python3
# Generates lib/std/src/crypto/nistec_p256_arm64.s and _amd64.s: P-256's
# field arithmetic in assembly, as Go's p256_asm_*.s, the limbs and the
# carries in registers. Montgomery multiplication (a * b / 2^256 mod p) is
# reduced word by word: P-256's -p^-1 mod 2^64 is 1, so each word's
# quotient m is the word itself, and m * p + m is m << 96 plus
# m * 0xffffffff00000001 << 192.
#
#   tools/genp256asm.py         (writes the files)
#   tools/genp256asm.py check   (runs each function on a small simulator
#                                against Python's integers: edge cases and
#                                random)

import os, random, sys
P = 2**256 - 2**224 + 2**192 + 2**96 - 1
R = 2**256
M64 = 2**64 - 1


def load(e, ptr, regs):
    e(f'ldp {regs[0]}, {regs[1]}, [{ptr}]')
    e(f'ldp {regs[2]}, {regs[3]}, [{ptr}, #16]')


def store(e, regs):
    e(f'stp {regs[0]}, {regs[1]}, [x8]')
    e(f'stp {regs[2]}, {regs[3]}, [x8, #16]')
    e('ret')


# Stores s[0..4] (with the carry word s[4], s < 2p) less p unless that
# borrows; c1 holds 0xffffffff00000001, t is free; r: 4 free registers.
def reduce_store(e, s, r, c1, t):
    e(f'mov {t}, #0xffffffff')
    e(f'adds {r[0]}, {s[0]}, #1')
    e(f'sbcs {r[1]}, {s[1]}, {t}')
    e(f'sbcs {r[2]}, {s[2]}, xzr')
    e(f'sbcs {r[3]}, {s[3]}, {c1}')
    e(f'sbcs xzr, {s[4]}, xzr')
    for k in range(4):
        e(f'csel {r[k]}, {s[k]}, {r[k]}, lo')
    store(e, r)


# One word of reduction: w[0..4] += m * p with m = w[0], leaving w[0] zero;
# the next window is w[1..4] plus m's register (which ends up holding the
# new top word unless top is given: then it is added into top).
def reduce_word(e, w, c1, t, u, top=None):
    m = w[0]
    e(f'adds {w[1]}, {w[1]}, {m}, lsl #32')
    e(f'lsr {t}, {m}, #32')
    e(f'mul {u}, {m}, {c1}')
    e(f'umulh {m}, {m}, {c1}')
    e(f'adcs {w[2]}, {w[2]}, {t}')
    e(f'adcs {w[3]}, {w[3]}, {u}')
    if top is None:
        e(f'adc {m}, {m}, xzr')
    else:
        e(f'adcs {w[4]}, {w[4]}, {m}')
        e(f'adc {top[1]}, {top[0]}, xzr')


def mul_body():
    o = []
    e = o.append
    a = ['x2', 'x3', 'x4', 'x5']
    b, c1, t, u = 'x6', 'x7', 'x15', 'x16'
    s = ['x9', 'x10', 'x11', 'x12', 'x13']
    free = 'x14'
    e('ldr x0, [sp]')
    e('ldr x1, [sp, #8]')
    load(e, 'x0', a)
    e(f'mov {c1}, #0xffffffff00000001')
    for i in range(4):
        e(f'ldr {b}, [x1, #{8*i}]' if i else f'ldr {b}, [x1]')
        s5 = free
        if i == 0:
            # T = a * b0
            for k in range(4):
                e(f'mul {s[k]}, {a[k]}, {b}')
            e(f'umulh {t}, {a[0]}, {b}')
            e(f'adds {s[1]}, {s[1]}, {t}')
            e(f'umulh {t}, {a[1]}, {b}')
            e(f'adcs {s[2]}, {s[2]}, {t}')
            e(f'umulh {t}, {a[2]}, {b}')
            e(f'adcs {s[3]}, {s[3]}, {t}')
            e(f'umulh {s[4]}, {a[3]}, {b}')
            e(f'adc {s[4]}, {s[4]}, xzr')
        else:
            # T += a * bi: the low halves, then the high halves a word up
            e(f'mul {t}, {a[0]}, {b}')
            e(f'adds {s[0]}, {s[0]}, {t}')
            for k in range(1, 4):
                e(f'mul {t}, {a[k]}, {b}')
                e(f'adcs {s[k]}, {s[k]}, {t}')
            e(f'adcs {s[4]}, {s[4]}, xzr')
            e(f'adc {s5}, xzr, xzr')
            e(f'umulh {t}, {a[0]}, {b}')
            e(f'adds {s[1]}, {s[1]}, {t}')
            for k in range(1, 4):
                e(f'umulh {t}, {a[k]}, {b}')
                e(f'adcs {s[k+1]}, {s[k+1]}, {t}')
            e(f'adc {s5}, {s5}, xzr')
        reduce_word(e, s, c1, t, u, top=(s5 if i else 'xzr', s5))
        free = s[0]
        s = s[1:] + [s5]
    reduce_store(e, s, a, c1, t)
    return o


def sqr_body():
    o = []
    e = o.append
    a = ['x2', 'x3', 'x4', 'x5']
    c1 = 'x7'
    t = ['x9', 'x10', 'x11', 'x12', 'x13', 'x14', 'x15', 'x16']
    x, y, z = 'x0', 'x1', 'x6'
    e('ldr x0, [sp]')
    load(e, 'x0', a)
    e(f'mov {c1}, #0xffffffff00000001')
    # The products a[i] * a[j], i < j, at t[1..7)
    e(f'mul {t[1]}, {a[0]}, {a[1]}')
    e(f'umulh {t[2]}, {a[0]}, {a[1]}')
    e(f'mul {x}, {a[0]}, {a[2]}')
    e(f'adds {t[2]}, {t[2]}, {x}')
    e(f'umulh {t[3]}, {a[0]}, {a[2]}')
    e(f'mul {x}, {a[0]}, {a[3]}')
    e(f'adcs {t[3]}, {t[3]}, {x}')
    e(f'umulh {t[4]}, {a[0]}, {a[3]}')
    e(f'adc {t[4]}, {t[4]}, xzr')
    e(f'mul {x}, {a[1]}, {a[2]}')
    e(f'umulh {y}, {a[1]}, {a[2]}')
    e(f'mul {z}, {a[1]}, {a[3]}')
    e(f'umulh {t[5]}, {a[1]}, {a[3]}')
    e(f'adds {y}, {y}, {z}')
    e(f'adc {t[5]}, {t[5]}, xzr')
    e(f'adds {t[3]}, {t[3]}, {x}')
    e(f'adcs {t[4]}, {t[4]}, {y}')
    e(f'adc {t[5]}, {t[5]}, xzr')
    e(f'mul {x}, {a[2]}, {a[3]}')
    e(f'umulh {t[6]}, {a[2]}, {a[3]}')
    e(f'adds {t[5]}, {t[5]}, {x}')
    e(f'adc {t[6]}, {t[6]}, xzr')
    # Doubled
    e(f'adds {t[1]}, {t[1]}, {t[1]}')
    for k in range(2, 7):
        e(f'adcs {t[k]}, {t[k]}, {t[k]}')
    e(f'adc {t[7]}, xzr, xzr')
    # Plus the squares a[i]^2 at 2i
    e(f'mul {t[0]}, {a[0]}, {a[0]}')
    e(f'umulh {x}, {a[0]}, {a[0]}')
    e(f'adds {t[1]}, {t[1]}, {x}')
    for k in range(1, 4):
        e(f'mul {x}, {a[k]}, {a[k]}')
        e(f'adcs {t[2*k]}, {t[2*k]}, {x}')
        e(f'umulh {x}, {a[k]}, {a[k]}')
        e(f'adcs {t[2*k+1]}, {t[2*k+1]}, {x}' if k < 3 else f'adc {t[7]}, {t[7]}, {x}')
    # Four words of reduction of the low half, then the high half added
    w = t[0:4]
    for i in range(4):
        reduce_word(e, w, c1, y, z)
        w = w[1:] + [w[0]]
    e(f'adds {w[0]}, {w[0]}, {t[4]}')
    for k in range(1, 4):
        e(f'adcs {w[k]}, {w[k]}, {t[4+k]}')
    e(f'adc {x}, xzr, xzr')
    reduce_store(e, w + [x], a, c1, y)
    return o


def add_body():
    o = []
    e = o.append
    a, b = ['x2', 'x3', 'x4', 'x5'], ['x9', 'x10', 'x11', 'x12']
    e('ldr x0, [sp]')
    e('ldr x1, [sp, #8]')
    load(e, 'x0', a)
    load(e, 'x1', b)
    e(f'adds {a[0]}, {a[0]}, {b[0]}')
    for k in range(1, 4):
        e(f'adcs {a[k]}, {a[k]}, {b[k]}')
    e('adc x6, xzr, xzr')
    e('mov x7, #0xffffffff00000001')
    reduce_store(e, a + ['x6'], b, 'x7', 'x13')
    return o


def sub_body():
    o = []
    e = o.append
    a, b = ['x2', 'x3', 'x4', 'x5'], ['x9', 'x10', 'x11', 'x12']
    e('ldr x0, [sp]')
    e('ldr x1, [sp, #8]')
    load(e, 'x0', a)
    load(e, 'x1', b)
    e(f'subs {a[0]}, {a[0]}, {b[0]}')
    for k in range(1, 4):
        e(f'sbcs {a[k]}, {a[k]}, {b[k]}')
    # On a borrow add p back: x6 all ones, p's words masked by it
    e('sbc x6, xzr, xzr')
    e('lsr x7, x6, #32')
    e('mov x13, #0xffffffff00000001')
    e('and x13, x13, x6')
    e(f'adds {a[0]}, {a[0]}, x6')
    e(f'adcs {a[1]}, {a[1]}, x7')
    e(f'adcs {a[2]}, {a[2]}, xzr')
    e(f'adc {a[3]}, {a[3]}, x13')
    store(e, a)
    return o


# select_point(table, n, idx): table[idx] (12 words) reading all n entries.
SELECT = '''    ldr x0, [sp]
    ldr x1, [sp, #8]
    ldr x2, [sp, #16]
    movi v0.16b, #0
    movi v1.16b, #0
    movi v2.16b, #0
    movi v3.16b, #0
    movi v4.16b, #0
    movi v5.16b, #0
    mov x3, #0
    cbz x1, .done
.loop:
    cmp x3, x2
    csetm x4, eq
    dup v31.2d, x4
    ld1 {v16.16b, v17.16b, v18.16b, v19.16b}, [x0], #64
    ld1 {v20.16b, v21.16b}, [x0], #32
    and v16.16b, v16.16b, v31.16b
    and v17.16b, v17.16b, v31.16b
    and v18.16b, v18.16b, v31.16b
    and v19.16b, v19.16b, v31.16b
    and v20.16b, v20.16b, v31.16b
    and v21.16b, v21.16b, v31.16b
    orr v0.16b, v0.16b, v16.16b
    orr v1.16b, v1.16b, v17.16b
    orr v2.16b, v2.16b, v18.16b
    orr v3.16b, v3.16b, v19.16b
    orr v4.16b, v4.16b, v20.16b
    orr v5.16b, v5.16b, v21.16b
    add x3, x3, #1
    cmp x3, x1
    b.ne .loop
.done:
    st1 {v0.16b, v1.16b, v2.16b, v3.16b}, [x8], #64
    st1 {v4.16b, v5.16b}, [x8]
    ret
'''

FUNCS = [
    ('fe_mul', mul_body, 2, lambda x, y: x * y * pow(R, -1, P) % P, '''// fe_mul(a, b): a * b / 2^256 mod p (a, b < p), as Go's p256MulInternal.
// a's limbs in x2-x5, b's read one per round; the 5-word sum rotates
// through x9-x14, each round adding a * b[i] and then the quotient's
// multiple of p, which leaves the low word zero (dropped).'''),
    ('fe_sqr', sqr_body, 1, lambda x, y: x * x * pow(R, -1, P) % P, '''// fe_sqr(a): a * a / 2^256 mod p, as Go's p256SqrInternal: the products
// a[i] * a[j] (i < j) once, doubled, plus the squares; four words of
// reduction of the low half, plus the high half.'''),
    ('fe_add', add_body, 2, lambda x, y: (x + y) % P, '''// fe_add(a, b): a + b mod p.'''),
    ('fe_sub', sub_body, 2, lambda x, y: (x - y) % P, '''// fe_sub(a, b): a - b mod p.'''),
]


# Runs a function's instructions with a at 0x1000, b at 0x2000 (their
# addresses the arguments at sp), the result at x8 = 0x3000.
def sim(prog, a, b):
    reg = {f'x{i}': 0 for i in range(32)}
    mem = {}
    def put(addr, v):
        for k in range(4): mem[addr + 8*k] = (v >> (64*k)) & M64
    put(0x1000, a); put(0x2000, b)
    mem[0x100] = 0x1000; mem[0x108] = 0x2000
    reg['sp'] = 0x100; reg['x8'] = 0x3000
    C = 0
    def g(x):
        x = x.strip()
        if x == 'xzr': return 0
        if x.startswith('#'): return int(x[1:], 0) & M64
        return reg[x]
    def addr(ops):
        inner = ops.strip()[1:-1].split(',')
        base = reg[inner[0].strip()]
        off = int(inner[1].strip()[1:]) if len(inner) > 1 else 0
        return base + off
    for ins in prog:
        op, rest = ins.split(' ', 1) if ' ' in ins else (ins, '')
        if op == 'ret': break
        if op == 'adr':
            r_, lab = [x.strip() for x in rest.split(',')]
            base, words = TABLES[lab]
            for k, wv in enumerate(words): mem[base + 8*k] = wv
            reg[r_] = base
            continue
        if op in ('ldr', 'ldp', 'stp'):
            regs, br = rest.split('[')
            regs = [x.strip() for x in regs.split(',') if x.strip()]
            ad = addr('[' + br)
            for k, r_ in enumerate(regs):
                if op == 'stp': mem[ad + 8*k] = reg[r_]
                else: reg[r_] = mem[ad + 8*k]
            continue
        args = [x.strip() for x in rest.split(',')]
        d = args[0]
        def w(v):
            if d != 'xzr': reg[d] = v & M64
        if op == 'mov': w(g(args[1]))
        elif op == 'mul': w(g(args[1]) * g(args[2]))
        elif op == 'umulh': w((g(args[1]) * g(args[2])) >> 64)
        elif op == 'lsr': w(g(args[1]) >> int(args[2][1:]))
        elif op == 'and': w(g(args[1]) & g(args[2]))
        elif op in ('adds', 'adcs', 'adc'):
            y = g(args[2])
            if len(args) == 4: y = (y << int(args[3].split('#')[1])) & M64
            v = g(args[1]) + y + (C if op != 'adds' else 0)
            w(v)
            if op != 'adc': C = v >> 64
        elif op in ('subs', 'sbcs', 'sbc'):
            v = g(args[1]) + (g(args[2]) ^ M64) + (C if op != 'subs' else 1)
            w(v)
            if op != 'sbc': C = v >> 64
        elif op == 'csel':
            assert args[3] == 'lo'
            w(g(args[1]) if C == 0 else g(args[2]))
        else: raise Exception(op)
    return sum(mem[0x3000 + 8*k] << (64*k) for k in range(4))


# amd64 (Intel syntax). MUL leaves rdx:rax and sets the flags, so each
# limb's product is added with its own carries, as Go's p256_asm_amd64.s.
# Arguments at [rsp + 8 + 8*i], the result at r13; fe_mul pushes rbx and
# rbp for the duration.

C1 = '0xffffffff00000001'


def mem(base, k):
    return f'qword ptr [{base} + {8*k}]' if k else f'qword ptr [{base}]'


def x_store(e, s):
    for k in range(4):
        e(f'mov {mem("r13", k)}, {s[k]}')


# Stores s[0..4] (s < 2p) less p unless that borrows; r: 4 free registers,
# t: one more.
def x_reduce_store(e, s, r, t):
    for k in range(4):
        e(f'mov {r[k]}, {s[k]}')
    e(f'sub {s[0]}, -1')
    e(f'mov {t}, 0xffffffff')
    e(f'sbb {s[1]}, {t}')
    e(f'sbb {s[2]}, 0')
    e(f'mov {t}, {C1}')
    e(f'sbb {s[3]}, {t}')
    e(f'sbb {s[4]}, 0')
    for k in range(4):
        e(f'cmovb {s[k]}, {r[k]}')
    x_store(e, s)


def x_mul_body(sqr=False):
    o = []
    e = o.append
    t = 'rbp'
    s = ['r8', 'r9', 'r10', 'r11', 'r15']
    free = 'rbx'
    e('mov rsi, qword ptr [rsp + 8]')
    e('mov rdi, rsi' if sqr else 'mov rdi, qword ptr [rsp + 16]')
    e('push rbx')
    e('push rbp')
    for i in range(4):
        e(f'mov rcx, {mem("rdi", i)}')
        s5 = free
        if i == 0:
            # T = a * b0
            e(f'mov rax, {mem("rsi", 0)}')
            e('mul rcx')
            e(f'mov {s[0]}, rax')
            e(f'mov {t}, rdx')
            for k in range(1, 4):
                e(f'mov rax, {mem("rsi", k)}')
                e('mul rcx')
                e(f'add rax, {t}')
                e('adc rdx, 0')
                e(f'mov {s[k]}, rax')
                e(f'mov {s[4] if k == 3 else t}, rdx')
            e(f'mov {s5}, 0')
        else:
            # T += a * bi, limb by limb (the carry word in t)
            for k in range(4):
                e(f'mov rax, {mem("rsi", k)}')
                e('mul rcx')
                if k:
                    e(f'add rax, {t}')
                    e('adc rdx, 0')
                e(f'add {s[k]}, rax')
                e('adc rdx, 0')
                e(f'mov {t}, rdx')
            e(f'mov {s5}, 0')
            e(f'add {s[4]}, {t}')
            e(f'adc {s5}, 0')
        # T += m * p with m = T[0]: m << 96 plus m * 0xffffffff00000001 << 192
        m = s[0]
        e(f'mov rax, {C1}')
        e(f'mul {m}')
        e(f'mov {t}, {m}')
        e(f'shl {t}, 32')
        e(f'shr {m}, 32')
        e(f'add {s[1]}, {t}')
        e(f'adc {s[2]}, {m}')
        e(f'adc {s[3]}, rax')
        e(f'adc {s[4]}, rdx')
        e(f'adc {s5}, 0')
        free = s[0]
        s = s[1:] + [s5]
    x_reduce_store(e, s, ['rax', 'rcx', 'rdx', 'rsi'], 'rdi')
    e('pop rbp')
    e('pop rbx')
    e('ret')
    return o


def x_add_body():
    o = []
    e = o.append
    s = ['r8', 'r9', 'r10', 'r11', 'rax']
    e('mov rsi, qword ptr [rsp + 8]')
    e('mov rdi, qword ptr [rsp + 16]')
    for k in range(4):
        e(f'mov {s[k]}, {mem("rsi", k)}')
    e('mov rax, 0')
    e(f'add r8, {mem("rdi", 0)}')
    for k in range(1, 4):
        e(f'adc {s[k]}, {mem("rdi", k)}')
    e('adc rax, 0')
    x_reduce_store(e, s, ['rcx', 'rdx', 'rsi', 'rdi'], 'r15')
    e('ret')
    return o


def x_sub_body():
    o = []
    e = o.append
    s = ['r8', 'r9', 'r10', 'r11']
    e('mov rsi, qword ptr [rsp + 8]')
    e('mov rdi, qword ptr [rsp + 16]')
    for k in range(4):
        e(f'mov {s[k]}, {mem("rsi", k)}')
    e(f'sub r8, {mem("rdi", 0)}')
    for k in range(1, 4):
        e(f'sbb {s[k]}, {mem("rdi", k)}')
    # On a borrow add p back: rax all ones, p's words masked by it
    e('sbb rax, rax')
    e('mov rdx, rax')
    e('shr rdx, 32')
    e(f'mov rsi, {C1}')
    e('and rsi, rax')
    e('add r8, rax')
    e('adc r9, rdx')
    e('adc r10, 0')
    e('adc r11, rsi')
    x_store(e, s)
    e('ret')
    return o


# select_point on amd64 (SSE2): the mask from idx ^ j, broadcast.
X_SELECT = [
    'mov rsi, qword ptr [rsp + 8]',
    'mov rcx, qword ptr [rsp + 16]',
    'mov rdx, qword ptr [rsp + 24]',
] + [f'pxor xmm{k}, xmm{k}' for k in range(6)] + [
    'mov rax, 0',
    'cmp rcx, 0',
    'je .done',
    '.loop:',
    'mov r8, rax',
    'xor r8, rdx',
    'mov r9, r8',
    'neg r9',
    'or r8, r9',
    'shr r8, 63',
    'sub r8, 1',
    'movq xmm15, r8',
    'pshufd xmm15, xmm15, 0x44',
] + [f'movdqu xmm{8+k}, xmmword ptr [rsi + {16*k}]' if k else 'movdqu xmm8, xmmword ptr [rsi]' for k in range(6)] \
  + [f'pand xmm{8+k}, xmm15' for k in range(6)] \
  + [f'por xmm{k}, xmm{8+k}' for k in range(6)] + [
    'add rsi, 96',
    'inc rax',
    'cmp rax, rcx',
    'jne .loop',
    '.done:',
] + [f'movdqu xmmword ptr [r13 + {16*k}], xmm{k}' if k else 'movdqu xmmword ptr [r13], xmm0' for k in range(6)] + ['ret']

X_FUNCS = [
    ('fe_mul', x_mul_body, '''// fe_mul(a, b): a * b / 2^256 mod p (a, b < p), as Go's p256MulInternal:
// the 5-word sum rotates through r8-r11, r15 and rbx, each round adding
// a * b[i] and then the quotient's multiple of p, which leaves the low
// word zero (dropped).'''),
    ('fe_sqr', lambda: x_mul_body(True), '// fe_sqr(a): fe_mul(a, a).'),
    ('fe_add', x_add_body, '// fe_add(a, b): a + b mod p.'),
    ('fe_sub', x_sub_body, '// fe_sub(a, b): a - b mod p.'),
]


# Runs amd64 instructions as sim does arm64's: the arguments' addresses
# at rsp + 8, the result at r13.
def xsim(prog, a, b):
    regs = ['rax', 'rbx', 'rcx', 'rdx', 'rsi', 'rdi', 'rbp', 'rsp'] + [f'r{i}' for i in range(8, 16)]
    reg = {r: 0x5555 + i for i, r in enumerate(regs)}
    mem = {}
    def put(addr, v):
        for k in range(4): mem[addr + 8*k] = (v >> (64*k)) & M64
    put(0x1000, a); put(0x2000, b)
    reg['rsp'] = 0x100
    mem[0x108] = 0x1000; mem[0x110] = 0x2000
    reg['r13'] = 0x3000
    saved = (reg['rbx'], reg['rbp'], reg['r12'], reg['r14'])
    C = 0
    for base, words in X_TABLES.values():
        for k, wv in enumerate(words): mem[base + 8*k] = wv
    def ea(x):
        inner = x.split('[')[1].rstrip(']').split('+')
        if inner[0].strip() == 'rip': return X_TABLES[inner[1].strip()][0]
        return sum(reg[p.strip()] if p.strip() in reg else int(p.strip(), 0) for p in inner)
    def g(x):
        x = x.strip()
        if x in reg: return reg[x]
        if '[' in x: return mem[ea(x)]
        return int(x, 0) & M64
    for ins in prog:
        op, rest = ins.split(' ', 1) if ' ' in ins else (ins, '')
        args = [x.strip() for x in rest.split(',')] if rest else []
        def w(v):
            if '[' in args[0]: mem[ea(args[0])] = v & M64
            else: reg[args[0]] = v & M64
        if op == 'ret':
            assert reg['rsp'] == 0x100 and saved == (reg['rbx'], reg['rbp'], reg['r12'], reg['r14'])
            break
        elif op == 'push':
            reg['rsp'] -= 8; mem[reg['rsp']] = reg[args[0]]
        elif op == 'pop':
            reg[args[0]] = mem[reg['rsp']]; reg['rsp'] += 8
        elif op == 'mov': w(g(args[1]))
        elif op == 'mul':
            v = reg['rax'] * g(args[0])
            reg['rax'] = v & M64; reg['rdx'] = v >> 64; C = None
        elif op in ('add', 'adc'):
            v = g(args[0]) + g(args[1]) + (C if op == 'adc' else 0)
            w(v); C = v >> 64
        elif op in ('sub', 'sbb'):
            v = g(args[0]) - g(args[1]) - (C if op == 'sbb' else 0)
            w(v); C = 1 if v < 0 else 0
        elif op in ('shl', 'shr'):
            w(g(args[0]) << int(args[1]) if op == 'shl' else g(args[0]) >> int(args[1])); C = None
        elif op == 'and': w(g(args[0]) & g(args[1])); C = None
        elif op == 'cmovb':
            if C: w(g(args[1]))
        else: raise Exception(op)
    return sum(mem[0x3000 + 8*k] << (64*k) for k in range(4))


# --- Scalars modulo the group order N (ECDSA), Montgomery form: the same
# CIOS as fe_mul, each word's quotient m = T[0] * -N^-1 (not 1 here) and
# N's words read from a table after the code.
N = 0xffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551
NINV = (-pow(N, -1, 2**64)) % 2**64
N_WORDS = [(N >> (64 * k)) & M64 for k in range(4)] + [NINV]
# Simulated addresses of the tables (adr / [rip + label]).
TABLES = {'.n': (0x5000, N_WORDS)}


def sc_mul_body():
    o = []
    e = o.append
    a = ['x2', 'x3', 'x4', 'x5']
    b, tb, t, m, k = 'x7', 'x6', 'x15', 'x17', 'x0'
    s = ['x9', 'x10', 'x11', 'x12', 'x13']
    free = 'x14'
    e('ldr x0, [sp]')
    e('ldr x1, [sp, #8]')
    load(e, 'x0', a)
    e(f'adr {tb}, .n')
    for i in range(4):
        e(f'ldr {b}, [x1, #{8*i}]' if i else f'ldr {b}, [x1]')
        s5 = free
        if i == 0:
            for j in range(4):
                e(f'mul {s[j]}, {a[j]}, {b}')
            e(f'umulh {t}, {a[0]}, {b}')
            e(f'adds {s[1]}, {s[1]}, {t}')
            e(f'umulh {t}, {a[1]}, {b}')
            e(f'adcs {s[2]}, {s[2]}, {t}')
            e(f'umulh {t}, {a[2]}, {b}')
            e(f'adcs {s[3]}, {s[3]}, {t}')
            e(f'umulh {s[4]}, {a[3]}, {b}')
            e(f'adc {s[4]}, {s[4]}, xzr')
        else:
            e(f'mul {t}, {a[0]}, {b}')
            e(f'adds {s[0]}, {s[0]}, {t}')
            for j in range(1, 4):
                e(f'mul {t}, {a[j]}, {b}')
                e(f'adcs {s[j]}, {s[j]}, {t}')
            e(f'adcs {s[4]}, {s[4]}, xzr')
            e(f'adc {s5}, xzr, xzr')
            e(f'umulh {t}, {a[0]}, {b}')
            e(f'adds {s[1]}, {s[1]}, {t}')
            for j in range(1, 4):
                e(f'umulh {t}, {a[j]}, {b}')
                e(f'adcs {s[j+1]}, {s[j+1]}, {t}')
            e(f'adc {s5}, {s5}, xzr')
        # T += m * N, m = T[0] * -N^-1: the low halves, then the high ones
        e(f'ldr {k}, [{tb}, #32]')
        e(f'mul {m}, {s[0]}, {k}')
        for j in range(4):
            e(f'ldr {k}, [{tb}, #{8*j}]' if j else f'ldr {k}, [{tb}]')
            e(f'mul {t}, {m}, {k}')
            e(f'{"adds" if j == 0 else "adcs"} {s[j]}, {s[j]}, {t}')
        e(f'adcs {s[4]}, {s[4]}, xzr')
        e(f'adc {s5}, {s5}, xzr' if i else f'adc {s5}, xzr, xzr')
        for j in range(4):
            e(f'ldr {k}, [{tb}, #{8*j}]' if j else f'ldr {k}, [{tb}]')
            e(f'umulh {t}, {m}, {k}')
            e(f'{"adds" if j == 0 else "adcs"} {s[j+1]}, {s[j+1]}, {t}')
        e(f'adc {s5}, {s5}, xzr')
        free = s[0]
        s = s[1:] + [s5]
    # T < 2N: less N unless that borrows
    nn = ['x15', 'x16', 'x17', 'x7']
    e(f'ldp {nn[0]}, {nn[1]}, [{tb}]')
    e(f'ldp {nn[2]}, {nn[3]}, [{tb}, #16]')
    r = a
    e(f'subs {r[0]}, {s[0]}, {nn[0]}')
    for j in range(1, 4):
        e(f'sbcs {r[j]}, {s[j]}, {nn[j]}')
    e(f'sbcs xzr, {s[4]}, xzr')
    for j in range(4):
        e(f'csel {r[j]}, {s[j]}, {r[j]}, lo')
    store(e, r)
    return o


SC_DATA = '    .balign 8\n.n:\n    .quad ' + ', '.join(f'{w:#x}' for w in N_WORDS) + '\n'


def x_sc_mul_body():
    o = []
    e = o.append
    t = 'rbp'
    s = ['r8', 'r9', 'r10', 'r11', 'r15']
    free = 'rbx'
    e('mov rsi, qword ptr [rsp + 8]')
    e('mov rdi, qword ptr [rsp + 16]')
    e('push rbx')
    e('push rbp')
    for i in range(4):
        e(f'mov rcx, {mem("rdi", i)}')
        s5 = free
        if i == 0:
            e(f'mov rax, {mem("rsi", 0)}')
            e('mul rcx')
            e(f'mov {s[0]}, rax')
            e(f'mov {t}, rdx')
            for j in range(1, 4):
                e(f'mov rax, {mem("rsi", j)}')
                e('mul rcx')
                e(f'add rax, {t}')
                e('adc rdx, 0')
                e(f'mov {s[j]}, rax')
                e(f'mov {s[4] if j == 3 else t}, rdx')
            e(f'mov {s5}, 0')
        else:
            for j in range(4):
                e(f'mov rax, {mem("rsi", j)}')
                e('mul rcx')
                if j:
                    e(f'add rax, {t}')
                    e('adc rdx, 0')
                e(f'add {s[j]}, rax')
                e('adc rdx, 0')
                e(f'mov {t}, rdx')
            e(f'mov {s5}, 0')
            e(f'add {s[4]}, {t}')
            e(f'adc {s5}, 0')
        # m = T[0] * -N^-1 (in rcx); T += m * N limb by limb
        e('mov rax, qword ptr [rip + .ninv]')
        e(f'mul {s[0]}')
        e('mov rcx, rax')
        for j in range(4):
            e(f'mov rax, qword ptr [rip + .n{j}]')
            e('mul rcx')
            if j:
                e(f'add rax, {t}')
                e('adc rdx, 0')
            e(f'add {s[j]}, rax')
            e('adc rdx, 0')
            e(f'mov {t}, rdx')
        e(f'add {s[4]}, {t}')
        e(f'adc {s5}, 0')
        free = s[0]
        s = s[1:] + [s5]
    r = ['rax', 'rcx', 'rdx', 'rsi']
    for j in range(4):
        e(f'mov {r[j]}, {s[j]}')
    e(f'sub {s[0]}, qword ptr [rip + .n0]')
    for j in range(1, 4):
        e(f'sbb {s[j]}, qword ptr [rip + .n{j}]')
    e(f'sbb {s[4]}, 0')
    for j in range(4):
        e(f'cmovb {s[j]}, {r[j]}')
    x_store(e, s)
    e('pop rbp')
    e('pop rbx')
    e('ret')
    return o


X_SC_DATA = '    .balign 8\n' + ''.join(f'.n{j}:\n    .quad {N_WORDS[j]:#x}\n' for j in range(4)) + f'.ninv:\n    .quad {NINV:#x}\n'
X_TABLES = dict([(f'.n{j}', (0x5000 + 8 * j, [N_WORDS[j]])) for j in range(4)] + [('.ninv', (0x5020, [NINV]))])

# select_affine(table, n, idx): as select_point for 8-word entries.
SELECT_AFFINE = SELECT.replace('    movi v4.16b, #0\n    movi v5.16b, #0\n', '').replace(
    '    ld1 {v16.16b, v17.16b, v18.16b, v19.16b}, [x0], #64\n    ld1 {v20.16b, v21.16b}, [x0], #32\n',
    '    ld1 {v16.16b, v17.16b, v18.16b, v19.16b}, [x0], #64\n').replace(
    '    and v20.16b, v20.16b, v31.16b\n    and v21.16b, v21.16b, v31.16b\n', '').replace(
    '    orr v4.16b, v4.16b, v20.16b\n    orr v5.16b, v5.16b, v21.16b\n', '').replace(
    '    st1 {v0.16b, v1.16b, v2.16b, v3.16b}, [x8], #64\n    st1 {v4.16b, v5.16b}, [x8]\n',
    '    st1 {v0.16b, v1.16b, v2.16b, v3.16b}, [x8]\n')
X_SELECT_AFFINE = [l for l in X_SELECT if not any(f'xmm{k}' in l for k in (4, 5, 12, 13))]
X_SELECT_AFFINE = [l.replace('add rsi, 96', 'add rsi, 64') for l in X_SELECT_AFFINE]


FUNCS.append(('sc_mul', sc_mul_body, 2, lambda x, y: x * y * pow(R, -1, N) % N, '''// sc_mul(a, b): a * b / 2^256 mod N (the group order; a, b < N): fe_mul's
// rounds with the quotient T[0] * -N^-1 and N's words from .n.'''))
X_FUNCS.append(('sc_mul', x_sc_mul_body, '''// sc_mul(a, b): a * b / 2^256 mod N (the group order), as fe_mul with the
// quotient T[0] * -N^-1 and N's words from .n0-.n3.'''))
DATA = {'sc_mul': SC_DATA}
X_DATA = {'sc_mul': X_SC_DATA}


def main():
    if sys.argv[1:] == ['check']:
        edge = [0, 1, 2, P-1, P-2, 2**255, 2**64-1, 2**256 % P, P - 2**64, 2**224, (P-1)//2]
        cases = [(x, y) for x in edge for y in edge] + [(random.randrange(P), random.randrange(P)) for _ in range(20000)]
        nedge = [0, 1, 2, N-1, N-2, 2**255, 2**64-1, 2**256 % N, N - 2**64, (N-1)//2]
        ncases = [(x, y) for x in nedge for y in nedge] + [(random.randrange(N), random.randrange(N)) for _ in range(20000)]
        for name, body, _, want, _ in FUNCS:
            prog = body()
            for x, y in (ncases if name == 'sc_mul' else cases):
                got = sim(prog, x, y)
                if got != want(x, y):
                    print(name, 'MISMATCH', hex(x), hex(y), hex(got), hex(want(x, y))); sys.exit(1)
            print(name, 'arm64 ok', len(cases), 'cases', len(prog), 'instructions')
        wants = dict((f[0], f[3]) for f in FUNCS)
        for name, body, _ in X_FUNCS:
            prog = body()
            for x, y in (ncases if name == 'sc_mul' else cases):
                got = xsim(prog, x, x if name == 'fe_sqr' else y)
                if got != wants[name](x, y):
                    print(name, 'amd64 MISMATCH', hex(x), hex(y), hex(got), hex(wants[name](x, y))); sys.exit(1)
            print(name, 'amd64 ok', len(cases), 'cases', len(prog), 'instructions')
        return
    root = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..')
    path = os.path.join(root, 'lib', 'std', 'src', 'crypto', 'nistec_p256_arm64.s')
    with open(path, 'w') as f:
        f.write('''// Generated by tools/genp256asm.py (do not edit): crypto/nistec_p256.navi's
// #[asm] functions for arm64. Field elements are 4 little-endian limbs
// below p, in Montgomery form for fe_mul and fe_sqr.
''')
        for name, body, _, _, doc in FUNCS:
            f.write('\n' + doc + '\n' + name + ':\n')
            f.write(''.join('    ' + l + '\n' for l in body()))
            f.write(DATA.get(name, ''))
        f.write('''
// select_point(table, n, idx): the point table[idx] (idx < n), every entry read
// (constant time), as Go's p256Select: each masked by idx == j and or-ed.
select_point:
''' + SELECT)
        f.write('''
// select_affine(table, n, idx): the same for affine points (8 words).
select_affine:
''' + SELECT_AFFINE)
    print(path)
    path = os.path.join(root, 'lib', 'std', 'src', 'crypto', 'nistec_p256_amd64.s')
    with open(path, 'w') as f:
        f.write('''// Generated by tools/genp256asm.py (do not edit): crypto/nistec_p256.navi's
// #[asm] functions for amd64. Field elements are 4 little-endian limbs
// below p, in Montgomery form for fe_mul and fe_sqr.
''')
        for name, body, doc in X_FUNCS:
            f.write('\n' + doc + '\n' + name + ':\n')
            f.write(''.join('    ' + l + '\n' for l in body()))
            f.write(X_DATA.get(name, ''))
        f.write('''
// select_point(table, n, idx): the point table[idx] (idx < n), every entry read
// (constant time), as Go's p256Select: each masked by idx == j and or-ed.
select_point:
''')
        f.write(''.join(('' if l.startswith('.') else '    ') + l + '\n' for l in X_SELECT))
        f.write('''
// select_affine(table, n, idx): the same for affine points (8 words).
select_affine:
''')
        f.write(''.join(('' if l.startswith('.') else '    ') + l + '\n' for l in X_SELECT_AFFINE))
    print(path)


main()
