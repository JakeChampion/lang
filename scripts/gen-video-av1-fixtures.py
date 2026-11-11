#!/usr/bin/env python3
"""Create AV1 OBU references from FFmpeg's coded-bitstream reader."""
from pathlib import Path
import re
import struct
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def packets(data):
    at = 32
    result = []
    while at < len(data):
        size, timestamp = struct.unpack_from('<IQ', data, at)
        at += 12
        assert size <= len(data) - at
        result.append((timestamp, data[at:at + size]))
        at += size
    return result


def ivf(header, values):
    result = bytearray(header[:32])
    struct.pack_into('<I', result, 24, len(values))
    for timestamp, data in values:
        result += struct.pack('<IQ', len(data), timestamp) + data
    return result


def reference(path):
    result = subprocess.run(['ffmpeg', '-v', 'verbose', '-i', str(path), '-c:v', 'copy',
                             '-bsf:v', 'trace_headers', '-f', 'null', '-'], capture_output=True, text=True)
    assert result.returncode == 0 and 'Failed to read unit' not in result.stderr, result.stderr
    records = []
    current = None
    for line in result.stderr.splitlines():
        if re.search(r'Packet: \d+ bytes', line):
            records.append([])
            current = None
        elif 'OBU header' in line and records:
            current = {}
            records[-1].append(current)
        elif current is not None:
            match = re.search(r'\b(obu_type|obu_extension_flag|temporal_id|spatial_id|obu_size)\s+([01]+) = (\d+)', line)
            if match:
                name, bits, value = match.groups()
                current[name] = int(value)
                if name == 'obu_size':
                    current['size_bytes'] = len(bits) // 8
    layouts = []
    for records_for_packet in records:
        at = 0
        units = []
        for record in records_for_packet:
            offset = at + 1 + record['obu_extension_flag'] + record['size_bytes']
            units.append((record['obu_type'], offset, record['obu_size'], record.get('temporal_id', 0),
                          record.get('spatial_id', 0), record['obu_extension_flag']))
            at = offset + record['obu_size']
        layouts.append(units)
    return layouts


def generate(directory):
    streams = []
    vectors = []
    for pixel_format in ('yuv420p', 'yuv420p10le', 'yuv444p', 'yuv444p10le', 'yuv422p10le', 'yuv420p12le'):
        path = directory / (pixel_format + '.ivf')
        subprocess.run(['ffmpeg', '-v', 'error', '-f', 'lavfi', '-i',
                        'testsrc2=size=64x36:rate=3:duration=1', '-pix_fmt', pixel_format,
                        '-c:v', 'libaom-av1', '-cpu-used', '8', '-threads', '1', '-f', 'ivf', str(path)], check=True)
        data = path.read_bytes()
        values = packets(data)
        assert len(values) == 3
        # Padding permits independent length/extension controls within a real temporal unit.
        controls = [(0, bytes([0x7e, 0x00, 0x80, 0x00])),
                    (0, bytes([0x7a]) + bytes([0x81] + [0x80] * 6 + [0]) + b'\x80')]
        padded = values[0][1] + b''.join(value for _, value in controls)
        values[0] = (values[0][0], padded)
        path.write_bytes(ivf(data, values))
        layouts = reference(path)
        assert len(layouts) == len(values)
        for i, ((timestamp, packet), units) in enumerate(zip(values, layouts)):
            assert units and units[-1][1] + units[-1][2] == len(packet)
            vectors.append((pixel_format + '-' + str(i), packet, units))
        streams.append((path, pixel_format, values, layouts))
    return vectors, streams


def main():
    with tempfile.TemporaryDirectory() as temporary:
        vectors, _ = generate(Path(temporary))
    lines = ['// FFmpeg CBS references. Regenerate with scripts/gen-video-av1-fixtures.py.',
             'pub struct Vector { name: string, bytes: string, kinds: i32[], offsets: i32[], sizes: i32[], temporal: i32[], spatial: i32[] }',
             '', 'pub function vectors(): Vector[] {', '  return [']
    for name, packet, units in vectors:
        fields = [f'name: "{name}"', f'bytes: "{packet.hex()}"']
        for field, column in [('kinds', 0), ('offsets', 1), ('sizes', 2), ('temporal', 3), ('spatial', 4)]:
            fields.append(field + ': [' + ', '.join(str(unit[column]) for unit in units) + ']')
        lines.append('    Vector { ' + ', '.join(fields) + ' },')
    lines += ['  ];', '}', '']
    (ROOT / 'tests/stdlib/video_av1_vectors.fern').write_text('\n'.join(lines))


if __name__ == '__main__':
    main()
