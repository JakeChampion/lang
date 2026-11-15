#!/usr/bin/env python3
# differ.py DIFFER DIR SEED N: a differential of std/deflate against zlib.
#
# It writes N raw DEFLATE streams to DIR/cases.bin: zlib streams of random
# and repeated data at several levels and strategies, a third of them
# damaged. DIFFER, built from differ.fern, decodes each with `inflate` and
# with the resumable reader. Each verdict must agree with zlib on ok and
# error, and each ok must give the bytes that zlib gives.
import os, random, struct, subprocess, sys, zlib

differ, outdir, seed, n = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
rnd = random.Random(seed)
words = [b'the ', b'of ', b'and ', b'deflate ', b'inflate ', b'tree ', b'blob ', b'\x00\x00\x00', b'\xff', b'0123456789']


def sample():
    k = rnd.randrange(6)
    size = rnd.choice([0, 1, 2, 3, 10, 100, 258, 300, 1000, 4096, 5000, 40000, 70000])
    if k == 0:
        return bytes(rnd.randrange(256) for _ in range(size))
    if k == 1:
        return b''.join(rnd.choice(words) for _ in range(size // 4 + 1))[:size]
    if k == 2:
        return bytes([rnd.randrange(4)]) * size
    if k == 3:
        base = bytes(rnd.randrange(256) for _ in range(rnd.randrange(1, 40)))
        return (base * (size // len(base) + 1))[:size]
    if k == 4:
        return bytes(rnd.randrange(3) * 50 + rnd.randrange(2) for _ in range(size))
    return bytes(rnd.randrange(256) for _ in range(size // 3)) + b'a' * (size // 3) + bytes(range(256)) * (size // 768)


def compress(data):
    level = rnd.choice([0, 1, 2, 6, 9])
    strategy = rnd.choice([zlib.Z_DEFAULT_STRATEGY, zlib.Z_FIXED, zlib.Z_HUFFMAN_ONLY, zlib.Z_RLE, zlib.Z_FILTERED])
    c = zlib.compressobj(level, zlib.DEFLATED, -15, rnd.choice([1, 8, 9]), strategy)
    if rnd.randrange(4) == 0 and len(data) > 10:
        cut = rnd.randrange(len(data))
        return c.compress(data[:cut]) + c.flush(zlib.Z_FULL_FLUSH) + c.compress(data[cut:]) + c.flush()
    return c.compress(data) + c.flush()


def corrupt(z):
    z = bytearray(z)
    k = rnd.randrange(5)
    if k == 0 and z:
        for _ in range(rnd.randrange(1, 4)):
            z[rnd.randrange(len(z))] ^= 1 << rnd.randrange(8)
    elif k == 1 and z:
        z = z[:rnd.randrange(len(z))]
    elif k == 2:
        z = bytearray(rnd.randrange(256) for _ in range(rnd.randrange(1, 64)))
    elif k == 3 and z:
        z[rnd.randrange(len(z))] = rnd.randrange(256)
    else:
        z += bytes(rnd.randrange(256) for _ in range(rnd.randrange(1, 8)))
    return bytes(z)


def verdict(z):
    d = zlib.decompressobj(-15)
    try:
        got = d.decompress(z, 1 << 26)
    except zlib.error:
        return 'err'
    if not d.eof:
        return 'err'
    return 'ok %d %d' % (len(got), zlib.crc32(got))


cases = os.path.join(outdir, 'cases.bin')
want = []
with open(cases, 'wb') as f:
    for i in range(n):
        z = compress(sample())
        if rnd.randrange(3) == 0:
            z = corrupt(z)
        f.write(struct.pack('<I', len(z)))
        f.write(z)
        want.append(verdict(z))
bad = 0
for mode in ([], ['reader']):
    got = subprocess.run([differ, cases] + mode, capture_output=True, text=True, check=True).stdout.split('\n')
    for i, w in enumerate(want):
        g = got[i] if i < len(got) else ''
        if (w == 'err' and g.startswith('err ')) or w == g:
            continue
        bad += 1
        if bad <= 10:
            print('case %d (%s): zlib %s, std/deflate %s' % (i, mode[0] if mode else 'inflate', w, g))
oks = sum(1 for w in want if w != 'err')
print('differ: seed %d, %d streams, %d ok and %d errors in zlib, %d disagreements' % (seed, n, oks, n - oks, bad))
sys.exit(1 if bad else 0)
