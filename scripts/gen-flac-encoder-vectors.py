#!/usr/bin/env python3
"""Qualify native FLAC output with FFmpeg, libFLAC, and its PCM MD5."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import random
import runpy
import struct
import subprocess
import tempfile
import zlib

ROOT = Path(__file__).resolve().parent.parent
INDEPENDENT = runpy.run_path(str(ROOT / 'scripts/audio-reference/flac-independent.py'))['decode']


def packed(value):
    return base64.b64encode(zlib.compress(value, 9)).decode()


def signal(depth, channels, frames, seed):
    rng = random.Random(seed)
    low, high = -(1 << (depth - 1)), (1 << (depth - 1)) - 1
    output = []
    for at in range(frames):
        for channel in range(channels):
            if at == 0:
                value = low
            elif at == 1:
                value = high
            elif seed % 3 == 0:
                value = rng.randint(low, high)
            else:
                value = (((at * at * (channel + 1) + channel * 17) % 13) - 6) << max(depth - 5, 0)
            output.append(value)
    return output


def cases():
    rows = []
    rates = [1, 8000, 44100, 48000, 192000, 655350, 768000, 1048575]
    for depth in range(4, 33):
        rows.append((f'depth-{depth}', depth, 1, rates[depth % len(rates)], 16,
                     signal(depth, 1, 97, depth)))
    for depth in [16, 24, 32]:
        for channels in range(1, 9):
            rows.append((f'layout-{depth}-{channels}ch', depth, channels, 48000, 128,
                         signal(depth, channels, 257, depth + channels)))
    for frames in [1, 2, 15, 16, 17, 192, 255, 256, 257, 576, 1024, 4096, 4097, 65535]:
        rows.append((f'block-{frames}', 16, 1, 44100, max(frames, 16),
                     signal(16, 1, frames, frames)))
    for name, frames in [('frame-counter-2byte', 2049), ('frame-counter-3byte', 32785)]:
        rows.append((name, 16, 1, 48000, 16, [2468] * frames))
    for shape in ['constant', 'ramp', 'quadratic', 'cubic', 'quartic', 'partitioned', 'rice2']:
        frames = 512
        values = []
        rng = random.Random(14688)
        walk = 0
        for at in range(frames):
            if shape == 'constant': value = -123456
            elif shape == 'ramp': value = 17 * at - 4096
            elif shape == 'quadratic': value = 3 * at * at - 400000
            elif shape == 'cubic': value = (at - 256) ** 3 // 4
            elif shape == 'quartic': value = (at - 256) ** 4 // 1024 - 4194304
            elif shape == 'partitioned': value = 0 if at < 256 else rng.randint(-32768, 32767)
            else:
                walk += rng.randint(-70000, 70000)
                value = max(-8388608, min(8388607, walk))
            values.append(value)
        rows.append((shape, 24, 1, 96000, frames, values))
    for mode in ['independent', 'left-side', 'right-side', 'mid-side', 'side-33bit']:
        rng = random.Random(14746)
        values = []
        for at in range(257):
            if mode == 'side-33bit':
                left = [-2147483648, 2147483647, 0][at] if at < 3 else rng.randint(-2147483648, 2147483647)
                right = -left - 1
            elif mode == 'independent': left, right = rng.randint(-32768, 32767), rng.randint(-32768, 32767)
            elif mode == 'left-side':
                left = rng.randint(-2000, 2000) * 16
                right = left + rng.randint(-7, 7)
            elif mode == 'right-side':
                right = rng.randint(-2000, 2000) * 16
                left = right + rng.randint(-7, 7)
            else:
                left = rng.randint(-30000, 30000)
                right = -left + rng.randint(-7, 7)
            values.extend([left, right])
        rows.append((mode, 32 if mode == 'side-33bit' else 16, 2, 48000, 512, values))
    return rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--encoder', required=True, type=Path)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    encoder = args.encoder.resolve()
    ffmpeg = subprocess.check_output(['ffmpeg', '-version'], text=True).splitlines()[0]
    vectors, manifest = [], []
    with tempfile.TemporaryDirectory() as directory:
        temp = Path(directory)
        for name, depth, channels, rate, block, samples in cases():
            pcm = b''.join(struct.pack('<i', x) for x in samples)
            source, encoded_path = temp / 'input.s32le', temp / 'encoded.flac'
            source.write_bytes(pcm)
            result = subprocess.run([str(encoder), str(source), str(encoded_path), str(depth), str(channels), str(rate), str(block)],
                                    check=True, capture_output=True, text=True, timeout=120)
            if 'leakcheck:' in result.stderr:
                assert 'live_bytes=0' in result.stderr, (name, result.stderr)
            encoded = encoded_path.read_bytes()
            ffmpeg_pcm = subprocess.check_output(['ffmpeg', '-v', 'error', '-i', str(encoded_path),
                                                  '-map', '0:a:0', '-f', 's32le', '-'], timeout=30)
            assert ffmpeg_pcm == b''.join(struct.pack('<i', x << (32 - depth)) for x in samples), name
            decoded, version = INDEPENDENT(encoded_path, channels, depth, rate)
            assert decoded == samples, name
            md5 = hashlib.md5(b''.join((x & ((1 << (((depth + 7) // 8) * 8)) - 1)).to_bytes((depth + 7) // 8, 'little') for x in samples)).digest()
            assert encoded[26:42] == md5, name
            vectors.append(f'    Vector {{ name: "{name}", depth: {depth}, channels: {channels}, rate: {rate}, block: {block}, frames: {len(samples) // channels}, pcm: "{packed(pcm)}", encoded: "{packed(encoded)}" }},')
            manifest.append({'name': name, 'depth': depth, 'channels': channels, 'rate': rate, 'block': block,
                             'frames': len(samples) // channels, 'input_sha256': hashlib.sha256(pcm).hexdigest(),
                             'encoded_sha256': hashlib.sha256(encoded).hexdigest(), 'encoded_bytes': len(encoded),
                             'first_assignment': encoded[45] >> 4})
    assert {row['first_assignment'] for row in manifest if row['name'] in ['independent', 'left-side', 'right-side', 'mid-side']} == {1, 8, 9, 10}
    source = '// Generated by scripts/gen-flac-encoder-vectors.py.\n// Independent decode: ' + ffmpeg + ', libFLAC ' + version + '.\n'
    source += 'pub struct Vector { name: string, depth: i32, channels: i32, rate: i32, block: i32, frames: i32, pcm: string, encoded: string }\n\npub function all(): Vector[] {\n  return [\n'
    source += '\n'.join(vectors) + '\n  ];\n}\n'
    metadata = {'ffmpeg': ffmpeg, 'libFLAC': version,
                'sources': {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in
                            [ROOT / 'internal/stdlib/std/audio/flac/encoder.fern.md',
                             ROOT / 'internal/stdlib/std/audio/flac/plan.fern.md',
                             ROOT / 'internal/stdlib/std/audio/flac/bitwriter.fern.md',
                             ROOT / 'internal/stdlib/std/audio/flac/checksum.fern.md']},
                'vectors': manifest}
    outputs = {ROOT / 'tests/stdlib/flac_encoder_vectors.fern': source,
               ROOT / 'scripts/audio-reference/flac-encoder-vectors.json': json.dumps(metadata, indent=2) + '\n'}
    for path, content in outputs.items():
        if args.check: assert path.read_text() == content, str(path) + ' is stale'
        else: path.write_text(content)
    print(f'{len(vectors)} native encoder cases pass FFmpeg, libFLAC {version}, and PCM MD5')


if __name__ == '__main__':
    main()
