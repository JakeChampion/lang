#!/usr/bin/env python3
"""Regenerate independent golden vectors for the audio codecs.

Requires cc and FFmpeg. The QOA reference is pinned and hash checked.
"""
import hashlib
import math
import os
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import urllib.request
import wave
import zlib

ROOT = Path(__file__).resolve().parent.parent
QOA_REV = 'bc0589710ff6aa2f50e08fb4d0a815d567bc0f3e'
QOA_SHA = '7b2396063eb960b10001db7db28ba25e284d4e772191968012e612dc8834952c'


def crc(data, width, poly):
    value = 0
    for b in data:
        value ^= b << (width - 8)
        for _ in range(8):
            value = ((value << 1) ^ (poly if value & (1 << (width - 1)) else 0)) & ((1 << width) - 1)
    return value


class Bits:
    def __init__(self):
        self.bits = ''

    def put(self, value, width):
        if width:
            self.bits += format(value & ((1 << width) - 1), f'0{width}b')

    def bytes(self):
        return int(self.bits.ljust((len(self.bits) + 7) // 8 * 8, '0'), 2).to_bytes((len(self.bits) + 7) // 8, 'big')


def subframe(values, depth, kind, order=0, wasted=0, rice2=False, partition=0, shift=0, coeff=None, escape=False, rice_bits=None):
    b = Bits()
    tag = {'constant': 0, 'verbatim': 1, 'fixed': 8 + order, 'lpc': 31 + order}[kind]
    b.put(tag * 2 + bool(wasted), 8)
    if wasted:
        b.put(1, wasted)
    values = [v >> wasted for v in values]
    depth -= wasted
    if kind == 'constant':
        b.put(values[0], depth)
    elif kind == 'verbatim':
        for v in values:
            b.put(v, depth)
    else:
        for v in values[:order]:
            b.put(v, depth)
        if coeff is None:
            coeff = [[], [1], [2, -1], [3, -3, 1], [4, -6, 4, -1]][order]
        if kind == 'lpc':
            b.put(3, 4)  # Four-bit coefficients.
            b.put(shift, 5)
            for c in coeff:
                b.put(c, 4)
        residuals = []
        for i in range(order, len(values)):
            pred = sum(coeff[j] * values[i - j - 1] for j in range(order))
            pred = pred >> shift if shift >= 0 else pred << -shift
            residuals.append(values[i] - pred)
        b.put(int(rice2), 2)
        b.put(partition, 4)
        width = 5 if rice2 else 4
        at = 0
        for p in range(1 << partition):
            count = len(values) // (1 << partition) - (order if p == 0 else 0)
            rs = residuals[at:at + count]
            at += count
            if escape:
                b.put((1 << width) - 1, width)
                raw = max((abs(v).bit_length() + 1 for v in rs), default=0)
                b.put(raw, 5)
                for v in rs:
                    b.put(v, raw)
            else:
                k = rice_bits if rice_bits is not None else (17 if rice2 else 3)
                b.put(k, width)
                for v in rs:
                    u = v * 2 if v >= 0 else -v * 2 - 1
                    b.put(1, (u >> k) + 1)
                    b.put(u, k)
    return b.bits


def native_flac(channels, depth, kinds, *, assignment=None, **options):
    n = len(channels[0])
    cc = len(channels)
    assignment = cc - 1 if assignment is None else assignment
    coded = channels
    depths = [depth] * cc
    if assignment >= 8:
        left, right = channels
        side = [a - b for a, b in zip(left, right)]
        if assignment == 8:
            coded, depths = [left, side], [depth, depth + 1]
        elif assignment == 9:
            coded, depths = [side, right], [depth + 1, depth]
        else:
            coded, depths = [[(a + b) >> 1 for a, b in zip(left, right)], side], [depth, depth + 1]
    header = bytes([255, 248, 112, assignment << 4, 0]) + (n - 1).to_bytes(2, 'big')
    header += bytes([crc(header, 8, 7)])
    body = Bits()
    for i, values in enumerate(coded):
        body.bits += subframe(values, depths[i], kinds[i], **options)
    frame = header + body.bytes()
    frame += crc(frame, 16, 0x8005).to_bytes(2, 'big')
    interleaved = [channels[c][i] for i in range(n) for c in range(cc)]
    raw = b''.join(s.to_bytes((depth + 7) // 8, 'little', signed=True) for s in interleaved)
    packed = (48000 << 44) | ((cc - 1) << 41) | ((depth - 1) << 36) | n
    info = struct.pack('>HH', n, n) + bytes(6) + packed.to_bytes(8, 'big') + hashlib.md5(raw).digest()
    return b'fLaC\x80\x00\x00\x22' + info + frame, interleaved


def frame_number(value):
    if value < 128:
        return bytes([value])
    assert value < 2048
    return bytes([0xc0 | (value >> 6), 0x80 | (value & 63)])


def chained_flac(variable, wrong_number=False, overlong=False):
    sizes = [128, 192 if variable else 128, 17]
    frames = []
    values = []
    for i, size in enumerate(sizes):
        number = sum(sizes[:i]) if variable else i
        if wrong_number and i == 1:
            number += 1
        encoded_number = b'\xc0\x80' if overlong and i == 0 else frame_number(number)
        header = bytes([255, 249 if variable else 248, 112, 0]) + encoded_number + (size - 1).to_bytes(2, 'big')
        header += bytes([crc(header, 8, 7)])
        source, samples = native_flac([[-3 + i] * size], 16, ['constant'])
        frame = header + source[50:-2]
        frame += crc(frame, 16, 0x8005).to_bytes(2, 'big')
        frames.append(frame)
        values.extend(samples)
    raw = b''.join(v.to_bytes(2, 'little', signed=True) for v in values)
    packed = (48000 << 44) | (15 << 36) | len(values)
    info = struct.pack('>HH', min(sizes[:-1]), max(sizes)) + bytes(6) + packed.to_bytes(8, 'big') + hashlib.md5(raw).digest()
    return b'fLaC\x80\x00\x00\x22' + info + b''.join(frames), values


def sample_signal(frames, cc):
    x = 42
    result = []
    for i in range(frames * cc):
        x = (x * 1664525 + 1013904223) & 0xffffffff
        # Mix noise, ramps, silence, and the signed extrema.
        v = (x >> 16) - 32768
        if i % 7 < 3:
            v = (i * 31 % 65536) - 32768
        if i % 11 == 0:
            v = 0
        result.append(v)
    return result


def pcm_crc(values):
    return zlib.crc32(b''.join(struct.pack('<i', v) for v in values))


def main():
    flacs = []
    invalid_flacs = []
    for assignment in [1, 8, 9, 10]:
        channels = [[-2147483648, 2147483647, -1, 0] * 8, [2147483647, -2147483648, 0, -1] * 8]
        data, values = native_flac(channels, 32, ['verbatim'] * 2, assignment=assignment)
        flacs.append((f'verbatim32-assignment{assignment}', data, values))
    for order in range(5):
        data, values = native_flac([[i * i - 21 for i in range(32)]], 16, ['fixed'], order=order, partition=2 if order <= 4 else 0)
        flacs.append((f'fixed{order}', data, values))
    for name, kind, depth, options in [
        ('constant4', 'constant', 4, {}),
        ('wasted-constant', 'constant', 16, {'wasted': 3}),
        ('wasted-verbatim', 'verbatim', 24, {'wasted': 4}),
        ('rice2', 'fixed', 24, {'order': 2, 'rice2': True}),
        ('escape', 'fixed', 16, {'order': 4, 'escape': True}),
        ('lpc', 'lpc', 24, {'order': 2, 'coeff': [2, -1], 'shift': 1}),
        ('lpc-negative-shift', 'lpc', 24, {'order': 1, 'coeff': [1], 'shift': -1}),
        ('lpc32', 'lpc', 16, {'order': 32, 'coeff': [0] * 32, 'escape': True}),
    ]:
        samples = [-3 if depth == 4 else -24 for _ in range(64)] if kind == 'constant' else [(i * 3 - 42) * 16 for i in range(64)]
        data, values = native_flac([samples], depth, [kind], **options)
        if name == "lpc-negative-shift":
            invalid_flacs.append(data.hex())
        else:
            flacs.append((name, data, values))
    for variable in [False, True]:
        data, values = chained_flac(variable)
        flacs.append(('variable-frames' if variable else 'fixed-frames', data, values))
    invalid_flacs.append(chained_flac(False, wrong_number=True)[0].hex())
    invalid_flacs.append(chained_flac(True, overlong=True)[0].hex())
    forbidden, _ = native_flac([[-2147483648] * 32], 32, ['fixed'], order=0, rice2=True, rice_bits=30)
    invalid_flacs.append(forbidden.hex())
    # Verify each valid synthetic FLAC file with an independent decoder.
    for name, data, values in flacs:
        packed = int.from_bytes(data[18:26], 'big')
        depth = ((packed >> 36) & 31) + 1
        decoded = subprocess.run(['ffmpeg', '-v', 'error', '-f', 'flac', '-i', 'pipe:0', '-f', 's32le', 'pipe:1'], input=data, capture_output=True, check=True).stdout
        actual = [v >> (32 - depth) for v in struct.unpack('<' + 'i' * (len(decoded) // 4), decoded)]
        assert actual == values, name
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        url = f'https://raw.githubusercontent.com/phoboslab/qoa/{QOA_REV}/qoa.h'
        header = urllib.request.urlopen(url).read()
        assert hashlib.sha256(header).hexdigest() == QOA_SHA
        (tmp / 'qoa.h').write_bytes(header)
        shutil.copy(ROOT / 'scripts/audio-reference/qoa.c', tmp / 'qoa.c')
        subprocess.run(['cc', '-O3', '-fwrapv', '-o', str(tmp / 'qoa'), str(tmp / 'qoa.c')], check=True)
        qoas = []
        for frames, cc in [(1, 1), (19, 2), (20, 8), (21, 2), (5121, 1)]:
            samples = sample_signal(frames, cc)
            path = tmp / 'input.wav'
            raw = struct.pack('<' + 'h' * len(samples), *samples)
            with wave.open(str(path), 'wb') as w:
                w.setnchannels(cc)
                w.setsampwidth(2)
                w.setframerate(48000)
                w.writeframes(raw)
            qoa = tmp / 'output.qoa'
            decoded = tmp / 'decoded.raw'
            subprocess.run([str(tmp / 'qoa'), str(path), str(qoa), str(decoded)], check=True)
            qoas.append((frames, cc, zlib.crc32(qoa.read_bytes()), zlib.crc32(decoded.read_bytes())))
        for depth in [16, 24]:
            for cc in [1, 2, 8]:
                n = 193
                values = [int(math.sin(i / cc * 0.13 + i % cc) * (1 << (depth - 2))) for i in range(n * cc)]
                path = tmp / 'input.wav'
                with wave.open(str(path), 'wb') as w:
                    w.setnchannels(cc)
                    w.setsampwidth(depth // 8)
                    w.setframerate(48000)
                    w.writeframes(b''.join(s.to_bytes(depth // 8, 'little', signed=True) for s in values))
                out = tmp / 'reference.flac'
                subprocess.run(['ffmpeg', '-v', 'error', '-y', '-i', str(path), '-c:a', 'flac', '-compression_level', '8', '-map_metadata', '-1', '-fflags', '+bitexact', '-flags:a', '+bitexact', '-metadata', 'encoder=', str(out)], check=True)
                data = out.read_bytes()
                # Remove the optional metadata and its padding.
                first = data[4:42]
                at = 4
                while True:
                    last = data[at] & 128
                    size = int.from_bytes(data[at + 1:at + 4], 'big')
                    at += 4 + size
                    if last:
                        break
                data = b'fLaC' + bytes([128]) + first[1:] + data[at:]
                flacs.append((f'ffmpeg-{depth}-{cc}', data, values))
    lines = ['// Generated by scripts/gen-audio-fixtures.py.\n',
             'pub struct Flac { name: string, bytes: string, count: i32, crc: u32 }\n',
             'pub struct Qoa { frames: i32, channels: i32, encoded_crc: u32, decoded_crc: u32 }\n',
             'pub function flacs(): Flac[] {\n  return [\n']
    for name, data, values in flacs:
        lines.append(f'    Flac {{ name: "{name}", bytes: "{data.hex()}", count: {len(values)}, crc: {pcm_crc(values)}u32 }},\n')
    lines.append('  ];\n}\n\npub function qoas(): Qoa[] {\n  return [\n')
    for frames, cc, encoded, decoded in qoas:
        lines.append(f'    Qoa {{ frames: {frames}, channels: {cc}, encoded_crc: {encoded}u32, decoded_crc: {decoded}u32 }},\n')
    lines.append('  ];\n}\n\npub function invalid_flacs(): string[] {\n  return [' + ', '.join('\"' + v + '\"' for v in invalid_flacs) + '];\n}\n')
    (ROOT / 'tests/stdlib/audio_vectors.fern').write_text(''.join(lines))
    compiler = os.environ.get('FERN') or subprocess.check_output([str(ROOT / 'bootstrap/bootstrap.sh'), 'stage0'], text=True).strip()
    subprocess.run([compiler, '-fmt', '-w', str(ROOT / 'tests/stdlib/audio_vectors.fern')], check=True)
    print(f'{len(flacs)} FLAC vectors and {len(qoas)} QOA vectors')


if __name__ == '__main__':
    main()
