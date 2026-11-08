#!/usr/bin/env python3
"""Freeze independent FFmpeg AAC-LC packets and decoded float PCM."""
import argparse
import base64
import hashlib
import math
from pathlib import Path
import runpy
import shutil
import struct
import subprocess
import tempfile
import zlib

ROOT = Path(__file__).resolve().parent.parent
RATES = [96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350]


class Bits:
    def __init__(self):
        self.data = ''

    def put(self, value, width):
        assert 0 <= value < 1 << width
        self.data += format(value, f'0{width}b')

    def bytes(self):
        data = self.data + '0' * (-len(self.data) % 8)
        return int(data, 2).to_bytes(len(data) // 8, 'big')


def syntax_vectors(source_dir):
    """Write AAC syntax directly, then use FFmpeg as the sole PCM oracle."""
    tables = runpy.run_path(str(ROOT / 'scripts/gen-aac-tables.py'))
    source = tables['reference']('codebooks.rs', source_dir)
    books = {}
    for book in range(12):
        prefix = 'SCF_CODEBOOK' if book == 0 else f'SPECTRUM_CODEBOOK{book}'
        books[book] = (tables['array'](source, prefix + '_CODES'), tables['array'](source, prefix + '_LENS'))

    def word(bits, book, symbol):
        code, width = books[book]
        bits.put(code[symbol], width[symbol])

    def spectrum(bits, book):
        if book < 5:
            values = [-1, 0, 1, -1] if book < 3 else [-2, 1, 0, -2]
            symbol = 0
            for v in values:
                symbol = symbol * 3 + (v + 1 if book < 3 else abs(v))
            word(bits, book, symbol)
            if book >= 3:
                for v in values:
                    if v:
                        bits.put(int(v < 0), 1)
        else:
            radix = 9 if book < 7 else 8 if book < 9 else 13 if book < 11 else 17
            pairs = [(-4, 3), (1, -2)] if book < 7 else [(radix - 1, -(radix - 2)), (-2, 1)]
            if book == 11:
                pairs = [(16, -8191), (-32, 1)]
            for pair in pairs:
                values = [v + 4 for v in pair] if book < 7 else [min(abs(v), radix - 1) for v in pair]
                word(bits, book, values[0] * radix + values[1])
                if book >= 7:
                    for v in pair:
                        if v:
                            bits.put(int(v < 0), 1)
                if book == 11:
                    for v in pair:
                        v = abs(v)
                        if v >= 16:
                            width = v.bit_length() - 1
                            for _ in range(width - 4):
                                bits.put(1, 1)
                            bits.put(0, 1)
                            bits.put(v - (1 << width), width)

    for name, book, pulse, tns in [(f'codebook-{b}', b, False, None) for b in range(1, 12)] + [
            ('pulse-signed', 5, True, None), ('pulse-zero', 1, True, None),
            *[(f'tns-{resolution}-{compressed}-{reverse}', 5, False, (resolution, compressed, reverse))
              for resolution in [3, 4] for compressed in [0, 1] for reverse in [0, 1]]]:
        bits = Bits()
        bits.put(0, 3)  # Single-channel element.
        bits.put(0, 4)
        bits.put(100 if book == 11 else 180, 8)
        bits.put(0, 1)  # Reserved bit.
        bits.put(0, 2)  # Only-long window.
        bits.put(0, 1)  # Sine window.
        bits.put(1, 6)  # One scalefactor band.
        bits.put(0, 1)  # No prediction.
        bits.put(book, 4)
        bits.put(1, 5)
        word(bits, 0, 60)  # No scalefactor delta.
        bits.put(int(pulse), 1)
        if pulse:
            bits.put(1, 2)  # Two pulses.
            bits.put(0, 6)
            for offset, amplitude in [(1, 3), (1, 2)]:
                bits.put(offset, 5)
                bits.put(amplitude, 4)
        bits.put(int(tns is not None), 1)
        if tns:
            resolution, compressed, reverse = tns
            bits.put(1, 2)
            bits.put(resolution - 3, 1)
            bits.put(49, 6)  # Filter the complete first band.
            bits.put(3, 5)
            bits.put(reverse, 1)
            bits.put(compressed, 1)
            for coefficient in [1, 2, 1]:
                bits.put(coefficient, resolution - compressed)
        bits.put(0, 1)  # No gain control.
        spectrum(bits, book)
        bits.put(7, 3)
        payload = bits.bytes()
        size = len(payload) + 7
        header = bytes([255, 241, 80, 64 | size >> 11, size >> 3 & 255, (size & 7) << 5 | 31, 252])
        yield name, header + payload


def run(args):
    subprocess.run(['ffmpeg', '-v', 'error', '-nostdin', '-y', *args], check=True)


def signal(channels, frames, mode):
    state = 12345
    values = []
    for i in range(frames):
        for channel in range(channels):
            state = (state * 1664525 + 1013904223) & 0xffffffff
            tone = .3 * math.sin(i * (.12 + channel * .017)) + .1 * math.sin(i * .73)
            noise = ((state >> 16) / 32768 - 1) * .2
            value = tone
            if mode == 'transient':
                value = noise if i % 557 < 29 else tone * .04
            elif mode == 'noise':
                value = noise
            elif mode == 'correlated':
                value = .25 * math.sin(i * .13) + .12 * math.sin(i * .81)
            elif mode == 'silence':
                value = 0
            values.append(value)
    return struct.pack('<%df' % len(values), *values)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--keep', type=Path)
    parser.add_argument('--source-dir', type=Path)
    args = parser.parse_args()
    version = subprocess.check_output(['ffmpeg', '-version'], text=True).splitlines()[0]
    cases = [(f'mono-{rate}', rate, 1, 'tone', False, False) for rate in RATES]
    cases += [(f'layout-{channels}', 48000, channels, 'tone', False, False) for channels in [2, 3, 4, 5, 6, 8]]
    cases += [('short-transients', 44100, 2, 'transient', False, True),
              ('intensity-stereo', 44100, 2, 'correlated', False, False),
              ('noise-substitution', 48000, 2, 'noise', True, True),
              ('silence', 44100, 1, 'silence', False, False)]
    text = '// Generated by scripts/gen-aac-vectors.py.\n'
    text += '// Independent encoder and decoder: ' + version + '\n'
    binary = Path(shutil.which('ffmpeg')).resolve()
    text += '// FFmpeg executable SHA-256: ' + hashlib.sha256(binary.read_bytes()).hexdigest() + '\n'
    text += 'pub struct Vector { name: string, rate: i32, channels: i32, count: i32, bytes: string, pcm: string }\n\n'
    smoke = text
    text += 'pub function vectors(): Vector[] {\n  return [\n'
    with tempfile.TemporaryDirectory() as directory:
        temp = Path(directory)
        def freeze(name, rate, channels, packet):
            nonlocal text, smoke
            (temp / 'packet.aac').write_bytes(packet)
            run(['-i', str(temp / 'packet.aac'), '-c:a', 'pcm_f32le', '-f', 'f32le', str(temp / 'output.f32')])
            pcm = (temp / 'output.f32').read_bytes()
            assert pcm, name
            encoded = base64.b64encode(zlib.compress(packet, 9)).decode()
            reference = base64.b64encode(zlib.compress(pcm, 9)).decode()
            row = f'    Vector {{ name: "{name}", rate: {rate}, channels: {channels}, count: {len(pcm) // 4}, bytes: "{encoded}", pcm: "{reference}" }}'
            text += row + ',\n'
            if name == 'codebook-1':
                smoke += 'pub function vector(): Vector {\n  return ' + row.strip() + ';\n}\n'
            print(name, 'input', len(packet), 'PCM samples', len(pcm) // 4)
            if args.keep:
                args.keep.mkdir(parents=True, exist_ok=True)
                (args.keep / (name + '.aac')).write_bytes(packet)
                (args.keep / (name + '.f32')).write_bytes(pcm)

        for name, rate, channels, mode, pns, tns in cases:
            (temp / 'input.f32').write_bytes(signal(channels, 2048, mode))
            bitrate = min(rate * channels * 2, 64000 * channels)
            if name == 'intensity-stereo':
                bitrate = 24000
            layout = {1: 'mono', 2: 'stereo', 3: '3.0', 4: '4.0', 5: '5.0', 6: '5.1', 8: '7.1'}[channels]
            run(['-f', 'f32le', '-ar', str(rate), '-ac', str(channels), '-channel_layout', layout, '-i', str(temp / 'input.f32'),
                 '-c:a', 'aac', '-b:a', str(bitrate), '-aac_pns', str(int(pns)), '-aac_tns', str(int(tns)),
                 '-map_metadata', '-1', '-fflags', '+bitexact', '-flags:a', '+bitexact', '-f', 'adts', str(temp / 'packet.aac')])
            packet = (temp / 'packet.aac').read_bytes()
            freeze(name, rate, channels, packet)
        for name, packet in syntax_vectors(args.source_dir):
            freeze(name, 44100, 1, packet)
    text += '  ];\n}\n'
    target = ROOT / 'tests/stdlib/aac_vectors.fern'
    if args.check:
        if not target.exists() or target.read_text() != text:
            parser.exit(1, 'AAC vectors are stale; run scripts/gen-aac-vectors.py\n')
    else:
        target.write_text(text)
    target = ROOT / 'tests/stdlib/aac_smoke_vectors.fern'
    if args.check:
        if not target.exists() or target.read_text() != smoke:
            parser.exit(1, 'AAC smoke vectors are stale; run scripts/gen-aac-vectors.py\n')
    else:
        target.write_text(smoke)


if __name__ == '__main__':
    main()
