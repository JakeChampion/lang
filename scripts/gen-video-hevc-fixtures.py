#!/usr/bin/env python3
"""Create HEVC SPS vectors through the independent FFmpeg CBS reader."""
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


avc = load('hevc_avc_fields', 'gen-video-avc-fixtures.py')
syntax = load('hevc_shared_writer', 'gen-video-avc-syntax-fixtures.py')


def finish(writer, kind):
    data = writer.finish(kind << 1)
    return data[:1] + b'\x01' + data[1:]


def ptl(w, sub, profile=4, variants=False):
    w.u(profile, 8)
    w.u(1 << (31 - profile), 32)
    w.u(9 << 44, 48)
    w.u(186, 8)
    flags = [i % 4 if variants else 0 for i in range(sub)]
    for flag in flags:
        w.u(flag, 2)
    if sub:
        w.u(0, 2 * (8 - sub))
    for flag in flags:
        if flag & 2:
            w.u(profile, 8)
            w.u(1 << (31 - profile), 32)
            w.u(9 << 44, 48)
        if flag & 1:
            w.u(120, 8)


def vps(identifier=0, sub=0):
    w = syntax.Writer()
    w.u(identifier, 4)
    w.u(3, 2)
    w.u(0, 6)
    w.u(sub, 3)
    w.u(1, 1)
    w.u(65535, 16)
    ptl(w, sub)
    w.u(0, 1)
    for value in (15, 0, 0):
        w.ue(value)
    w.u(0, 6)
    w.ue(0)
    w.u(0, 2)
    return finish(w, 32)


