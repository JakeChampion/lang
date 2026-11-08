#!/usr/bin/env python3
"""Freeze independent FFmpeg PCM references for AIFF, AIFF-C, and AU."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import struct
import subprocess
import tempfile
import zlib

ROOT = Path(__file__).resolve().parent.parent


def packed(data):
    return base64.b64encode(zlib.compress(data, 9)).decode()


def chunk(tag, body):
    return tag + struct.pack('>I', len(body)) + body + bytes(len(body) % 2)


def extended(rate):
    exponent = rate.bit_length() - 1
    return struct.pack('>HQ', 16383 + exponent, rate << (63 - exponent))


def aiff(payload, bits, frames, channels=1, rate=44100, tag=None, offset=0, extra=b''):
    common = struct.pack('>HIH', channels, frames, bits) + extended(rate)
    rows = []
    if tag:
        rows.append(chunk(b'FVER', bytes.fromhex('a2805140')))
        common += tag + b'\x00\x00'
    rows += [chunk(b'COMM', common), extra,
             chunk(b'SSND', struct.pack('>II', offset, 0) + bytes(offset) + payload)]
    body = (b'AIFC' if tag else b'AIFF') + b''.join(rows)
    return b'FORM' + struct.pack('>I', len(body)) + body


def au(payload, code, channels=1, rate=44100, annotation=b'', unknown=False):
    size = 0xffffffff if unknown else len(payload)
    return b'.snd' + struct.pack('>5I', 24 + len(annotation), size, code, rate, channels) + annotation + payload


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    version = subprocess.check_output(['ffmpeg', '-version'], text=True).splitlines()[0]
    vectors, manifest = [], []
    with tempfile.TemporaryDirectory() as directory:
        temp = Path(directory)

        def decode(path, codec, format):
            return subprocess.check_output(['ffmpeg', '-v', 'error', '-i', str(path),
                                            '-map', '0:a:0', '-c:a', codec, '-f', format, '-'])

        def freeze(name, data, container, channels, bits, rate, floating=False, expected=None):
            path = temp / ('reference.' + ('aiff' if container == 0 else 'au'))
            path.write_bytes(data)
            floats = decode(path, 'pcm_f64le', 'f64le')
            assert len(floats) % (8 * channels) == 0
            integers = b''
            if not floating:
                raw = decode(path, 'pcm_s32le', 's32le')
                samples = [x[0] >> (32 - bits) for x in struct.iter_unpack('<i', raw)]
                if expected is not None:
                    assert samples == expected, (name, samples, expected)
                integers = b''.join(struct.pack('<i', x) for x in samples)
            frames = len(floats) // (8 * channels)
            vectors.append(f'    Vector {{ name: "{name}", container: {container}, channels: {channels}, bits: {bits}, rate: {rate}, frames: {frames}, floating: {str(floating).lower()}, encoded: "{packed(data)}", integers: "{packed(integers)}", floats: "{packed(floats)}" }},')
            manifest.append({'name': name, 'container': ['AIFF/AIFC', 'AU'][container],
                             'channels': channels, 'bits': bits, 'rate': rate, 'frames': frames,
                             'floating': floating, 'encoded_sha256': hashlib.sha256(data).hexdigest(),
                             'integer_sha256': hashlib.sha256(integers).hexdigest(),
                             'float64_sha256': hashlib.sha256(floats).hexdigest()})

        codecs = [('pcm_s8', 8), ('pcm_s16be', 16), ('pcm_s24be', 24), ('pcm_s32be', 32),
                  ('pcm_f32be', 32), ('pcm_f64be', 64), ('pcm_alaw', 16), ('pcm_mulaw', 16)]
        for container, name in enumerate(['aiff', 'au']):
            for codec, bits in codecs + ([('pcm_s16le', 16)] if container == 0 else []):
                layouts = list(range(1, 9)) if codec == 'pcm_s16be' else [1, 2]
                for channels in layouts:
                    rate = [8000, 44100, 48000, 96000, 192000][(channels + bits) % 5]
                    floating = codec.startswith('pcm_f')
                    count = 33 * channels
                    if floating:
                        signal = [-1.5, -1., -0.5, -0., 0., 0.25, 1., 1.5, 2.0 ** -100]
                        raw = b''.join(struct.pack('<d', signal[i % len(signal)]) for i in range(count))
                        source = 'f64le'
                    else:
                        signal = [-2147483648, -2147483647, -65536, -1, 0, 1, 65536, 2147483646, 2147483647]
                        values = [signal[i] if i < len(signal) else ((i * 1812433253) & 0xffffffff) - 2147483648 for i in range(count)]
                        raw = b''.join(struct.pack('<i', x) for x in values)
                        source = 's32le'
                    path = temp / ('encoded.' + name)
                    subprocess.run(['ffmpeg', '-v', 'error', '-y', '-f', source, '-ar', str(rate),
                                    '-ac', str(channels), '-i', 'pipe:0', '-map_metadata', '-1',
                                    '-c:a', codec, '-fflags', '+bitexact', '-flags:a', '+bitexact',
                                    '-f', name, str(path)], input=raw, check=True)
                    freeze(f'{name}-{codec}-{channels}ch', path.read_bytes(), container, channels, bits, rate, floating)

        for bits in [1, 4, 12, 20, 31]:
            values = [-(1 << (bits - 1)), -1, 0, 0, 0, 1 if bits > 1 else 0, (1 << (bits - 1)) - 1]
            storage = (bits + 7) // 8
            payload = b''.join(((x << (storage * 8 - bits)) & ((1 << (storage * 8)) - 1)).to_bytes(storage, 'big') for x in values)
            freeze(f'aiff-{bits}bit', aiff(payload, bits, len(values)), 0, 1, bits, 44100, expected=values)
        for tag, little in [(b'NONE', False), (b'twos', False), (b'sowt', True)]:
            values = [-32768, -17, 0, 31, 32767]
            payload = b''.join(x.to_bytes(2, 'little' if little else 'big', signed=True) for x in values)
            freeze('aifc-' + tag.decode(), aiff(payload, 16, len(values), tag=tag), 0, 1, 16, 44100, expected=values)
        values = [-128, -1, 0, 1, 127]
        payload = bytes(x & 255 for x in values)
        freeze('aiff-offset-unknown-odd-chunk', aiff(payload, 8, 5, offset=3, extra=chunk(b'JUNK', b'abc')), 0, 1, 8, 44100, expected=values)
        for code, name in [(1, 'mulaw'), (27, 'alaw')]:
            freeze('au-all-' + name + '-codes', au(bytes(range(256)), code), 1, 1, 16, 44100)
        freeze('au-unknown-size-annotation', au(bytes(x & 255 for x in values), 2, annotation=b'odd annotation\x00', unknown=True), 1, 1, 8, 44100, expected=values)

    source = '// Generated by scripts/gen-linear-audio-vectors.py.\n// Reference: ' + version + '\n'
    source += 'pub struct Vector { name: string, container: i32, channels: i32, bits: i32, rate: i32, frames: i32, floating: boolean, encoded: string, integers: string, floats: string }\n\npub function all(): Vector[] {\n  return [\n'
    source += '\n'.join(vectors) + '\n  ];\n}\n'
    outputs = {ROOT / 'tests/stdlib/linear_audio_vectors.fern': source,
               ROOT / 'scripts/audio-reference/linear-audio-vectors.json': json.dumps({'reference': version, 'vectors': manifest}, indent=2) + '\n'}
    for path, content in outputs.items():
        if args.check:
            assert path.read_text() == content, str(path) + ' is stale'
        else:
            path.write_text(content)
    print(f'{len(vectors)} independent linear-audio vectors')


if __name__ == '__main__':
    main()
