#!/usr/bin/env python3
"""Generate VP9 keyframe headers and independent FFmpeg metadata."""
import json
import os
from pathlib import Path
import re
import struct
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def ivf(payload, width=64, height=36):
    return (struct.pack('<4sHH4sHHIIII', b'DKIF', 0, 32, b'VP90', width, height, 1, 1, 1, 0)
            + struct.pack('<IQ', len(payload), 0) + payload)


def packet(path):
    data = path.read_bytes()
    size, time = struct.unpack_from('<IQ', data, 32)
    assert time == 0 and len(data) == 44 + size
    return data[44:]


def reference(path, details=False):
    result = subprocess.run(['ffmpeg', '-v', 'verbose', '-i', str(path), '-c:v', 'copy',
                             '-bsf:v', 'trace_headers', '-f', 'null', '-'], text=True, capture_output=True)
    assert result.returncode == 0 and 'Failed to read unit' not in result.stderr, result.stderr
    values = {}
    positions = {}
    for line in result.stderr.splitlines():
        if '[trace_headers ' not in line:
            continue
        match = re.search(r'\]\s+(\d+)\s+(\S+)\s+.*?=\s*(-?\d+)\s*$', line)
        if match:
            at, name, value = match.groups()
            values[name] = int(value)
            positions[name] = int(at)
    assert values['frame_marker'] == 2 and values['frame_type'] == 0
    p = values['profile_low_bit'] + 2 * values['profile_high_bit']
    width, height = values['frame_width_minus_1'] + 1, values['frame_height_minus_1'] + 1
    different = values['render_and_frame_size_different']
    rw = values['render_width_minus_1'] + 1 if different else width
    rh = values['render_height_minus_1'] + 1 if different else height
    space = values['color_space']
    full = values.get('color_range', 1 if space == 7 else 0)
    x = values.get('subsampling_x', 0 if space == 7 else 1)
    y = values.get('subsampling_y', 0 if space == 7 else 1)
    size = values['header_size_in_bytes']
    header = (positions['header_size_in_bytes'] + 16 + 7) // 8
    fields = [p, width, height, rw, rh, 12 if values.get('ten_or_twelve_bit') else 10 if p >= 2 else 8,
            space, full, x, y, values['show_frame'], values['error_resilient_mode'],
            values['loop_filter_level'], values['loop_filter_sharpness'], values['base_q_idx'],
            values['segmentation_enabled'], values['tile_cols_log2'], values['tile_rows_log2'],
            header, size, header + size]
    return (fields, positions['header_size_in_bytes']) if details else fields


def generate(directory):
    cases = []
    for name, profile, pixel_format, options in (
        ('p0-420', 0, 'yuv420p', []),
        ('p0-resilient', 0, 'yuv420p', ['-error-resilient', '1']),
        ('p1-422', 1, 'yuv422p', []),
        ('p1-rgb', 1, 'gbrp', ['-colorspace', 'rgb']),
        ('p2-10', 2, 'yuv420p10le', []),
        ('p2-12', 2, 'yuv420p12le', []),
        ('p3-10', 3, 'yuv444p10le', []),
        ('p3-12', 3, 'yuv444p12le', []),
        ('tiles', 0, 'yuv420p', ['-tile-columns', '1', '-tile-rows', '1']),
    ):
        path = directory / (name + '.ivf')
        size = '512x128' if name == 'tiles' else '64x36'
        subprocess.run(['ffmpeg', '-v', 'error', '-y', '-f', 'lavfi', '-i', 'testsrc2=size=' + size + ':rate=1',
                        '-frames:v', '1', '-c:v', 'libvpx-vp9', '-threads', '1', '-cpu-used', '8',
                        '-b:v', '0', '-crf', '32', '-g', '1', '-profile:v', str(profile),
                        '-pix_fmt', pixel_format, *options, '-f', 'ivf', str(path)], check=True)
        cases.append((name, path, pixel_format))
    return cases


class Writer:
    def __init__(self):
        self.bits = ''

    def u(self, value, width):
        assert 0 <= value < 1 << width
        self.bits += f'{value:0{width}b}'

    def finish(self, compressed=2):
        self.bits += '0' * (-len(self.bits) % 8)
        return bytes(int(self.bits[i:i + 8], 2) for i in range(0, len(self.bits), 8)) + bytes(compressed + 1)


def segment(w, options):
    w.u(1, 1)
    update_map = options.get('map', 1)
    w.u(update_map, 1)
    if update_map:
        for i in range(7):
            w.u(1, 1)
            w.u(i * 42, 8)
        temporal = options.get('temporal', 1)
        w.u(temporal, 1)
        if temporal:
            for i in range(3):
                w.u(1, 1)
                w.u(i * 127, 8)
    update_data = options.get('data', 1)
    w.u(update_data, 1)
    if update_data:
        w.u(1, 1)
        for i in range(8):
            for width_bits in (8, 6, 2, 0):
                w.u(1, 1)
                if width_bits:
                    w.u((1 << width_bits) - 1, width_bits)
                    if width_bits != 2:
                        w.u(i % 2, 1)


