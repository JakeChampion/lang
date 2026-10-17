#!/usr/bin/env python3
# gen-p256.py: the generated region of std/crypto/p256: the limb arithmetic
# mod p and mod n as straight-line code, the curve's constants in Montgomery
# form, and BASE, the affine j * 16^i * G for i in 0..63 and j in 1..15. Run
# it after changing anything below; it rewrites the region in place, and
# checks the formulas it emits against Python's integers first.
import os, random, sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from limbgen import M64, Gen, fe_return, limbs, lit, run, write_region

P = 2**256 - 2**224 + 2**192 + 2**96 - 1
N = 0xffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551
B = 0x5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604b
GX = 0x6b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296
GY = 0x4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5
R = 2**256
# The Montgomery product a * b / R mod p, CIOS. For P-256's p, n0 is 1 and
# the low limb is all ones, so the multiple of p that clears a limb is the
# limb itself and its low product needs no multiplication.
def mont_mul(name, p, n0, special):
    g = Gen()
    pl = limbs(p)
    av = g.params("a")
    bv = g.params("b")
    t = [None] * 6
    for i in range(4):
        c = None
        for j in range(4):
            c, t[j] = g.mac(t[j], av[i], bv[j], c)
        if t[4] is None:
            t[4], t[5] = c, None
        else:
            t[4], k = g.add(t[4], c)
            t[5] = k if t[5] is None else g.let("%s + %s" % (t[5], k))
        # m clears the low limb.
        if special:
            m = t[0]
            c = m  # t0 + m*(2^64-1) = m * 2^64
        else:
            m = g.let("%s * %s" % (t[0], lit(n0)), "m")
            c, _ = g.mac(t[0], m, lit(pl[0]), None)
        for j in range(1, 4):
            if pl[j] == 0:
                t[j - 1], k = g.add(t[j], c)
                c = k
            else:
                c, t[j - 1] = g.mac(t[j], m, lit(pl[j]), c)
        t[3], k = g.add(t[4], c)
        t[4] = k if t[5] is None else g.let("%s + %s" % (t[5], k))
        t[5] = None
    # t < 2p: subtract p, keep t when that borrows past the top limb.
    return finish(g, name, "a: Fe, b: Fe", t[:5], pl)

def finish(g, name, params, t, pl):
    d = []
    bo = None
    for j in range(4):
        x, bo = g.sub(t[j], lit(pl[j]), bo)
        d.append(x)
    # keep is all ones when t - p went below zero.
    under, _ = g.sub(t[4], bo)
    keep = g.let("0u64 - (%s & 1u64)" % under, "keep")
    out = ["%s ^ (%s ^ %s) & %s" % (d[j], d[j], t[j], keep) for j in range(4)]
    body = "\n".join(g.lines)
    return "function %s(%s): Fe {\n%s\n  return Fe { l0: %s, l1: %s, l2: %s, l3: %s };\n}\n" % (name, params, body, out[0], out[1], out[2], out[3])

def mod_add(name, p):
    g = Gen()
    pl = limbs(p)
    a = g.params("a")
    b = g.params("b")
    t = []
    c = None
    for j in range(4):
        s, c = g.add(a[j], b[j], c)
        t.append(s)
    t.append(c)
    return finish(g, name, "a: Fe, b: Fe", t, pl)

def mod_sub(name, p):
    g = Gen()
    pl = limbs(p)
    a = g.params("a")
    b = g.params("b")
    d = []
    bo = None
    for j in range(4):
        x, bo = g.sub(a[j], b[j], bo)
        d.append(x)
    mask = g.let("0u64 - %s" % bo, "mask")
    out = []
    c = None
    for j in range(4):
        s, c = g.add(d[j], g.let("%s & %s" % (lit(pl[j]), mask), "q"), c)
        out.append(s)
    body = "\n".join(g.lines)
    return "function %s(a: Fe, b: Fe): Fe {\n%s\n  return Fe { l0: %s, l1: %s, l2: %s, l3: %s };\n}\n" % (name, body, out[0], out[1], out[2], out[3])

def n0_of(p):
    return (-pow(p, -1, 2**64)) % 2**64

def borrow_fn():
    g = Gen()
    a = g.params("a")
    b = g.params("b")
    bo = None
    for j in range(4):
        _, bo = g.sub(a[j], b[j], bo)
    return "// __borrow is 1 when the plain limbs a are below b and 0 otherwise.\nfunction __borrow(a: Fe, b: Fe): u64 {\n%s\n  return %s;\n}\n" % ("\n".join(g.lines), bo)

