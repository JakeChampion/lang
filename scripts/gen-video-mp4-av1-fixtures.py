#!/usr/bin/env python3
"""Create AV1 configuration and MP4 references with FFmpeg CBS and FFprobe."""
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


sequences = load('av1_sequences', 'gen-video-av1-sequence-fixtures.py')
mp4 = load('mp4_packets', 'gen-video-mp4-fixtures.py')


def field(data, positions, key, width, default=0):
    if key not in positions:
        return default
    at = positions[key]
    return int(''.join(f'{x:08b}' for x in data)[at:at + width], 2)


def record(data, values, positions):
    profile, width, height, depth, mono, x, y, csp = values[:8]
    level = field(data, positions, 'seq_level_idx[0]', 5)
    tier = field(data, positions, 'seq_tier[0]', 1)
    flags = tier << 7 | int(depth > 8) << 6 | int(depth == 12) << 5 | mono << 4 | x << 3 | y << 2 | csp
    header = bytes([0x81, profile << 5 | level, flags, 0])
    expected = [profile, level, tier, depth, mono, x, y, csp, 0, width, height]
    return header + data, expected


def extradata(path):
    value = json.loads(subprocess.check_output(['ffprobe', '-v', 'error', '-show_data',
                                               '-show_entries', 'stream=extradata', '-of', 'json', str(path)]))
    lines = value['streams'][0]['extradata'].strip().splitlines()
    return bytes.fromhex(''.join(line.split(':', 1)[1].split('  ', 1)[0].replace(' ', '') for line in lines))


def generate(directory):
    headers, streams, _ = sequences.generate(directory)
    configurations = [(name, *record(data, values, positions)) for name, data, values, positions, _ in headers]
    files = []
    for path, pixel_format, _, _ in streams:
        target = directory / (pixel_format + '.mp4')
        subprocess.run(['ffmpeg', '-v', 'error', '-i', str(path), '-c:v', 'copy', '-tag:v', 'av01',
                        '-movflags', '+faststart', str(target)], check=True)
        config = extradata(target)
        assert config[0] == 0x81 and len(config) >= 4
        metadata = mp4.packets(target)
        stream = metadata['streams'][0]
        assert stream['width'] == 64 and stream['height'] == 36
        assert len(metadata['packets']) == 3
        files.append((pixel_format, target, config, metadata))
    return configurations, files


def main():
    configs = ['// Independent FFmpeg CBS references from scripts/gen-video-mp4-av1-fixtures.py.',
               'pub struct Config { name: string, bytes: string, values: i32[] }',
               'pub function configurations(): Config[] {', '  return [']
    files = ['// Independent FFmpeg and FFprobe MP4 references.',
             'pub struct Packet { offset: i64, bytes: i32, dts: i64, pts: i64, duration: u32, sync: boolean }',
             'pub struct File { name: string, bytes: string, config: string, scale: u32, packets: Packet[] }',
             'pub function files(): File[] {', '  return [']
    with tempfile.TemporaryDirectory() as temporary:
        configurations, references = generate(Path(temporary))
        for name, data, values in configurations:
            configs.append(f'    Config {{ name: "{name}", bytes: "{data.hex()}", values: {values} }},')
        for name, path, config, metadata in references:
            numerator, denominator = map(int, metadata['streams'][0]['time_base'].split('/'))
            assert numerator == 1
            files.append(f'    File {{ name: "{name}", bytes: "{path.read_bytes().hex()}", config: "{config.hex()}", scale: {denominator}u32, packets: [')
            for p in metadata['packets']:
                files.append(f'      Packet {{ offset: {p["pos"]}i64, bytes: {p["size"]}, dts: {p["dts"]}i64, pts: {p["pts"]}i64, duration: {p["duration"]}u32, sync: {str("K" in p["flags"]).lower()} }},')
            files.append('    ] },')
    configs += ['  ];', '}']
    files += ['  ];', '}']
    (ROOT / 'tests/stdlib/video_mp4_av1_vectors.fern').write_text('\n'.join(configs + files) + '\n')


if __name__ == '__main__':
    main()
