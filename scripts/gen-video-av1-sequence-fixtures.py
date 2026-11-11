#!/usr/bin/env python3
"""Create AV1 sequence metadata from the independent FFmpeg CBS reader."""
import importlib.util
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


packets = load('av1_packets', 'gen-video-av1-fixtures.py')
Writer = load('vp9_writer', 'gen-video-vp9-fixtures.py').Writer


def obu(payload):
    size = len(payload)
    length = bytearray()
    while size >= 128:
        length.append((size & 127) | 128)
        size >>= 7
    length.append(size)
    return bytes([10]) + length + payload


def synthetic(profile=0, depth=8, mono=0, rgb=0, reduced=0, model=0, points=1,
              order=1, choose=1, force=1, integer=1, width=64, height=36,
              x=1, y=1, tick_zeros=0, zero_ticks=False, timing_only=0, equal=1,
              model_bits=32, tool_flags=7, colors=None, delta_id=8, extra_id=5):
    w = Writer()
    w.u(profile, 3)
    w.u(reduced, 1)
    w.u(reduced, 1)
    if reduced:
        w.u(0, 5)
    else:
        w.u(model or timing_only, 1)
        if model or timing_only:
            w.u(0 if zero_ticks else 1, 32)
            w.u(0xffffffff, 32)
            w.u(equal, 1)
            if equal:
                w.bits += '0' * tick_zeros + '1' + '1' * tick_zeros
            w.u(model, 1)
        if model:
            w.u(model_bits - 1, 5)
            w.u(0xffffffff, 32)
            w.u(31, 5)
            w.u(31, 5)
        w.u(model, 1)
        w.u(points - 1, 5)
        for i in range(points):
            w.u(0 if points == 1 else 256 + i + 1, 12)
            w.u(8 if model else 0, 5)
            if model:
                w.u(1, 1)
                w.u(i % 2, 1)
                if i % 2:
                    w.u((1 << model_bits) - 1, model_bits)
                    w.u((1 << model_bits) - 1, model_bits)
                    w.u(1, 1)
                w.u(1 - i % 2, 1)
                if not i % 2:
                    w.u(7, 4)
    wb, hb = max(1, (width - 1).bit_length()), max(1, (height - 1).bit_length())
    w.u(wb - 1, 4)
    w.u(hb - 1, 4)
    w.u(width - 1, wb)
    w.u(height - 1, hb)
    if not reduced:
        w.u(1, 1)
        w.u(delta_id, 4)
        w.u(extra_id, 3)
    w.u(1, 1)
    w.u(3, 2)
    if not reduced:
        w.u(15, 4)
        w.u(order, 1)
        if order:
            w.u(3, 2)
        w.u(choose, 1)
        if not choose:
            w.u(force, 1)
        if choose or force:
            w.u(integer, 1)
            if not integer:
                w.u(1, 1)
        if order:
            w.u(7, 3)
    w.u(tool_flags, 3)
    w.u(depth > 8, 1)
    if profile == 2 and depth > 8:
        w.u(depth == 12, 1)
    if profile != 1:
        w.u(mono, 1)
    w.u(bool(rgb or colors), 1)
    if rgb or colors:
        for value in ((1, 13, 0) if rgb else colors):
            w.u(value, 8)
    if mono:
        w.u(1, 1)
    elif not rgb:
        w.u(0, 1)
        if profile == 2 and depth == 12:
            w.u(x, 1)
            if x:
                w.u(y, 1)
        else:
            x, y = (1, 1) if profile == 0 else (0, 0) if profile == 1 else (1, 0)
        if x and y:
            w.u(2, 2)
    if not mono:
        w.u(1, 1)
    w.u(1, 1)
    w.bits += '1'
    w.bits += '0' * (-len(w.bits) % 8)
    payload = bytes(int(w.bits[i:i + 8], 2) for i in range(0, len(w.bits), 8))
    return obu(payload)