def fe_fn(name, v, doc):
    l = limbs(v)
    return "// %s\nfunction %s(): Fe {\n  return Fe { l0: %s, l1: %s, l2: %s, l3: %s };\n}\n" % (doc, name, lit(l[0]), lit(l[1]), lit(l[2]), lit(l[3]))

def ec_add(a, b):
    if a is None:
        return b
    if b is None:
        return a
    (x1, y1), (x2, y2) = a, b
    if x1 == x2:
        if (y1 + y2) % P == 0:
            return None
        lam = (3 * x1 * x1 - 3) * pow(2 * y1, -1, P) % P
    else:
        lam = (y2 - y1) * pow(x2 - x1, -1, P) % P
    x3 = (lam * lam - x1 - x2) % P
    return (x3, (lam * (x1 - x3) - y1) % P)

def base_table():
    words = []
    unit = (GX, GY)
    for i in range(64):
        acc = None
        for j in range(1, 16):
            acc = ec_add(acc, unit)
            words += limbs(acc[0] * R % P) + limbs(acc[1] * R % P)
        for _ in range(4):
            unit = ec_add(unit, unit)
    lines = []
    for at in range(0, len(words), 4):
        lines.append("  " + ", ".join(lit(w) for w in words[at:at + 4]) + ",")
    return "// BASE holds j * 16^i * G at ((i * 15 + j - 1) * 8): x then y, four limbs\n// each, in Montgomery form.\nconst BASE: u64[] = [\n" + "\n".join(lines) + "\n];\n"

def check(fns):
    rnd = random.Random(1)
    rinv = pow(R, -1, P)
    rninv = pow(R, -1, N)
    edge = [0, 1, P - 1, P - 2, N - 1, 2**255, 2**64 - 1]
    for k in range(3000):
        a = edge[k % len(edge)] if k < 49 else rnd.randrange(P)
        b = edge[k // len(edge) % len(edge)] if k < 49 else rnd.randrange(P)
        assert run(fns["__fp_mul"], {"a": a, "b": b}) == a * b * rinv % P
        assert run(fns["__fp_add"], {"a": a, "b": b}) == (a + b) % P
        assert run(fns["__fp_sub"], {"a": a, "b": b}) == (a - b) % P
        assert run(fns["__borrow"], {"a": a, "b": b}) == (1 if a < b else 0)
        an, bn = a % N, b % N
        assert run(fns["__fn_mul"], {"a": an, "b": bn}) == an * bn * rninv % N
        assert run(fns["__fn_add"], {"a": an, "b": bn}) == (an + bn) % N
        # One operand below the modulus is enough for the product.
        assert run(fns["__fn_mul"], {"a": a, "b": bn}) == a * bn * rninv % N

def region():
    fns = {
        "__fp_mul": mont_mul("__fp_mul", P, n0_of(P), True),
        "__fp_add": mod_add("__fp_add", P),
        "__fp_sub": mod_sub("__fp_sub", P),
        "__fn_mul": mont_mul("__fn_mul", N, n0_of(N), False),
        "__fn_add": mod_add("__fn_add", N),
        "__borrow": borrow_fn(),
    }
    check(fns)
    parts = [fns[k] for k in ("__fp_mul", "__fp_add", "__fp_sub", "__fn_mul", "__fn_add", "__borrow")]
    parts.append(fe_fn("__fp_one", R % P, "R mod p, 1 in Montgomery form."))
    parts.append(fe_fn("__fp_r2", R * R % P, "R^2 mod p, which takes a plain value into Montgomery form."))
    parts.append(fe_fn("__fp_b", B * R % P, "The curve's b in Montgomery form."))
    parts.append(fe_fn("__n", N, "The group order n."))
    parts.append(fe_fn("__n_minus2", N - 2, "n - 2."))
    parts.append(fe_fn("__fn_one", R % N, "R mod n."))
    parts.append(fe_fn("__fn_r2", R * R % N, "R^2 mod n."))
    parts.append(base_table())
    return "\n".join(parts)

if __name__ == "__main__":
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "internal", "stdlib", "std", "crypto", "p256.fern")
    write_region(path, "P-256 ARITHMETIC", "gen-p256.py", region())
