#!/usr/bin/env python3
"""Freeze independently remuxed M4A ALAC files and exact FFmpeg integer PCM."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import runpy
import struct
import subprocess
import tempfile
import zlib

ROOT = Path(__file__).resolve().parent.parent
ALAC = runpy.run_path(str(ROOT / 'scripts/gen-alac-vectors.py'))


def unpack(value):
    return zlib.decompress(base64.b64decode(value))


def packed(value):
    return base64.b64encode(zlib.compress(value, 9)).decode()


def box(kind, data):
    return struct.pack('>I4s', len(data) + 8, kind) + data


def clocked(data, rate, frames, durations):
    output = bytearray()
    at = 0
    while at < len(data):
        size, kind = struct.unpack_from('>I4s', data, at)
        assert size >= 8 and at + size <= len(data)
        body = bytearray(data[at + 8:at + size])
        if kind in [b'moov', b'trak', b'mdia', b'minf', b'stbl']:
            body = clocked(body, rate, frames, durations)
        elif kind in [b'mvhd', b'mdhd']:
            assert body[0] == 0
            struct.pack_into('>II', body, 12, rate, frames)
        elif kind == b'tkhd':
            assert body[0] == 0
            struct.pack_into('>I', body, 20, frames)
        elif kind == b'stts':
            groups = []
            for duration in durations:
                if groups and groups[-1][1] == duration:
                    groups[-1] = (groups[-1][0] + 1, duration)
                else:
                    groups.append((1, duration))
            body = bytes(4) + struct.pack('>I', len(groups)) + b''.join(struct.pack('>II', *entry) for entry in groups)
        elif kind == b'edts':
            at += size
            continue
        output += box(kind, body)
        at += size
    return bytes(output)


def rewrite(data, change, entry=None):
    output = bytearray()
    at = 0
    while at < len(data):
        size, kind = struct.unpack_from('>I4s', data, at)
        assert size >= 8 and at + size <= len(data)
        body = data[at + 8:at + size]
        if kind in [b'moov', b'trak', b'mdia', b'minf', b'stbl']:
            body = rewrite(body, change, entry)
        elif kind == b'stsd' and entry:
            header, body = body[:8], body[8:]
            items = bytearray()
            pos = 0
            while pos < len(body):
                width, tag = struct.unpack_from('>I4s', body, pos)
                items += box(tag, entry(body[pos + 8:pos + width]))
                pos += width
            body = header + items
        output += change(kind, body)
        at += size
    return bytes(output)


def edit_list(data, frames, edits):
    duration = sum(length for length, _ in edits)
    payload = bytes(4) + struct.pack('>I', len(edits)) + b''.join(struct.pack('>IiI', length, start, 65536) for length, start in edits)
    def change(kind, body):
        body = bytearray(body)
        if kind == b'mvhd':
            struct.pack_into('>I', body, 16, duration)
        elif kind == b'tkhd':
            struct.pack_into('>I', body, 20, duration)
        elif kind == b'trak':
            body += box(b'edts', box(b'elst', payload))
        return box(kind, body)
    return rewrite(data, change)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    parser.add_argument('--keep', type=Path)
    args = parser.parse_args()
    source = (ROOT / 'tests/stdlib/alac_vectors.fern').read_text()
    pattern = r'Vector \{ name: "([^"]+)"[^\n]*?configuration: "([^"]+)"[^\n]*?packets: "([^"]+)"[^\n]*?pcm: "([^"]+)" \}'
    references = []
    for name, config, bundle, pcm in re.findall(pattern, source):
        config, bundle, pcm = map(unpack, (config, bundle, pcm))
        count = struct.unpack_from('<I', bundle)[0]
        packets = []
        at = 4
        for _ in range(count):
            size = struct.unpack_from('<I', bundle, at)[0]
            at += 4
            packets.append(bundle[at:at + size])
            at += size
        assert at == len(bundle)
        references.append((name, config, packets, pcm))
    assert len(references) == 38
    version = subprocess.check_output(['ffmpeg', '-version'], text=True).splitlines()[0]
    rows, manifest, saved = [], [], {}
    with tempfile.TemporaryDirectory() as directory:
        temp = Path(directory)
        def freeze(name, path, depth, channels, expected, frames):
            encoded = path.read_bytes()
            if args.keep:
                args.keep.mkdir(parents=True, exist_ok=True)
                (args.keep / (name + '.m4a')).write_bytes(encoded)
            probe = json.loads(subprocess.check_output(['ffprobe', '-v', 'error', '-select_streams', 'a:0', '-show_streams', '-show_packets', '-of', 'json', str(path)]))
            stream = probe['streams'][0]
            ffmpeg_verified = depth != 32 and not name.startswith('two-pass') and name != 'data-and-fill'
            if ffmpeg_verified:
                decoded = subprocess.check_output(['ffmpeg', '-v', 'error', '-i', str(path), '-map', '0:a:0', '-f', 's32le', '-'])
                values = [v[0] >> (32 - depth) for v in struct.iter_unpack('<i', decoded)]
                assert len(values) >= frames * channels
                values = values[:frames * channels]
                order = {7: [0, 1, 2, 3, 6, 4, 5], 8: [0, 1, 2, 3, 6, 7, 4, 5]}.get(channels, list(range(channels)))
                values = [values[frame * channels + channel] for frame in range(frames) for channel in order]
                assert struct.pack('<' + 'i' * len(values), *values) == expected, (name, 'Apple and normalized FFmpeg PCM disagree')
            pcm = expected
            assert len(pcm) == frames * channels * 4
            assert int(stream['duration_ts']) == frames
            rate = int(stream['sample_rate'])
            rows.append(f'    Vector {{ name: "{name}", rate: {rate}, depth: {depth}, channels: {channels}, frames: {frames}, packets: {len(probe["packets"])}, encoded: "{packed(encoded)}", pcm: "{packed(pcm)}" }},')
            manifest.append({'name': name, 'rate': rate, 'depth': depth, 'channels': channels, 'frames': frames,
                             'ffmpeg_layout': stream.get('channel_layout'), 'output_mask': [0, 4, 3, 7, 263, 1543, 1551, 1807, 1743][channels], 'packets': len(probe['packets']),
                             'ffmpeg_pcm_verified': ffmpeg_verified, 'encoded_sha256': hashlib.sha256(encoded).hexdigest(), 'pcm_sha256': hashlib.sha256(pcm).hexdigest()})
            if args.keep:
                args.keep.mkdir(parents=True, exist_ok=True)
                (args.keep / (name + '.m4a')).write_bytes(encoded)
                (args.keep / (name + '.s32le')).write_bytes(pcm)
            print(name, depth, channels, frames, stream.get('channel_layout'), flush=True)

        for name, configuration, packets, pcm in references:
            depth, channels = configuration[5], configuration[9]
            frames = len(pcm) // (4 * channels)
            source_path = temp / 'input.caf'
            source_path.write_bytes(ALAC['caf'](configuration, packets, frames))
            output = temp / 'reference.m4a'
            subprocess.run(['ffmpeg', '-v', 'error', '-y', '-i', str(source_path), '-c:a', 'copy', '-map_metadata', '-1',
                            '-fflags', '+bitexact', '-flags:a', '+bitexact', '-movie_timescale', str(struct.unpack_from('>I', configuration, 20)[0]), str(output)], check=True)
            rate = struct.unpack_from('>I', configuration, 20)[0]
            block = struct.unpack_from('>I', configuration)[0]
            durations = [block] * (len(packets) - 1) + [frames - block * (len(packets) - 1)]
            assert all(0 < value <= block for value in durations)
            data = output.read_bytes()
            assert data.index(b'mdat') < data.index(b'moov')
            output.write_bytes(clocked(data, rate, frames, durations))
            expected = pcm
            freeze(name, output, depth, channels, expected, frames)
            saved[name] = (output.read_bytes(), configuration, packets, pcm, frames)

        base, cfg, packets, pcm, frames = saved['no-predictor']
        output = temp / 'variant.m4a'
        def variant(name, data, expected=pcm, count=frames):
            output.write_bytes(data)
            freeze(name, output, cfg[5], cfg[9], expected, count)
        def standard(kind, body):
            return box(kind, body)
        def version_one(body):
            header = bytearray(body[:28])
            struct.pack_into('>H', header, 8, 1)
            return header + struct.pack('>IIII', struct.unpack_from('>I', cfg)[0], 0, 0, 0) + body[28:]
        variant('entry-version-one', rewrite(base, standard, version_one))
        def co64(kind, body):
            if kind == b'stco':
                count = struct.unpack_from('>I', body, 4)[0]
                return box(b'co64', body[:8] + b''.join(struct.pack('>Q', struct.unpack_from('>I', body, 8 + 4 * i)[0]) for i in range(count)))
            return box(kind, body)
        variant('wide-chunk-offsets', rewrite(base, co64))
        def duplicate(kind, body):
            if kind == b'stsd':
                return box(kind, bytes(4) + struct.pack('>I', 2) + body[8:] + body[8:])
            return box(kind, body)
        variant('identical-descriptions', rewrite(base, duplicate))
        def repeat(kind, body):
            if kind == b'mdat': body = packets[0] * 2
            elif kind == b'stsz': body = bytes(4) + struct.pack('>II', 0, 2) + struct.pack('>II', len(packets[0]), len(packets[0]))
            elif kind == b'stsc': body = bytes(4) + struct.pack('>IIII', 1, 1, 2, 1)
            return box(kind, body)
        repeated = clocked(rewrite(base, repeat), struct.unpack_from('>I', cfg, 20)[0], frames * 2, [frames, frames])
        variant('two-short-packets', repeated, pcm * 2, frames * 2)
        trimmed = edit_list(base, frames, [(7, 5)])
        variant('sample-exact-edit', trimmed, pcm[5 * 4:12 * 4], 7)
        fast_input = temp / 'fast-input.m4a'
        fast_input.write_bytes(base)
        subprocess.run(['ffmpeg', '-v', 'error', '-y', '-i', str(fast_input), '-c:a', 'copy', '-movflags', '+faststart', '-movie_timescale', '48000', '-map_metadata', '-1', '-fflags', '+bitexact', '-flags:a', '+bitexact', str(output)], check=True)
        freeze('faststart', output, cfg[5], cfg[9], pcm, frames)
    header = '// Generated by scripts/gen-mp4-alac-vectors.py.\n// Container remux reference: ' + version + '.\n'
    header += 'pub struct Vector { name: string, rate: i32, depth: i32, channels: i32, frames: i32, packets: i32, encoded: string, pcm: string }\n\n'
    smoke = next(row for row in rows if 'name: "no-predictor"' in row).strip().removesuffix(',')
    outputs = {ROOT / 'tests/stdlib/mp4_alac_smoke_vectors.fern': header + 'pub function vector(): Vector {\n  return ' + smoke + ';\n}\n',
               ROOT / 'tests/stdlib/mp4_alac_vectors.fern': header + 'pub function all(): Vector[] {\n  return [\n' + '\n'.join(rows) + '\n  ];\n}\n',
               ROOT / 'scripts/audio-reference/mp4-alac-vectors.json': json.dumps({'ffmpeg': version, 'pcm_reference': 'Apple ALAC c38887c5c5e64a4b31108733bd79ca9b2496d987, frozen packet corpus', 'clock_source': 'Known packet counts, frame length, and independent decoded PCM extent', 'channel_order': 'Canonical ALAC WAVE order; FFmpeg back-surround order is normalized for 7 and 8 channels', 'source_bank_sha256': hashlib.sha256(source.encode()).hexdigest(), 'vectors': manifest}, indent=2) + '\n'}
    for path, content in outputs.items():
        if args.check:
            assert path.read_text() == content, str(path) + ' is stale'
        else:
            path.write_text(content)
    print(str(len(rows)) + ' independent M4A ALAC references')


if __name__ == '__main__':
    main()
