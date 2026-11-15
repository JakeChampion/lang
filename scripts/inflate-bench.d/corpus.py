#!/usr/bin/env python3
# corpus.py REPO REV OUT: write the two inflate-bench corpora under OUT.
#
# small.bin holds 10000 zlib streams of git objects of REV and its history
# that are 100 B to 4 KiB long, as git stores them: 4000 commits, 3000 trees
# and 3000 blobs. large.bin holds one zlib stream of 48 MiB of the text
# files of REV and the bin/fern-selfhost executable. Each record is the
# stream length and the decoded length as little-endian u32, then the
# stream. zlib level 6, git's default, compresses both.
import os, random, struct, subprocess, sys, zlib

repo, rev, out = sys.argv[1], sys.argv[2], sys.argv[3]


def git(*args, data=None):
    return subprocess.run(['git', '-C', repo] + list(args), input=data, capture_output=True, check=True).stdout


def record(f, body):
    z = zlib.compress(body, 6)
    f.write(struct.pack('<II', len(z), len(body)))
    f.write(z)


rnd = random.Random(7)
hashes = [line.split(' ')[0] for line in git('rev-list', '--objects', '--max-count=20000', rev).decode().split('\n') if line]
listing = git('cat-file', '--batch-check=%(objectname) %(objecttype) %(objectsize)', data='\n'.join(hashes).encode() + b'\n')
picked = {'commit': [], 'tree': [], 'blob': []}
for line in listing.decode().split('\n'):
    if line:
        h, kind, size = line.split(' ')
        if 100 <= int(size) <= 4096:
            picked.setdefault(kind, []).append(h)
chosen = []
for kind, count in (('commit', 4000), ('tree', 3000), ('blob', 3000)):
    rnd.shuffle(picked[kind])
    chosen += picked[kind][:count]
rnd.shuffle(chosen)
raw = git('cat-file', '--batch', data='\n'.join(chosen).encode() + b'\n')
with open(os.path.join(out, 'small.bin'), 'wb') as f:
    at = 0
    while at < len(raw):
        nl = raw.index(b'\n', at)
        size = int(raw[at:nl].split(b' ')[2])
        record(f, raw[nl + 1:nl + 1 + size])
        at = nl + 2 + size
text = bytearray()
for path in sorted(git('ls-tree', '-r', '--name-only', rev).decode().split('\n')):
    if path.endswith(('.fern', '.go', '.md', '.c', '.h', '.txt', '.json')) and len(text) < 48 << 20:
        text += git('show', rev + ':' + path)
with open(os.path.join(repo, 'bin', 'fern-selfhost'), 'rb') as exe:
    body = bytes(text[:48 << 20]) + exe.read()
with open(os.path.join(out, 'large.bin'), 'wb') as f:
    record(f, body)
