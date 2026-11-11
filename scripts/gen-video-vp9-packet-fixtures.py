#!/usr/bin/env python3
"""Generate VP9 packet ranges with an independent FFmpeg splitter."""
import importlib.util
import hashlib
import os
from pathlib import Path
import struct
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('vp9_headers', ROOT / 'scripts/gen-video-vp9-fixtures.py')
headers = importlib.util.module_from_spec(spec)
spec.loader.exec_module(headers)


def packets(data):
    at = 32
    result = []
    while at < len(data):
        size, timestamp = struct.unpack_from('<IQ', data, at)
        assert size > 0 and at + 12 + size <= len(data)
        result.append((timestamp, data[at + 12:at + 12 + size]))
        at += 12 + size
    assert at == len(data)
    return result


def generate(directory):
    path = directory / 'altref.ivf'
    command = ['ffmpeg', '-v', 'error', '-y', '-f', 'lavfi', '-i', 'testsrc2=size=64x36:rate=30',
               '-frames:v', '90', '-c:v', 'libvpx-vp9', '-threads', '1', '-cpu-used', '4',
               '-b:v', '100k', '-g', '60', '-lag-in-frames', '25', '-auto-alt-ref', '1',
               '-passlogfile', str(directory / 'encoder-pass')]
    subprocess.run(command + ['-pass', '1', '-f', 'null', os.devnull], check=True)
    subprocess.run(command + ['-pass', '2', '-f', 'ivf', str(path)], check=True)
    values = packets(path.read_bytes())
    indexed = [(t, p) for t, p in values if p[-1] & 224 == 192]
    assert indexed and all((p[-1] & 7) + 1 >= 2 for _, p in indexed)
    return path, [('plain-keyframe', values[0][1])] + [(f'altref-{t}', p) for t, p in indexed]


def indexed(payloads, width, trailing=b''):
    marker = 192 + (width - 1) * 8 + len(payloads) - 1
    return (b''.join(payloads) + trailing + bytes([marker])
            + b''.join(len(p).to_bytes(width, 'little') for p in payloads) + bytes([marker]))


def controls():
    result = []
    for width in range(1, 5):
        for count in range(1, 9):
            payloads = [bytes([130]) * (i + 1) for i in range(count)]
            result.append((f'width-{width}-count-{count}', indexed(payloads, width)))
    result.append(('unindexed-tail', indexed([b'\x82\x00', b'\x82\x01\x02'], 4, b'\x63\x64\x65')))
    return result


def reference(directory, name, data):
    source = directory / (name + '.ivf')
    source.write_bytes(headers.ivf(data))
    result = subprocess.run(['ffmpeg', '-v', 'error', '-y', '-i', str(source), '-c:v', 'copy', '-copyinkf',
                             '-bsf:v', 'vp9_superframe_split', '-f', 'framehash', '-'],
                            capture_output=True, text=True)
    assert result.returncode == 0 and 'Invalid frame size' not in result.stderr, result.stderr
    rows = [line.split(',') for line in result.stdout.splitlines() if line and not line.startswith('#')]
    assert rows, (name, result.stderr)
    offset, offsets, payloads = 0, [], []
    for row in rows:
        size = int(row[4])
        p = data[offset:offset + size]
        assert len(p) == size and hashlib.sha256(p).hexdigest() == row[5].strip(), name
        offsets.append(offset)
        payloads.append(p)
        offset += size
    marker = data[-1]
    index_offset = len(data) - 2 - ((marker & 7) + 1) * (((marker >> 3) & 3) + 1) if marker & 224 == 192 else len(data)
    assert offset <= index_offset, (name, offset, index_offset, [len(p) for p in payloads], result.stderr)
    return offsets, [len(p) for p in payloads], index_offset, index_offset - offset, payloads


def main():
    lines = ['// Generated with the FFmpeg VP9 superframe splitter.',
             'pub struct Vector { name: string, bytes: string, offsets: i32[], sizes: i32[], index_offset: i32, trailing: i32 }',
             'pub function vectors(): Vector[] {', '  return [']
    with tempfile.TemporaryDirectory() as temporary:
        directory = Path(temporary)
        _, cases = generate(directory)
        for name, data in cases + controls():
            offsets, sizes, start, trailing, _ = reference(directory, name, data)
            lines.append(f'    Vector {{ name: "{name}", bytes: "{data.hex()}", offsets: {offsets}, sizes: {sizes}, index_offset: {start}, trailing: {trailing} }},')
    lines += ['  ];', '}']
    target = ROOT / 'tests/stdlib/video_vp9_packet_vectors.fern'
    target.write_text('\n'.join(lines) + '\n')
    compiler = os.environ.get('FERN') or subprocess.check_output([str(ROOT / 'bootstrap/bootstrap.sh'), 'stage0'], text=True).strip()
    subprocess.run([compiler, '-fmt', '-w', str(target)], check=True)
    print('Wrote', target.relative_to(ROOT))


if __name__ == '__main__':
    main()