def reference(directory, name, data, carrier):
    path = directory / (name + '.ivf')
    path.write_bytes(packets.ivf(carrier, [(0, data)]))
    result = subprocess.run(['ffmpeg', '-v', 'verbose', '-i', str(path), '-c:v', 'copy',
                             '-bsf:v', 'trace_headers', '-f', 'null', '-'], text=True, capture_output=True)
    assert result.returncode == 0 and 'Failed to read unit' not in result.stderr, result.stderr
    assert 'Failed to parse temporal unit' not in result.stderr and 'out of range' not in result.stderr, result.stderr
    values, positions = {}, {}
    for line in result.stderr.splitlines():
        if '[trace_headers ' not in line:
            continue
        match = re.search(r'\]\s+(\d+)\s+(\S+)\s+.*?=\s*(-?\d+)\s*$', line)
        if match:
            at, key, value = match.groups()
            values[key], positions[key] = int(value), int(at)
    assert values['obu_type'] == 1 and values['obu_extension_flag'] == 0
    assert values['trailing_one_bit'] == 1
    profile = values['seq_profile']
    depth = 12 if values.get('twelve_bit', 0) else 10 if values['high_bitdepth'] else 8
    mono = values.get('mono_chrome', 0)
    cp, tc, mc = [values.get(key, 2) for key in ('color_primaries', 'transfer_characteristics', 'matrix_coefficients')]
    rgb = (cp, tc, mc) == (1, 13, 0)
    if mono:
        x, y = 1, 1
    elif rgb or profile == 1:
        x, y = 0, 0
    elif profile == 0:
        x, y = 1, 1
    elif depth == 12:
        x = values['subsampling_x']
        y = values.get('subsampling_y', 0)
    else:
        x, y = 1, 0
    frame_ids = (values['delta_frame_id_length_minus_2'] + values['additional_frame_id_length_minus_1'] + 3
                 if values.get('frame_id_numbers_present_flag', 0) else 0)
    order = values['order_hint_bits_minus_1'] + 1 if values.get('enable_order_hint', 0) else 0
    fields = [profile, values['max_frame_width_minus_1'] + 1, values['max_frame_height_minus_1'] + 1,
              depth, mono, x, y, values.get('chroma_sample_position', 0), cp, tc, mc,
              values.get('color_range', int(rgb)), values['still_picture'], values['reduced_still_picture_header'],
              values.get('operating_points_cnt_minus_1', 0) + 1, frame_ids, order,
              values['use_128x128_superblock'], values['enable_superres'], values['enable_cdef'],
              values['enable_restoration'], values['film_grain_params_present']]
    payload_at = positions['seq_profile'] // 8
    assert payload_at + values['obu_size'] == len(data)
    minimum = (positions['trailing_one_bit'] - payload_at * 8 + 8) // 8
    return fields, positions, minimum


def generate(directory):
    vectors, streams = packets.generate(directory)
    cases = []
    for path, pixel_format, values, layouts in streams:
        packet = values[0][1]
        at = 0
        for kind, offset, size, *_ in layouts[0]:
            if kind == 1:
                cases.append(('encoded-' + pixel_format, packet[at:offset + size]))
                break
            at = offset + size
    controls = [('reduced-still', dict(reduced=1)), ('monochrome', dict(mono=1)),
                ('rgb', dict(profile=1, depth=10, rgb=1)), ('rgb12', dict(profile=2, depth=12, rgb=1)),
                ('wide', dict(width=65536, height=1)), ('model32', dict(model=1, points=32, tick_zeros=31)),
                ('no-order-no-screen', dict(order=0, choose=0, force=0, tool_flags=0)),
                ('integer-force', dict(choose=0, force=1, integer=0)),
                ('twelve-444', dict(profile=2, depth=12, x=0, y=0)),
                ('twelve-422', dict(profile=2, depth=12, x=1, y=0)),
                ('monochrome12', dict(profile=2, depth=12, mono=1)),
                ('timing-only', dict(timing_only=1)), ('variable-timing', dict(timing_only=1, equal=0)),
                ('model-one-bit', dict(model=1, points=2, model_bits=1)),
                ('bt2020', dict(profile=2, depth=12, colors=(9, 16, 9)))]
    cases += [(name, synthetic(**options)) for name, options in controls]
    carrier = streams[0][0].read_bytes()
    results = [(name, data, *reference(directory, name, data, carrier)) for name, data in cases]
    return results, streams, carrier


def main():
    with tempfile.TemporaryDirectory() as temporary:
        vectors, _, _ = generate(Path(temporary))
    lines = ['// Independent FFmpeg CBS sequence references. Regenerate with scripts/gen-video-av1-sequence-fixtures.py.',
             'pub struct Vector { name: string, bytes: string, values: i32[], minimum: i32, trailing_bit: i32, sample_bit: i32, tick_bit: i32, scale_bit: i32, decoder_tick_bit: i32 }',
             '', 'pub function vectors(): Vector[] {', '  return [']
    for name, data, fields, positions, minimum in vectors:
        parts = [f'name: "{name}"', f'bytes: "{data.hex()}"', 'values: [' + ', '.join(map(str, fields)) + ']', f'minimum: {minimum}']
        for field, key in [('trailing_bit', 'trailing_one_bit'), ('sample_bit', 'chroma_sample_position'),
                           ('tick_bit', 'num_units_in_display_tick'), ('scale_bit', 'time_scale'),
                           ('decoder_tick_bit', 'num_units_in_decoding_tick')]:
            parts.append(field + ': ' + str(positions.get(key, -1)))
        lines.append('    Vector { ' + ', '.join(parts) + ' },')
    lines += ['  ];', '}', '']
    invalid = [synthetic(model=1, zero_ticks=True), synthetic(model=1, tick_zeros=32),
               synthetic(delta_id=15, extra_id=7), synthetic(rgb=1), synthetic(profile=2, depth=10, rgb=1)]
    lines += ['pub function invalid_constraints(): string[] {',
              '  return [' + ', '.join('"' + data.hex() + '"' for data in invalid) + '];', '}', '',
              'pub function excessive_area(): string {',
              '  return "' + synthetic(width=65536, height=65536).hex() + '";', '}', '']
    (ROOT / 'tests/stdlib/video_av1_sequence_vectors.fern').write_text('\n'.join(lines))


if __name__ == '__main__':
    main()
