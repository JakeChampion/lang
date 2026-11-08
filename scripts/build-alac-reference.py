#!/usr/bin/env python3
"""Build the pinned Apple ALAC reference for independent packet fixtures."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import urllib.request

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-dir', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--cxx', default='g++')
    args = parser.parse_args()
    manifest = json.loads((ROOT / 'scripts/audio-reference/alac-source.json').read_text())
    for row in manifest['files']:
        path = args.source_dir / row['path']
        if not path.exists():
            url = 'https://raw.githubusercontent.com/macosforge/alac/' + manifest['commit'] + '/' + row['path']
            data = urllib.request.urlopen(url, timeout=60).read()
        else:
            data = path.read_bytes()
        digest = hashlib.sha1(b'blob ' + str(len(data)).encode() + b'\0' + data).hexdigest()
        if digest != row['sha']:
            parser.exit(1, 'source hash mismatch: ' + row['path'] + '\n')
        if not path.exists():
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
    codec = args.source_dir / 'codec'
    names = ['ALACDecoder.cpp', 'ALACBitUtilities.c', 'EndianPortable.c', 'ag_dec.c', 'dp_dec.c', 'matrix_dec.c']
    args.output.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run([args.cxx, '-O2', '-fwrapv', '-fno-strict-aliasing', '-I', str(codec),
                    str(ROOT / 'scripts/audio-reference/alac-packets.cpp'),
                    *[str(codec / name) for name in names], '-o', str(args.output)], check=True)
    print('Apple ALAC', manifest['commit'])
    print('reference SHA256', hashlib.sha256(args.output.read_bytes()).hexdigest())


if __name__ == '__main__':
    main()
