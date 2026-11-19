#!/usr/bin/env python3
# Symbolizes the addresses a navi program prints at a deadlock (the
# goroutines' return addresses) with the binary's symbols.
#
#   tools/symbolize.py path/to/binary < stderr-output
import bisect
import re
import subprocess
import sys

binary = sys.argv[1]
text = sys.stdin.read()
syms = []
for line in subprocess.run(["nm", binary], capture_output=True, text=True).stdout.splitlines():
    p = line.split()
    if len(p) == 3 and p[1] in "tT":
        syms.append((int(p[0], 16), p[2]))
syms.sort()
addrs = [a for a, _ in syms]

# The load slide: the runtime's mstart routine at run time less its
# address in the binary.
slide = 0
m = re.search(r"mstart at (0x[0-9a-f]+)", text)
if m:
    for a, n in syms:
        if n in ("_navi_mstart", "navi_mstart"):
            slide = int(m.group(1), 16) - a


def name(x):
    i = bisect.bisect_right(addrs, x - slide) - 1
    if i < 0:
        return hex(x)
    return "%s+%d" % (syms[i][1], x - slide - syms[i][0])


for line in text.splitlines():
    m = re.fullmatch(r"\t(0x[0-9a-f]+)", line)
    print("\t" + name(int(m.group(1), 16)) if m else line)
