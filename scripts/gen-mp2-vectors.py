#!/usr/bin/env python3
"""Freeze Layer II signals from FFmpeg and TwoLAME with independent float PCM."""
import argparse
import base64
import hashlib
import json
import math
from pathlib import Path
import struct
import subprocess
import tempfile
import zlib

ROOT = Path(__file__).resolve().parent.parent
REVISION = 'ea99364f61c14656440e8d77e9c233ccf3124633'
HEADER_SHA = '57e437c5c1f0e8b243885d3929c8973b5e6c778451e0100ab4251d19915cb3ad'


def packed(value):
    return base64.b64encode(zlib.compress(value, 9)).decode()


def frame_offsets(data):
    out, at = [], 0
    while at < len(data):
        assert len(data) - at >= 4 and data[at] == 255
        a, b, c = data[at + 1:at + 4]
        assert a & 6 == 4 and (a >> 3 & 3) in (2, 3)
        version = 1 if a & 8 else 2
        rate = [44100, 48000, 32000][b >> 2 & 3] // version
        kbps = ([0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384] if version == 1 else
                [0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160])[b >> 4]
        size = 144000 * kbps // rate + (b >> 1 & 1)
        assert 4 <= size <= len(data) - at
        out.append((at, size, rate, 1 if c >> 6 == 3 else 2, kbps, c >> 6, not bool(a & 1)))
        at += size
    return out


