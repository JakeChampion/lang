#!/usr/bin/env python3
# gen-ed25519.py: the generated region of std/crypto/ed25519, BASE, the
# fixed-base table __mul_base reads: for each j in 0..31 and k in 1..8, the
# affine point k * 256^j * B as (y + x, y - x, 2d * x * y), four 64-bit limbs
# each, below p. Run it after changing anything below; it checks the table's
# points lie on the curve before rewriting the region.
import os, sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from limbgen import limbs, lit, write_region

P = 2**255 - 19
D = -121665 * pow(121666, -1, P) % P

def inv(x):
    return pow(x, P - 2, P)

def add(a, b):
    (x1, y1), (x2, y2) = a, b
    t = D * x1 * x2 * y1 * y2 % P
    return ((x1 * y2 + y1 * x2) * inv(1 + t) % P, (y1 * y2 + x1 * x2) * inv(1 - t) % P)

def base():
    y = 4 * inv(5) % P
    xx = (y * y - 1) * inv(D * y * y + 1) % P
    x = pow(xx, (P + 3) // 8, P)
    if (x * x - xx) % P != 0:
        x = x * pow(2, (P - 1) // 4, P) % P
    if x & 1:
        x = P - x
    return (x, y)

def on_curve(pt):
    x, y = pt
    return (-x * x + y * y - 1 - D * x * x * y * y) % P == 0

def table():
    words = []
    row = base()
    for j in range(32):
        acc = None
        for k in range(1, 9):
            acc = row if acc is None else add(acc, row)
            assert on_curve(acc)
            x, y = acc
            for v in ((y + x) % P, (y - x) % P, 2 * D * x * y % P):
                words += limbs(v)
        for _ in range(8):
            row = add(row, row)
    lines = []
    for at in range(0, len(words), 4):
        lines.append("  " + ", ".join(lit(w) for w in words[at:at + 4]) + ",")
    return "// BASE stores `k * 256^j * B` at `((j * 8 + k - 1) * 12)`.\n// Each point stores `y + x`, `y - x`, and `2d * x * y`, with four limbs per value.\nconst BASE: u64[] = [\n" + "\n".join(lines) + "\n];\n"

if __name__ == "__main__":
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "internal", "stdlib", "std", "crypto", "ed25519.fern.md")
    write_region(path, "ED25519 BASE TABLE", "gen-ed25519.py", table())
