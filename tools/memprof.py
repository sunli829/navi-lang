#!/usr/bin/env python3
# Sums the live heap samples a navi program prints with NAVI_MEMPROF=1
# (at the end of each mark: "MEMPROF cycle N mstart ADDR", then one
# "M size pc..." line a live sample) for the cycle with the most, by the
# functions that allocated them.
#
#   NAVI_MEMPROF=1 prog 2> prof.txt; tools/memprof.py prog < prof.txt [depth]
import bisect, collections, re, subprocess, sys

RATE = 262144
binary = sys.argv[1]
depth = int(sys.argv[2]) if len(sys.argv) > 2 else 2
syms = []
for line in subprocess.run(["nm", binary], capture_output=True, text=True).stdout.splitlines():
    p = line.split()
    if len(p) == 3 and p[1] in "tT":
        syms.append((int(p[0], 16), p[2]))
syms.sort()
addrs = [a for a, _ in syms]
mstart = [a for a, n in syms if n.endswith("_navi_mstart") or n == "_navi_mstart"]

cycles = []
for line in sys.stdin:
    if line.startswith("MEMPROF "):
        m = re.match(r"MEMPROF cycle (\d+) mstart (0x[0-9a-f]+)", line)
        cycles.append((int(m.group(1)), int(m.group(2), 16), []))
    elif line.startswith("M ") and cycles:
        p = line.split()
        cycles[-1][2].append((int(p[1]), [int(x, 16) for x in p[2:]]))
if not cycles:
    sys.exit("no samples")
cyc, rt_mstart, samples = max(cycles, key=lambda c: sum(max(s, RATE) for s, _ in c[2]))
slide = rt_mstart - mstart[0] if mstart else 0

def name(pc):
    if pc == 0:
        return "-"
    i = bisect.bisect_right(addrs, pc - slide) - 1
    n = syms[i][1] if i >= 0 else "?"
    return re.sub(r"\.r$", "", re.sub(r"^_navi_\d+_", "", n))

SKIP = {"prof_alloc", "alloc", "alloc_typed", "alloc_array_typed", "gc_alloc_typed", "gc_alloc_array", "gc_alloc", "alloc_array"}
by = collections.Counter()
total = 0
for size, pcs in samples:
    names = [name(pc) for pc in pcs]
    names = [n for n in names if n not in SKIP]
    w = max(size, RATE)
    total += w
    by[" <- ".join(names[:depth])] += w
print(f"cycle {cyc}: {len(samples)} samples, about {total >> 20} MiB live")
for k, w in by.most_common(40):
    print(f"{w >> 20:6d} MiB {100 * w / total:5.1f}%  {k}")
