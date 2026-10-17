# limbgen.py: the straight-line 64-bit limb arithmetic that scripts/gen-p256.py
# and scripts/gen-field25519.py emit as Fern, and an evaluator that runs what
# they emit on Python's integers, so each script checks its output before it
# writes it.
import re

M64 = 2**64 - 1

def limbs(v, n=4):
    return [(v >> (64 * i)) & M64 for i in range(n)]

def lit(v):
    return "0x%016xu64" % v

class Gen:
    def __init__(self):
        self.lines = []
        self.n = 0
    def fresh(self, base="v"):
        self.n += 1
        return "%s%d" % (base, self.n)
    def let(self, expr, base="v"):
        name = self.fresh(base)
        self.lines.append("  let %s: u64 = %s;" % (name, expr))
        return name
    # The sum and carry of x + y (+ c), the carry from the top bits: the
    # spelling ssa.fuse_carries reads off the flags.
    def add(self, x, y, c=None):
        s = self.let("%s + %s" % (x, y) if c is None else "%s + %s + %s" % (x, y, c), "s")
        k = self.let("(%s & %s | (%s | %s) & (%s ^ ALL)) >> 63u64" % (x, y, x, y, s), "c")
        return s, k
    def sub(self, x, y, b=None):
        d = self.let("%s - %s" % (x, y) if b is None else "%s - %s - %s" % (x, y, b), "d")
        k = self.let("((%s ^ ALL) & %s | (%s ^ ALL | %s) & %s) >> 63u64" % (x, y, x, y, d), "b")
        return d, k
    # (hi, lo) = x + y*z + c, each term below 2^64; x and c may be None.
    def mac(self, x, y, z, c):
        lo = self.let("%s * %s" % (y, z), "lo")
        hi = self.let("__mulhi_u64(%s, %s)" % (y, z), "hi")
        for t in (x, c):
            if t is not None:
                lo, k = self.add(lo, t)
                hi = self.let("%s + %s" % (hi, k), "hi")
        return hi, lo
    # params binds an Fe parameter's limbs to locals, so each is read once and
    # every use of a limb is the same value.
    def params(self, name):
        return [self.let("%s.l%d" % (name, i), name) for i in range(4)]
    def body(self):
        return "\n".join(self.lines)

def fe_return(out):
    return "  return Fe { l0: %s, l1: %s, l2: %s, l3: %s };" % tuple(out)

# run evaluates an emitted function: `fes` maps an Fe parameter's name to its
# value and `words` a u64 parameter's. It answers what the function returns.
def run(fn_src, fes, words=None):
    env = {"ALL": M64}
    for name, v in fes.items():
        for j in range(4):
            env["%s.l%d" % (name, j)] = limbs(v)[j]
    for name, v in (words or {}).items():
        env[name] = v
    ret = None
    for line in fn_src.splitlines():
        m = re.match(r"  let (\w+): u64 = (.*);$", line)
        if m:
            env[m.group(1)] = ev(m.group(2), env)
        m = re.match(r"  return Fe \{ l0: (.*), l1: (.*), l2: (.*), l3: (.*) \};$", line)
        if m:
            ret = sum(ev(m.group(k + 1), env) << (64 * k) for k in range(4))
        m = re.match(r"  return (\w+);$", line)
        if m:
            ret = env[m.group(1)]
    return ret

def ev(expr, env):
    e = re.sub(r"0x([0-9a-f]+)u64", r"0x\1", expr)
    e = re.sub(r"(\d+)u64", r"\1", e)
    e = e.replace("__mulhi_u64", "mulhi")
    e = re.sub(r"\b(\w+)\.(l\d)\b", r"\1_\2", e)
    names = {k.replace(".", "_"): v for k, v in env.items()}
    names["mulhi"] = lambda x, y: (x * y) >> 64
    # Python's precedence for these operators matches C's, and so Fern's.
    return eval(e, {}, names) & M64

# write_region replaces the text between the BEGIN and END markers named
# `title` in the file at `path` with `text`.
def write_region(path, title, script, text):
    src = open(path).read()
    begin = "// BEGIN GENERATED %s (scripts/%s): do not edit by hand.\n" % (title, script)
    end = "// END GENERATED %s\n" % title
    i = src.index(begin) + len(begin)
    j = src.index(end)
    out = src[:i] + text + src[j:]
    open(path, "w").write(out)
