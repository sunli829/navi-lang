'''Checks compiler/src/asm against clang: each instruction of tests/asm/<arch>.txt
assembled by both, disassembled by objdump, must read the same.

    python3 tools/asmcheck.py arm64|amd64 path/to/navi tests/asm/<arch>.txt tmpdir
'''
import subprocess, sys, re, os
arch = sys.argv[1]
navi = sys.argv[2]
insts = [l.strip() for l in open(sys.argv[3]) if l.strip() and not l.startswith('#')]
d = sys.argv[4]
os.makedirs(d, exist_ok=True)
target = 'arm64-apple-macos' if arch == 'arm64' else 'x86_64-apple-macos'
pre = '' if arch == 'arm64' else '.intel_syntax noprefix\n'
src = ''.join(f'f{i}:\n    {x}\n' for i, x in enumerate(insts))
open(f'{d}/mine.s', 'w').write(src)
open(f'{d}/ref.s', 'w').write(pre + src)
out = subprocess.run([navi, 'asm-dump', arch, f'{d}/mine.s'], capture_output=True, text=True)
if out.returncode != 0:
    print(out.stderr); sys.exit(1)
mine = {}
for l in out.stdout.splitlines():
    n, h = l.split(' ', 1) if ' ' in l else (l, '')
    mine[n] = h
# My bytes as .byte, disassembled by objdump.
bs = ''.join(f'_f{i}:\n    .byte {",".join("0x"+mine["f%d"%i][k:k+2] for k in range(0, len(mine["f%d"%i]), 2)) or "0"}\n' for i in range(len(insts)))
open(f'{d}/mineb.s', 'w').write(bs)
def dis(srcfile, obj):
    r = subprocess.run(['clang', '-c', '-target', target, '-x', 'assembler', srcfile, '-o', obj], capture_output=True, text=True)
    if r.returncode != 0:
        print(r.stderr[:3000]); sys.exit(1)
    o = subprocess.run(['objdump', '-d', '--no-show-raw-insn', '-M', 'intel' if arch != 'arm64' else 'no-aliases', obj] if arch != 'arm64' else ['objdump', '-d', '--no-show-raw-insn', obj], capture_output=True, text=True).stdout
    funcs = {}
    cur = None
    for l in o.splitlines():
        m = re.match(r'^[0-9a-f]+ <_?(f\d+)>:', l)
        if m:
            cur = m.group(1); funcs[cur] = []; continue
        m = re.match(r'^\s*[0-9a-f]+:\s*(.*)$', l)
        if m and cur:
            t = re.sub(r'\s+', ' ', m.group(1).split(';')[0].split('//')[0]).strip()
            funcs[cur].append(t)
    return funcs
ref = dis(f'{d}/ref.s', f'{d}/ref.o')
got = dis(f'{d}/mineb.s', f'{d}/mineb.o')
bad = 0
for i, x in enumerate(insts):
    k = f'f{i}'
    if ref.get(k) != got.get(k):
        bad += 1
        print(f'MISMATCH {x!r}: clang {ref.get(k)} mine {got.get(k)} ({mine[k]})')
print(f'{len(insts)} instructions, {bad} differ')
sys.exit(1 if bad else 0)
