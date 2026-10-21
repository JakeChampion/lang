#!/usr/bin/env python3
# gen-huffman-table.py OUT — the decoding states of std/http/huffman
# (HUFF_DEC) from its code tables, checked against a bit-by-bit decoder and
# the packed form the Fern decoder reads, and the encoder's packed table
# (HUFF, `code << 8 | length`). Paste OUT's entries into HUFF_DEC, sixteen
# a line, and the printed HUFF entries into HUFF, eight a line.
#
# gen-huffman-table.py --check — fail if the committed HUFF or HUFF_DEC has
# drifted from what the authored HUFF_CODE and HUFF_LEN generate, the mode
# make huffman-table-check runs in the lint lane.
import re, random, sys
import os
src = open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "internal", "stdlib", "std", "http", "huffman.fern")).read()
def arr(name):
    m = re.search(r"const " + name + r": [^=]*= \[(.*?)\];", src, re.S)
    return [int(x) for x in re.findall(r"\d+", m.group(1))]
LEN = arr("HUFF_LEN"); CODE = arr("HUFF_CODE")
class N:
    __slots__ = ("kids", "sym")
    def __init__(s): s.kids = [None, None]; s.sym = -1
root = N()
for sym in range(257):
    n = root
    for i in range(LEN[sym] - 1, -1, -1):
        bit = (CODE[sym] >> i) & 1
        if n.kids[bit] is None: n.kids[bit] = N()
        n = n.kids[bit]
    n.sym = sym
EMIT, ACCEPT, FAIL = 1, 2, 4
ids = {id(root): 0}; nodes = [root]; paths = {0: ""}
table = {}
queue = [0]
def is_accept(path):
    return len(path) <= 7 and all(c == "1" for c in path)
while queue:
    s = queue.pop(0)
    n = nodes[s]
    for nib in range(16):
        cur = n; path = paths[s]; sym = -1; flags = 0; failed = False
        for i in range(3, -1, -1):
            bit = (nib >> i) & 1
            nxt = cur.kids[bit]
            if nxt is None:
                failed = True; break
            path = path + str(bit)
            if nxt.sym >= 0:
                if nxt.sym == 256:
                    failed = True; break
                sym = nxt.sym; flags |= EMIT
                cur = root; path = ""
            else:
                cur = nxt
        if failed:
            table[(s, nib)] = (0, FAIL, 0); continue
        if id(cur) not in ids:
            ids[id(cur)] = len(nodes); nodes.append(cur); paths[len(nodes)-1] = path; queue.append(len(nodes)-1)
        ns = ids[id(cur)]
        if is_accept(path): flags |= ACCEPT
        table[(s, nib)] = (ns, flags, sym if sym >= 0 else 0)
def decode(coded):
    out = []; state = 0; acc = ACCEPT
    for b in coded:
        for nib in (b >> 4, b & 15):
            ns, flags, sym = table[(state, nib)]
            if flags & FAIL: return None
            if flags & EMIT: out.append(sym)
            state = ns; acc = flags & ACCEPT
    if not acc: return None
    return bytes(out)
def encode(bs):
    acc = 0; n = 0; out = []
    for b in bs:
        acc = (acc << LEN[b]) | CODE[b]; n += LEN[b]
        while n >= 8:
            n -= 8; out.append((acc >> n) & 255)
    if n > 0:
        acc = (acc << (8 - n)) | ((1 << (8 - n)) - 1); out.append(acc & 255)
    return bytes(out)
print("states", len(nodes))
print("www:", decode(bytes.fromhex("f1e3c2e5f23a6ba0ab90f4ff")))
print("no-cache:", decode(bytes.fromhex("a8eb10649cbf")))
print("1fff:", decode(bytes.fromhex("1fff")))
print("ffffffff:", decode(bytes([0xff]*4)))
print("07:", decode(bytes([7])))
random.seed(1)
bad = 0
for t in range(20000):
    s = bytes(random.randrange(256) for _ in range(random.randrange(0, 40)))
    if decode(encode(s)) != s: bad += 1
print("roundtrip failures:", bad)
# Packed as the Fern decoder reads it: the symbol in the low byte, the
# flags in bits 8 to 10, the next state above them.
vals = []
for s in range(len(nodes)):
    for nib in range(16):
        ns, flags, sym = table[(s, nib)]
        vals.append((ns << 11) | (flags << 8) | sym)
def decode_packed(coded):
    out = []; state = 0; entry = ACCEPT << 8
    for b in coded:
        for nib in (b >> 4, b & 15):
            entry = vals[state * 16 + nib]
            if entry & (FAIL << 8): return None
            if entry & (EMIT << 8): out.append(entry & 255)
            state = entry >> 11
    if not (entry & (ACCEPT << 8)): return None
    return bytes(out)
bad = 0
for t in range(20000):
    s = bytes(random.randrange(256) for _ in range(random.randrange(0, 40)))
    if decode_packed(encode(s)) != s: bad += 1
assert decode_packed(bytes.fromhex("1fff")) is None and decode_packed(bytes([0xff] * 4)) is None
print("packed roundtrip failures:", bad)
# HUFF: each symbol's code and length as code << 8 | length, the encoder's
# one indexed load per symbol.
huff = [(CODE[s] << 8) | LEN[s] for s in range(len(LEN))]
if "--check" in sys.argv[1:]:
    # The committed HUFF and HUFF_DEC must be what the authored HUFF_CODE and
    # HUFF_LEN generate; a drift fails here, where make huffman-table-check
    # runs it, rather than later as a failing HPACK round trip.
    stale = [name for name, want in (("HUFF", huff), ("HUFF_DEC", vals)) if arr(name) != want]
    if stale:
        print("internal/stdlib/std/http/huffman.fern is out of date: "
              + ", ".join(stale) + "; regenerate with scripts/gen-huffman-table.py OUT "
              "and paste OUT's entries into HUFF_DEC, the printed entries into HUFF", file=sys.stderr)
        sys.exit(1)
    print("huffman tables up to date")
    sys.exit(0)
if len(sys.argv) < 2:
    print("usage: gen-huffman-table.py OUT | gen-huffman-table.py --check", file=sys.stderr)
    sys.exit(2)
open(sys.argv[1], "w").write(",".join(str(v) for v in vals))
print("entries", len(vals), "max", max(vals))
print("HUFF entries", len(huff), "max", max(huff))
for i in range(0, len(huff), 8):
    print("  " + ", ".join(str(v) for v in huff[i:i + 8]) + ",")
