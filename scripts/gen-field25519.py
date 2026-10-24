#!/usr/bin/env python3
# gen-field25519.py: the generated region of std/crypto/field25519, the limb
# arithmetic mod p = 2^255 - 19 as straight-line code over four 64-bit limbs.
# A value is any 256-bit number, reduced mod p only by `__canonical`: since
# 2^256 = 38 mod p, a carry out of the top limb folds back in as 38. Run it
# after changing anything below; it checks what it emits against Python's
# integers before rewriting the region.
import os, random, sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from limbgen import M64, Gen, fe_return, limbs, lit, run, write_region

P = 2**255 - 19
W = 2**256

# fold adds 38 * top to the limbs r, top below 2^58, then 38 again for a
# carry out of the top limb, which leaves r below 38 * 2^58 and so cannot
# carry a second time.
def fold(g, r, top):
    k = g.let("%s * 38u64" % top, "f")
    out = []
    for j in range(4):
        s, k = g.add(r[j], k)
        out.append(s)
    out[0] = g.let("%s + %s * 38u64" % (out[0], k), "s")
    return out

def mul():
    g = Gen()
    a = g.params("a")
    b = g.params("b")
    t = [None] * 8
    for i in range(4):
        c = None
        for j in range(4):
            c, t[i + j] = g.mac(t[i + j], a[i], b[j], c)
        t[i + 4] = c
    return finish_wide(g, "mul", "a: Fe, b: Fe", t, "the product a * b")

def sq():
    g = Gen()
    a = g.params("a")
    # The cross products once, doubled, then the squares on the diagonal.
    t = [None] * 8
    for i in range(4):
        c = None
        for j in range(i + 1, 4):
            c, t[i + j] = g.mac(t[i + j], a[i], a[j], c)
        if c is not None:
            t[i + 4] = c
    t = [x if x is not None else "0u64" for x in t]
    d = []
    k = None
    for j in range(8):
        s, k = g.add(t[j], t[j], k)
        d.append(s)
    c = None
    out = []
    for i in range(4):
        hi, lo = g.mac(d[2 * i], a[i], a[i], c)
        s, k2 = g.add(d[2 * i + 1], hi)
        out += [lo, s]
        c = k2
    return finish_wide(g, "sq", "a: Fe", out, "a squared")

# finish_wide reduces the eight limbs t: t[0..3] + 38 * t[4..7], then the
# top folded.
def finish_wide(g, name, params, t, what):
    c = None
    r = []
    for j in range(4):
        c, x = g.mac(t[j], t[4 + j], "38u64", c)
        r.append(x)
    out = fold(g, r, c)
    return "// %s returns a field representation of %s with a value less than `2^256`.\npub function %s(%s): Fe {\n%s\n%s\n}\n" % (name, what, name, params, g.body(), fe_return(out))

def add():
    g = Gen()
    a = g.params("a")
    b = g.params("b")
    r = []
    k = None
    for j in range(4):
        s, k = g.add(a[j], b[j], k)
        r.append(s)
    out = fold(g, r, k)
    return "// add returns a field representation of `a + b` with a value less than `2^256`.\npub function add(a: Fe, b: Fe): Fe {\n%s\n%s\n}\n" % (g.body(), fe_return(out))

def sub():
    g = Gen()
    a = g.params("a")
    b = g.params("b")
    d = []
    bo = None
    for j in range(4):
        x, bo = g.sub(a[j], b[j], bo)
        d.append(x)
    # A borrow out of the top limb took 2^256 = 38 too many: take 38 off,
    # and 38 again for a second borrow, which leaves the top limbs all ones
    # and cannot borrow a third time.
    k = g.let("%s * 38u64" % bo, "f")
    out = []
    for j in range(4):
        x, k = g.sub(d[j], k)
        out.append(x)
    out[0] = g.let("%s - %s * 38u64" % (out[0], k), "d")
    return "// sub returns a field representation of `a - b` with a value less than `2^256`.\npub function sub(a: Fe, b: Fe): Fe {\n%s\n%s\n}\n" % (g.body(), fe_return(out))

def mul_small():
    g = Gen()
    a = g.params("a")
    r = []
    c = None
    for j in range(4):
        c, x = g.mac(None, a[j], "k", c)
        r.append(x)
    out = fold(g, r, c)
    return "// mul_small returns a field representation of `a * k` with a value less than `2^256`.\n// `k` must be less than `2^32`.\npub function mul_small(a: Fe, k: u64): Fe {\n%s\n%s\n}\n" % (g.body(), fe_return(out))

def canonical():
    g = Gen()
    # Bit 255 is 2^255 = 19 mod p: fold it, leaving a value below
    # 2^255 + 19, then take p off when the value plus 19 reaches 2^255.
    a = g.params("a")
    top = g.let("%s >> 63u64" % a[3], "t")
    k = g.let("%s * 19u64" % top, "f")
    v = []
    lo = [a[0], a[1], a[2], g.let("%s & 0x7fffffffffffffffu64" % a[3], "a")]
    for j in range(4):
        s, k = g.add(lo[j], k)
        v.append(s)
    w = []
    k = "19u64"
    for j in range(4):
        s, k = g.add(v[j], k)
        w.append(s)
    q = g.let("0u64 - (%s >> 63u64)" % w[3], "q")
    w[3] = g.let("%s & 0x7fffffffffffffffu64" % w[3], "w")
    out = ["%s ^ (%s ^ %s) & %s" % (v[j], v[j], w[j], q) for j in range(4)]
    return "// __canonical returns `a mod p` as a value less than `p`.\nfunction __canonical(a: Fe): Fe {\n%s\n%s\n}\n" % (g.body(), fe_return(out))

def check(fns):
    rnd = random.Random(1)
    edge = [0, 1, 2, 19, 38, P - 1, P, P + 1, 2**255 - 1, 2**255, W - 39, W - 38, W - 1, M64, 2**64]
    for k in range(1500):
        a = edge[k % len(edge)] if k < len(edge) ** 2 else rnd.randrange(W)
        b = edge[k // len(edge) % len(edge)] if k < len(edge) ** 2 else rnd.randrange(W)
        for name, want in (("mul", a * b), ("add", a + b), ("sub", a - b)):
            got = run(fns[name], {"a": a, "b": b})
            assert got < W and (got - want) % P == 0, (name, a, b)
        got = run(fns["sq"], {"a": a})
        assert got < W and (got - a * a) % P == 0, ("sq", a)
        for small in (0, 1, 121665, 2**32 - 1):
            got = run(fns["mul_small"], {"a": a}, {"k": small})
            assert got < W and (got - a * small) % P == 0, ("mul_small", a, small)
        assert run(fns["__canonical"], {"a": a}) == a % P, ("canonical", a)

def region():
    fns = {"mul": mul(), "sq": sq(), "add": add(), "sub": sub(), "mul_small": mul_small(), "__canonical": canonical()}
    check(fns)
    return "\n".join(fns[k] for k in ("mul", "sq", "add", "sub", "mul_small", "__canonical"))

if __name__ == "__main__":
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "internal", "stdlib", "std", "crypto", "field25519.fern")
    write_region(path, "FIELD25519 ARITHMETIC", "gen-field25519.py", region())