def synthetic(**options):
    w = Writer()
    profile = options.get('profile', 3)
    w.u(2, 2)
    w.u(profile & 1, 1)
    w.u(profile >> 1, 1)
    if profile == 3:
        w.u(0, 1)
    for value in (0, 0, options.get('show', 1), options.get('resilient', 0)):
        w.u(value, 1)
    w.u(0x498342, 24)
    if profile >= 2:
        w.u(options.get('depth', 12) == 12, 1)
    space = options.get('space', 5)
    w.u(space, 3)
    if space != 7:
        w.u(1, 1)
        if profile in (1, 3):
            w.u(options.get('x', 0), 1)
            w.u(options.get('y', 0), 1)
            w.u(0, 1)
    elif profile in (1, 3):
        w.u(0, 1)
    width = options.get('width', 512)
    w.u(width - 1, 16)
    w.u(options.get('height', 128) - 1, 16)
    w.u(1, 1)
    w.u(options.get('render_width', 1000) - 1, 16)
    w.u(options.get('render_height', 71) - 1, 16)
    if not options.get('resilient', 0):
        w.u(1, 1)
        w.u(0, 1)
    w.u(3, 2)
    w.u(63, 6)
    w.u(7, 3)
    w.u(1, 1)
    w.u(1, 1)
    for i in range(6):
        w.u(1, 1)
        w.u(63, 6)
        w.u(i % 2, 1)
    w.u(255, 8)
    for i in range(3):
        w.u(1, 1)
        w.u(15, 4)
        w.u(i % 2, 1)
    segment(w, options)
    blocks = (width + 63) // 64
    minimum = 0
    while 64 << minimum < blocks:
        minimum += 1
    maximum = 0
    while blocks >> (maximum + 1) >= 4:
        maximum += 1
    for _ in range(minimum, maximum):
        w.u(1, 1)
    w.u(1, 1)
    w.u(1, 1)
    compressed = options.get('compressed', 2)
    w.u(compressed, 16)
    return w.finish(compressed)


def controls():
    return [(name, synthetic(**opts)) for name, opts in (
        ('features', {}), ('rgb-12', {'space': 7}), ('resilient', {'resilient': 1}),
        ('hidden', {'show': 0}), ('420-10', {'profile': 2, 'depth': 10}),
        ('422-8', {'profile': 1, 'x': 1}), ('440-12', {'y': 1}),
        ('wide-tiles', {'width': 65536, 'height': 1}),
        ('zero-compressed', {'compressed': 0}),
        ('segment-reset', {'map': 0, 'data': 0}),
        ('segment-map', {'data': 0, 'temporal': 0}),
        ('segment-data', {'map': 0}),
    )]


def main():
    lines = ['// Generated by scripts/gen-video-vp9-fixtures.py with FFmpeg CBS and libvpx.',
             'pub struct Vector { name: string, bytes: string, fields: i64[], size_bit: i32 }',
             'pub function vectors(): Vector[] {', '  return [']
    with tempfile.TemporaryDirectory() as temporary:
        directory = Path(temporary)
        cases = [(name, packet(path), *reference(path, details=True)) for name, path, _ in generate(directory)]
        for name, data in controls():
            path = directory / (name + '.ivf')
            path.write_bytes(ivf(data))
            cases.append((name, data, *reference(path, details=True)))
        for name, data, fields, size_bit in cases:
            assert len(fields) == 21
            assert fields[20] <= len(data)
            if (size_bit + 16) % 8:
                bad = bytearray(data)
                bad[fields[18] - 1] |= 1
                path = directory / 'bad-padding.ivf'
                path.write_bytes(ivf(bad))
                result = subprocess.run(['ffmpeg', '-v', 'verbose', '-i', str(path), '-c:v', 'copy',
                                         '-bsf:v', 'trace_headers', '-f', 'null', '-'], text=True, capture_output=True)
                # FFmpeg can exit zero after a rejected bitstream-filter packet.
                assert 'zero_bit out of range' in result.stderr and 'Failed to read unit' in result.stderr, name
            lines.append('    Vector { name: ' + json.dumps(name) + ', bytes: "' + data.hex() + '", fields: [' +
                         ', '.join(str(value) + 'i64' for value in fields) + '], size_bit: ' + str(size_bit) + ' },')
    lines += ['  ];', '}']
    target = ROOT / 'tests/stdlib/video_vp9_vectors.fern'
    target.write_text('\n'.join(lines) + '\n')
    compiler = os.environ.get('FERN') or subprocess.check_output([str(ROOT / 'bootstrap/bootstrap.sh'), 'stage0'], text=True).strip()
    subprocess.run([compiler, '-fmt', '-w', str(target)], check=True)
    print('Wrote', len(cases), 'independent VP9 headers')


if __name__ == '__main__':
    main()