def signal(rate, channels, noise, silence=False):
    out = bytearray()
    for i in range(1499):
        for channel in range(channels):
            if silence:
                value = 0.0
            elif noise:
                word = ((i * 747796405 + channel * 2891336453 + 1234567) ^ (i * 13579 >> 3)) & 65535
                value = (word - 32768) * 0.7 / 32768
            else:
                value = 0.4 * math.sin(2 * math.pi * (317 + channel * 173) * i / rate)
                if i in (0, 701):
                    value = 0.9 if channel == 0 else -0.8
            out += struct.pack('<f', value)
    return out


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--minimp3', type=Path, required=True)
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--keep', type=Path)
    args = parser.parse_args()
    header = args.minimp3.read_bytes()
    assert hashlib.sha256(header).hexdigest() == HEADER_SHA
    version = subprocess.check_output(['ffmpeg', '-version'], text=True).splitlines()[0]
    rows, manifest = [], []
    smoke_row = None
    stream_row = None
    transition_row = None
    with tempfile.TemporaryDirectory() as directory:
        temp = Path(directory)
        (temp / 'minimp3.h').write_bytes(header)
        reference = temp / 'reference'
        subprocess.run(['cc', '-O3', '-ffp-contract=off', '-I', str(temp), str(ROOT / 'scripts/audio-reference/mp2.c'), '-o', str(reference)], check=True)
        for rate in [16000, 22050, 24000, 32000, 44100, 48000]:
            for mode in [0, 1, 2, 3]:
                channels = 1 if mode == 3 else 2
                for kind in ['tone', 'noise-low', 'silence']:
                    if kind == 'silence' and mode != 0:
                        continue
                    noise = kind == 'noise-low'
                    low = noise
                    kbps = (32 if channels == 1 else 64) if rate >= 32000 and low else (96 if channels == 1 else 192) if rate >= 32000 else (8 if channels == 1 else 16) if low else (32 if channels == 1 else 64)
                    for encoder in (['libtwolame', 'mp2'] if mode in (0, 3) and kind == 'tone' else ['libtwolame']):
                        name = f'{encoder}-{rate}-{mode}-' + kind
                        source = temp / 'input.f32'
                        source.write_bytes(signal(rate, channels, noise, kind == 'silence'))
                        encoded = temp / 'input.mp2'
                        command = ['ffmpeg', '-v', 'error', '-y', '-f', 'f32le', '-ar', str(rate), '-ac', str(channels), '-i', str(source), '-c:a', encoder, '-b:a', str(kbps * 1000)]
                        if encoder == 'libtwolame':
                            command += ['-mode', str(mode), '-error_protection', str(int(noise))]
                        command += ['-f', 'mp2', str(encoded)]
                        subprocess.run(command, check=True)
                        data = encoded.read_bytes()
                        frames = frame_offsets(data)
                        assert frames and all(row[2] == rate and row[3] == channels and row[4] == kbps for row in frames)
                        golden = temp / 'output.f32'
                        subprocess.run([str(reference), str(encoded), str(golden)], check=True)
                        pcm = golden.read_bytes()
                        assert len(pcm) == len(frames) * 1152 * channels * 4
                        fmt = struct.pack('<HHIIHHH', 0x50, channels, rate, kbps * 125, 1, 0, 0)
                        body = b'WAVEfmt ' + struct.pack('<I', len(fmt)) + fmt + b'data' + struct.pack('<I', len(data)) + data
                        wrapped = temp / 'input.wav'
                        wrapped.write_bytes(b'RIFF' + struct.pack('<I', len(body)) + body)
                        other = subprocess.check_output(['ffmpeg', '-v', 'error', '-xerror', '-err_detect', 'crccheck+explode', '-f', 'wav', '-c:a', 'mp2float', '-i', str(wrapped), '-f', 'f32le', '-c:a', 'pcm_f32le', '-'])
                        assert len(pcm) == len(other)
                        error = max(abs(a[0] - b[0]) for a, b in zip(struct.iter_unpack('<f', pcm), struct.iter_unpack('<f', other)))
                        assert error < 0.00003, (name, error)
                        rows.append(f'    Vector {{ name: "{name}", rate: {rate}, channels: {channels}, frames: {len(frames) * 1152}, encoded: "{packed(data)}", pcm: "{packed(pcm)}" }},\n')
                        manifest.append({'name': name, 'input_sha256': hashlib.sha256(data).hexdigest(), 'pcm_sha256': hashlib.sha256(pcm).hexdigest(), 'frames': len(frames), 'mode': frames[0][5], 'protected': frames[0][6], 'ffmpeg_max_abs': error})
                        if name == 'libtwolame-16000-3-noise-low':
                            stream_row = rows[-1].strip().removesuffix(',')
                            first = data[:frames[0][1]]
                            first_pcm = pcm[:1152 * channels * 4]
                            smoke_row = f'Vector {{ name: "{name}-first-frame", rate: {rate}, channels: {channels}, frames: 1152, encoded: "{packed(first)}", pcm: "{packed(first_pcm)}" }}'
                        if name == 'libtwolame-44100-0-tone':
                            transition_row = f'Vector {{ name: "{name}-first-frame", rate: {rate}, channels: {channels}, frames: 1152, encoded: "{packed(data[:frames[0][1]])}", pcm: "{packed(pcm[:1152 * channels * 4])}" }}'
                        print(name, len(frames), error, flush=True)
                        if args.keep:
                            args.keep.mkdir(parents=True, exist_ok=True)
                            (args.keep / (name + '.mp2')).write_bytes(data)
                            (args.keep / (name + '.f32')).write_bytes(pcm)
    prefix = '// Generated by scripts/gen-mp2-vectors.py.\n// Scalar minimp3 ' + REVISION + '.\n'
    prefix += 'pub struct Vector { name: string, rate: i32, channels: i32, frames: i32, encoded: string, pcm: string }\n\n'
    sources = {'mp2_vectors.fern': prefix + 'pub function vectors(): Vector[] {\n  return [\n' + ''.join(rows) + '  ];\n}\n',
               'mp2_smoke_vectors.fern': prefix + 'pub function vector(): Vector {\n  return ' + smoke_row + ';\n}\n\npub function stream(): Vector {\n  return ' + stream_row + ';\n}\n\npub function transition(): Vector {\n  return ' + transition_row + ';\n}\n'}
    for name, content in sources.items():
        path = ROOT / 'tests/stdlib' / name
        if args.check:
            assert path.read_text() == content, name + ' is stale'
        else:
            path.write_text(content)
    content = json.dumps({'minimp3_revision': REVISION, 'minimp3_sha256': HEADER_SHA, 'reference_source_sha256': hashlib.sha256((ROOT / 'scripts/audio-reference/mp2.c').read_bytes()).hexdigest(), 'ffmpeg': version, 'ffmpeg_input': 'RIFF/WAVE tag 0x50 with unchanged MPEG audio payload', 'vectors': manifest}, indent=2) + '\n'
    path = ROOT / 'scripts/audio-reference/mp2.json'
    if args.check:
        assert path.read_text() == content
    else:
        path.write_text(content)


if __name__ == '__main__':
    main()