def scaling(w, mode, options):
    w.u(int(bool(mode)), 1)
    if not mode:
        return
    w.u(1, 1)
    for size in range(4):
        for matrix in range(0, 6, 3 if size == 3 else 1):
            dense = mode == 'dense'
            w.u(int(dense), 1)
            if not dense:
                w.ue(options.get("scaling_delta", matrix // 3 if size == 3 else matrix))
                continue
            if size > 1:
                w.se(options.get("scaling_dc", -7 if matrix % 2 else 247))
            for i in range(16 if size == 0 else 64):
                w.se(options.get("scaling_coefficient", -128 if i % 2 else 127))


def reference_sets(w, mode, options):
    count = 64 if mode == 'maximum' else 4 if mode == 'predicted' else 0
    w.ue(options.get("rps_count", count))
    previous = []
    for index in range(count):
        predicted = mode == 'predicted' and index > 0
        if index:
            w.u(int(predicted), 1)
        if predicted:
            delta = 1 if index % 2 else -1
            w.u(int(delta < 0), 1)
            w.ue(0)
            values = []
            for j, value in enumerate(previous + [0]):
                used = j % 2
                w.u(used, 1)
                if not used:
                    w.u(1, 1)
                if value + delta:
                    values.append(value + delta)
            previous = sorted((x for x in values if x < 0), reverse=True) + sorted(x for x in values if x > 0)
        else:
            negative, positive = (15, 0) if mode == 'maximum' else (2, 2)
            w.ue(options.get("negative_count", negative))
            w.ue(positive)
            previous = []
            for count, direction in ((negative, -1), (positive, 1)):
                poc = 0
                for j in range(count):
                    delta = 32767 if mode == 'maximum' else j
                    w.ue(delta)
                    w.u(j % 2, 1)
                    poc += direction * (delta + 1)
                    previous.append(poc)


def hrd(w, sub, sub_picture, options):
    w.u(3, 2)
    w.u(int(sub_picture), 1)
    if sub_picture:
        w.u((1 << 19) - 1, 19)
    w.u((1 << (12 if sub_picture else 8)) - 1, 12 if sub_picture else 8)
    w.u((1 << 15) - 1, 15)
    for layer in range(sub + 1):
        general = layer % 3 == 0
        within = layer % 3 != 2
        w.u(int(general), 1)
        if not general:
            w.u(int(within), 1)
        if within:
            w.ue(options.get("hrd_duration", 2047))
            w.ue(options.get("cpb_count", 31))
            count = 32
        else:
            w.u(1, 1)
            count = 1
        for _ in range(2):
            for i in range(count):
                for field in range(4 if sub_picture else 2):
                    w.ue(4294967294 if layer == 0 and i == 0 and field == 0 else 0)
                w.u(i % 2, 1)


def vui(w, sub, mode, options):
    w.u(int(bool(mode)), 1)
    if not mode:
        return
    w.u(1, 1)
    w.u(255, 8)
    w.u(options.get('sar_width', 1000), 16)
    w.u(options.get("sar_height", 1001), 16)
    w.u(3, 2)
    w.u(1, 1)
    w.u(options.get("video_flags", 23), 5)
    for value in (9, 16, 9):
        w.u(value, 8)
    w.u(1, 1)
    w.ue(options.get("chroma_location", 5))
    w.ue(4)
    w.u(7, 3)
    w.u(1, 1)
    for value in (1, 2, 3, 4):
        w.ue(value)
    w.u(1, 1)
    w.u(options.get('tick', 4294967295), 32)
    w.u(options.get("time_scale", 4294967294), 32)
    w.u(1, 1)
    w.ue(4294967294)
    w.u(int(mode in ('hrd', 'sub-hrd')), 1)
    if mode in ('hrd', 'sub-hrd'):
        hrd(w, sub, mode == 'sub-hrd', options)
    w.u(1, 1)
    w.u(7, 3)
    for value in (4095, 16, 16, options.get("max_mv", 15), 15):
        w.ue(value)


def make(**options):
    w = syntax.Writer()
    chroma = options.get('chroma', 1)
    sub = options.get('sub', 0)
    depth = options.get('depth', 8)
    w.u(options.get('vps', 0), 4)
    w.u(sub, 3)
    w.u(options.get('nesting', 1), 1)
    ptl(w, sub, variants=True)
    w.ue(options.get('id', 0))
    w.ue(chroma)
    if chroma == 3:
        w.u(options.get('separate', 0), 1)
    for value in (options.get('width', 64), options.get('height', 48)):
        w.ue(value)
    w.u(1, 1)
    for value in options.get('crop', (1, 2, 1, 2)):
        w.ue(value)
    w.ue(depth - 8)
    w.ue(0 if chroma == 0 else depth - 8)
    w.ue(options.get("poc", 12))
    w.u(1, 1)
    for _ in range(sub + 1):
        for value in (options.get("buffering", 15), options.get("reorder", 2), 0):
            w.ue(value)
    for value in options.get("blocks", (0, 3, 0, 3, 4, 4)):
        w.ue(value)
    scaling(w, options.get('scaling'), options)
    w.u(3, 2)
    pcm = options.get('pcm', False)
    w.u(int(pcm), 1)
    if pcm:
        w.u(options.get("pcm_luma", depth - 1), 4)
        w.u((8 if chroma == 0 else depth) - 1, 4)
        w.ue(0)
        w.ue(2)
        w.u(1, 1)
    reference_sets(w, options.get('references'), options)
    long_term = options.get('long_term', False)
    w.u(int(long_term), 1)
    if long_term:
        w.ue(options.get("long_count", 32))
        for i in range(32):
            w.u(65535 - i, 16)
            w.u(i % 2, 1)
    w.u(3, 2)
    vui(w, sub, options.get('vui'), options)
    extension = options.get('extension', 0)
    w.u(int(bool(extension)), 1)
    if extension:
        w.u(extension, 8)
        if extension == 128:
            w.u(511, 9)
    return finish(w, 33)


def controls():
    return [(name, make(**options)) for name, options in (
        ('minimal', {}), ('monochrome', {'chroma': 0}),
        ('separate-planes', {'chroma': 3, 'separate': 1, 'depth': 12}),
        ('sublayers', {'sub': 6, 'vps': 15}),
        ('scaling-prediction', {'scaling': 'prediction'}),
        ('scaling-dense', {'scaling': 'dense', 'depth': 16, 'chroma': 3, 'extension': 128}),
        ('pcm', {'pcm': True, 'depth': 12}),
        ('rps-predicted-zero', {'references': 'predicted'}),
        ('rps-maximum', {'references': 'maximum'}),
        ('long-term-32', {'long_term': True}),
        ('vui-wide-clocks', {'vui': 'metadata'}),
        ('vui-unspecified-sar', {'vui': 'metadata', 'sar_width': 0}),
        ('hrd-sublayers', {'sub': 6, 'vui': 'hrd'}),
        ('hrd-subpicture', {'sub': 6, 'vui': 'sub-hrd'}),
        ('wide', {'width': 16384, 'height': 8, 'crop': (0, 0, 0, 0)}),
    )]


def invalid_controls():
    rows = []
    for name, options in (
        ('sequence-id', {'id': 16}), ('chroma', {'chroma': 4}),
        ('sublayer-count', {'sub': 7}), ('nesting', {'nesting': 0}),
        ('precision', {'depth': 17}), ('poc-width', {'poc': 13}),
        ('crop-empty', {'crop': (32, 0, 0, 0)}),
        ('crop-u32', {'crop': (4294967294, 0, 0, 0)}),
        ('reorder', {'reorder': 16}), ('buffering', {'buffering': 16}),
        ('block-multiple', {'width': 65}), ('ctb-size', {'blocks': (0, 4, 0, 3, 4, 4)}),
        ('ctb-minimum', {'blocks': (0, 0, 0, 1, 1, 1)}),
        ('transform-size', {'blocks': (0, 3, 1, 3, 4, 4)}),
        ('scaling-prediction', {'scaling': 'prediction', 'scaling_delta': 1}),
        ('scaling-dc', {'scaling': 'dense', 'scaling_dc': 248}),
        ('scaling-coefficient', {'scaling': 'dense', 'scaling_coefficient': 128}),
        ('pcm-depth', {'pcm': True, 'pcm_luma': 8}),
        ('rps-count', {'rps_count': 65}),
        ('rps-references', {'references': 'predicted', 'negative_count': 16}),
        ('long-count', {'long_term': True, 'long_count': 33}),
        ('tick-zero', {'vui': 'metadata', 'tick': 0}),
        ('scale-zero', {'vui': 'metadata', 'time_scale': 0}),
        ('video-format', {'vui': 'metadata', 'video_flags': 31}),
        ('chroma-location', {'vui': 'metadata', 'chroma_location': 6}),
        ('mv-restriction', {'vui': 'metadata', 'max_mv': 16}),
        ('hrd-duration', {'vui': 'hrd', 'hrd_duration': 2048}),
        ('cpb-count', {'vui': 'hrd', 'cpb_count': 32}),
    ):
        rows.append((name, make(**options), 'Header'))
    for flags in (1, 8, 16, 32, 64, 255):
        rows.append(('extension-' + str(flags), make(extension=flags), 'Unsupported'))
    rows += [('unsigned-width', make(width=4294967294), 'Limit'),
             ('wide-area', make(width=65536, height=65536), 'Limit'),
             ('overlong-integer', make(id=4294967295), 'Limit')]
    return rows


def units(data):
    starts = list(re.finditer(b'\x00{2,}\x01', data))
    return [data[m.end():starts[i + 1].start() if i + 1 < len(starts) else len(data)].rstrip(b'\x00')
            for i, m in enumerate(starts)]


def metadata(f):
    chroma = f['chroma_format_idc']
    separate = f.get('separate_colour_plane_flag', 0)
    x, y = (1, 1) if separate or chroma == 0 else (1 if chroma == 3 else 2, 2 if chroma == 1 else 1)
    left, right, top, bottom = [f.get('conf_win_' + edge + '_offset', 0) * scale
                                for edge, scale in (('left', x), ('right', x), ('top', y), ('bottom', y))]
    width, height = f['pic_width_in_luma_samples'], f['pic_height_in_luma_samples']
    sub = f['sps_max_sub_layers_minus1']
    ratio_id = f.get('aspect_ratio_idc', 0)
    ratio = (f['sar_width'], f['sar_height']) if ratio_id == 255 else avc.RATIOS[ratio_id]
    compatibility = sum(f['general_profile_compatibility_flag[' + str(i) + ']'] << (31 - i) for i in range(32))
    result = [f['general_profile_space'], f['general_tier_flag'], f['general_profile_idc'], f['general_level_idc'], compatibility,
              f['sps_video_parameter_set_id'], f['sps_seq_parameter_set_id'], sub + 1, f['sps_temporal_id_nesting_flag'], chroma, separate,
              f['bit_depth_luma_minus8'] + 8, f['bit_depth_chroma_minus8'] + 8, width, height, width - left - right, height - top - bottom,
              left, top, right, bottom, f['log2_max_pic_order_cnt_lsb_minus4'] + 4,
              f['sps_max_dec_pic_buffering_minus1[' + str(sub) + ']'] + 1, f['sps_max_num_reorder_pics[' + str(sub) + ']'], *ratio,
              f.get('video_signal_type_present_flag', 0), f.get('colour_description_present_flag', 0), f.get('video_format', 5),
              f.get('video_full_range_flag', 0), f.get('colour_primaries', 2), f.get('transfer_characteristics', 2), f.get('matrix_coeffs', f.get('matrix_coefficients', 2)),
              f.get('field_seq_flag', 0), f.get('vui_num_units_in_tick', 0), f.get('vui_time_scale', 0)]
    assert len(result) == 36
    return result


def reference(binary, directory, name, sps, context=None):
    context = vps((sps[2] >> 4) & 15, (sps[2] >> 1) & 7) if context is None else context
    path = directory / (name + '.context')
    path.write_bytes(context + b'\x00\x00\x00\x01' + sps)
    process = subprocess.run([str(binary), str(path)], text=True, capture_output=True)
    log = process.stderr
    assert process.returncode == 0 and 'Failed to read unit' not in log and 'out of range' not in log, (name, log)
    assert 'Sequence Parameter Set' in log, (name, log)
    section = log.split('Sequence Parameter Set', 1)[1]
    fields = {}
    for key, value in re.findall(r'\[trace_headers[^\]]*\]\s+\d+\s+(\S+)\s+[01]+\s+=\s+(-?\d+)', section):
        fields.setdefault(key, int(value))
    assert fields['rbsp_stop_one_bit'] == 1
    return metadata(fields)


def generate(directory):
    for name, pixel_format, options in (
        ('main-crop', 'yuv420p', []), ('main10', 'yuv420p10le', []), ('main12', 'yuv420p12le', []),
        ('422-10', 'yuv422p10le', []), ('444-8', 'yuv444p', []), ('444-12', 'yuv444p12le', []),
        ('mono-12', 'gray12le', []),
        ('colour-sar', 'yuv420p10le', ['-vf', 'setsar=41/33,setparams=range=full:color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc']),
    ):
        path = directory / (name + '.hevc')
        raw = path.with_suffix('.yuv')
        subprocess.run(['ffmpeg', '-v', 'error', '-y', '-f', 'lavfi', '-i', 'testsrc2=size=66x50:rate=6',
                        '-frames:v', '3', '-threads', '1', '-pix_fmt', pixel_format, '-f', 'rawvideo', str(raw)], check=True)
        subprocess.run(['ffmpeg', '-v', 'error', '-y', '-f', 'rawvideo', '-pixel_format', pixel_format,
                        '-video_size', '66x50', '-framerate', '6', '-i', str(raw), '-frames:v', '3',
                        '-c:v', 'libx265', '-threads', '1', '-pix_fmt', pixel_format,
                        '-x265-params', 'pools=none:frame-threads=1:log-level=error:lossless=1', *options, '-f', 'hevc', str(path)], check=True)
        yield name, path


def main():
    trace = syntax.build_trace('HEVC', 'hevc-sps-trace')
    rows = []
    with tempfile.TemporaryDirectory() as temporary:
        directory = Path(temporary)
        for name, path in generate(directory):
            nal = units(path.read_bytes())
            sps = next(x for x in nal if x[0] >> 1 & 63 == 33)
            context = next(x for x in nal if x[0] >> 1 & 63 == 32)
            values = reference(trace, directory, name, sps, context)
            stream = json.loads(subprocess.check_output(['ffprobe', '-v', 'error', '-show_streams', '-of', 'json', str(path)]))['streams'][0]
            assert (values[15], values[16]) == (stream['width'], stream['height']), name
            rows.append((name, sps, values))
        for name, data in controls():
            rows.append((name, data, reference(trace, directory, name, data)))
    lines = ['// Generated by scripts/gen-video-hevc-fixtures.py through FFmpeg CBS and FFprobe.',
             'pub struct Vector { name: string, bytes: string, values: i64[] }', 'pub function vectors(): Vector[] {', '  return [']
    for name, data, values in rows:
        lines.append('    Vector { name: "' + name + '", bytes: "' + data.hex() + '", values: [' + ', '.join(str(x) + 'i64' for x in values) + '] },')
    lines += ['  ];', '}']
    lines += ['pub struct Invalid { name: string, bytes: string, error: string }', 'pub function invalid(): Invalid[] {', '  return [']
    for name, data, error in invalid_controls():
        lines.append('    Invalid { name: "' + name + '", bytes: "' + data.hex() + '", error: "' + error + '" },')
    lines += ['  ];', '}']
    target = ROOT / 'tests/stdlib/video_hevc_vectors.fern'
    target.write_text('\n'.join(lines) + '\n')
    compiler = os.environ.get('FERN') or subprocess.check_output([str(ROOT / 'bootstrap/bootstrap.sh'), 'stage0'], text=True).strip()
    subprocess.run([compiler, '-fmt', '-w', str(target)], check=True)
    print('Wrote', len(rows), 'HEVC SPS reference headers')


if __name__ == '__main__':
    main()
