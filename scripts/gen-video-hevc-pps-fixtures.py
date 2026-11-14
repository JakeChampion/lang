#!/usr/bin/env python3
"""Create HEVC PPS controls through the independent FFmpeg CBS reader."""
import importlib.util
import os
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('hevc', ROOT / 'scripts/gen-video-hevc-fixtures.py')
hevc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hevc)

# Fern field, independent CBS field, absent-field default, result offset.
FIELDS = [
    ('picture_id', 'pps_pic_parameter_set_id', 0, 0), ('sequence_id', 'pps_seq_parameter_set_id', 0, 0),
    ('dependent_slice_segments', 'dependent_slice_segments_enabled_flag', 0, 0), ('output_flag_present', 'output_flag_present_flag', 0, 0),
    ('extra_slice_header_bits', 'num_extra_slice_header_bits', 0, 0), ('sign_data_hiding', 'sign_data_hiding_enabled_flag', 0, 0),
    ('cabac_init_present', 'cabac_init_present_flag', 0, 0), ('default_l0_references', 'num_ref_idx_l0_default_active_minus1', 0, 1),
    ('default_l1_references', 'num_ref_idx_l1_default_active_minus1', 0, 1), ('initial_qp', 'init_qp_minus26', 0, 26),
    ('constrained_intra', 'constrained_intra_pred_flag', 0, 0), ('transform_skip', 'transform_skip_enabled_flag', 0, 0),
    ('cu_qp_delta', 'cu_qp_delta_enabled_flag', 0, 0), ('qp_delta_depth', 'diff_cu_qp_delta_depth', 0, 0),
    ('cb_qp_offset', 'pps_cb_qp_offset', 0, 0), ('cr_qp_offset', 'pps_cr_qp_offset', 0, 0),
    ('slice_chroma_qp_offsets', 'pps_slice_chroma_qp_offsets_present_flag', 0, 0), ('weighted_prediction', 'weighted_pred_flag', 0, 0),
    ('weighted_biprediction', 'weighted_bipred_flag', 0, 0), ('transquant_bypass', 'transquant_bypass_enabled_flag', 0, 0),
    ('tiles', 'tiles_enabled_flag', 0, 0), ('entropy_coding_sync', 'entropy_coding_sync_enabled_flag', 0, 0),
    ('tile_columns', 'num_tile_columns_minus1', 0, 1), ('tile_rows', 'num_tile_rows_minus1', 0, 1),
    ('uniform_spacing', 'uniform_spacing_flag', 1, 0), ('loop_filter_across_tiles', 'loop_filter_across_tiles_enabled_flag', 1, 0),
    ('loop_filter_across_slices', 'pps_loop_filter_across_slices_enabled_flag', 0, 0), ('deblock_control', 'deblocking_filter_control_present_flag', 0, 0),
    ('deblock_override', 'deblocking_filter_override_enabled_flag', 0, 0), ('deblock_disabled', 'pps_deblocking_filter_disabled_flag', 0, 0),
    ('beta_offset_div2', 'pps_beta_offset_div2', 0, 0), ('tc_offset_div2', 'pps_tc_offset_div2', 0, 0),
    ('scaling_lists', 'pps_scaling_list_data_present_flag', 0, 0), ('lists_modification', 'lists_modification_present_flag', 0, 0),
    ('parallel_merge_log2', 'log2_parallel_merge_level_minus2', 0, 2), ('slice_header_extension', 'slice_segment_header_extension_present_flag', 0, 0),
    ('range_extension', 'pps_range_extension_flag', 0, 0), ('transform_skip_log2', 'log2_max_transform_skip_block_size_minus2', 0, 2),
    ('cross_component_prediction', 'cross_component_prediction_enabled_flag', 0, 0), ('chroma_qp_offset_list', 'chroma_qp_offset_list_enabled_flag', 0, 0),
    ('chroma_qp_depth', 'diff_cu_chroma_qp_offset_depth', 0, 0), ('chroma_qp_entries', 'chroma_qp_offset_list_len_minus1', -1, 1),
    ('sao_luma_scale', 'log2_sao_offset_scale_luma', 0, 0), ('sao_chroma_scale', 'log2_sao_offset_scale_chroma', 0, 0),
]
BOOLEANS = {x[0] for x in FIELDS if x[1].endswith('_flag')}


def make(**options):
    w = hevc.syntax.Writer()
    u = lambda key, width=1, default=0: w.u(options.get(key, default), width)
    ue = lambda key, default=0: w.ue(options.get(key, default))
    se = lambda key, default=0: w.se(options.get(key, default))
    ue('id'); ue('sequence')
    u('dependent'); u('output'); u('extra', 3); u('sign'); u('cabac')
    ue('l0'); ue('l1'); se('qp')
    u('intra'); u('skip'); u('cu_qp')
    if options.get('cu_qp'): ue('qp_depth')
    se('cb'); se('cr')
    for key in ('slice_chroma', 'weighted', 'bipred', 'bypass', 'tiles', 'sync'): u(key)
    if options.get('tiles'):
        columns, rows = options.get('columns', 1), options.get('rows', 1)
        w.ue(columns - 1); w.ue(rows - 1)
        u('uniform', default=1)
        if not options.get('uniform', 1):
            for extent in options.get('widths', [1] * (columns - 1)): w.ue(extent - 1)
            for extent in options.get('heights', [1] * (rows - 1)): w.ue(extent - 1)
        u('tile_filter', default=1)
    u('slice_filter'); u('deblock')
    if options.get('deblock'):
        u('override'); u('disabled')
        if not options.get('disabled'): se('beta'); se('tc')
    u('scaling')
    if options.get('scaling'):
        matrix = hevc.syntax.Writer()
        hevc.scaling(matrix, options.get('scaling_mode', 'dense'), options)
        w.bits += matrix.bits[2:]
    u('lists'); ue('merge'); u('slice_extension')
    extension = options.get('extension', 0)
    w.u(int(bool(extension)), 1)
    if extension:
        w.u(extension, 8)
        if extension == 128:
            if options.get('skip'): ue('skip_max')
            u('cross'); u('chroma_list')
            if options.get('chroma_list'):
                ue('chroma_depth')
                values = options.get('chroma_offsets', [(0, 0)])
                w.ue(len(values) - 1)
                for cb, cr in values: w.se(cb); w.se(cr)
            ue('sao_luma'); ue('sao_chroma')
    return hevc.finish(w, 34)


def controls():
    base = dict(width=256, height=192)
    yield 'minimal', make(), hevc.make(**base)
    for columns in range(1, 5):
        for rows in range(1, 4):
            if columns == rows == 1: continue
            for uniform in (0, 1):
                yield f'tiles-{columns}x{rows}-{uniform}', make(tiles=1, columns=columns, rows=rows, uniform=uniform), hevc.make(**base)
    cases = [
        ('flags', dict(id=63, sequence=15, dependent=1, output=1, extra=7, sign=1, cabac=1, l0=14, l1=14,
                       qp=25, intra=1, skip=1, cu_qp=1, qp_depth=3, cb=-12, cr=12, slice_chroma=1,
                       weighted=1, bipred=1, bypass=1, sync=1, slice_filter=1, deblock=1, override=1,
                       beta=-6, tc=6, lists=1, merge=4, slice_extension=1), dict(id=15)),
        ('qp8-min', dict(qp=-26), {}), ('qp12-min', dict(qp=-50), dict(depth=12)),
        ('deblock-disabled', dict(deblock=1, override=1, disabled=1), {}),
        ('scaling-dense', dict(scaling=1), dict(scaling='dense')),
        ('scaling-predicted', dict(scaling=1, scaling_mode='predicted'), dict(scaling='predicted')),
        ('range-max', dict(skip=1, extension=128, skip_max=3, cross=1, chroma_list=1, chroma_depth=3,
                           chroma_offsets=[(-12, 12), (12, -12)] * 3, sao_luma=2, sao_chroma=2), dict(chroma=3, depth=12)),
        ('range-mono', dict(extension=128, sao_luma=2), dict(chroma=0, depth=12)),
        ('transform4', dict(skip=1, extension=128, skip_max=0), dict(blocks=(0, 3, 0, 0, 4, 4))),
    ]
    for name, options, sequence in cases:
        yield name, make(**options), hevc.make(**(base | sequence))


def invalid_controls():
    base = dict(width=256, height=192)
    cases = [
        ('picture-id', dict(id=64), {}), ('sequence-id', dict(sequence=16), {}),
        ('sequence-mismatch', dict(sequence=1), {}), ('l0', dict(l0=15), {}), ('l1', dict(l1=15), {}),
        ('qp8-low', dict(qp=-27), {}), ('qp-high', dict(qp=26), {}),
        ('qp12-low', dict(qp=-51), dict(depth=12)), ('cb', dict(cb=-13), {}), ('cr', dict(cr=13), {}),
        ('qp-depth', dict(cu_qp=1, qp_depth=4), {}),
        ('tile-columns', dict(tiles=1, columns=5), {}), ('tile-rows', dict(tiles=1, rows=4), {}),
        ('single-tile', dict(tiles=1), {}),
        ('tile-no-last-column', dict(tiles=1, columns=2, uniform=0, widths=[4]), {}),
        ('tile-late-column', dict(tiles=1, columns=4, uniform=0, widths=[1, 1, 2]), {}),
        ('tile-no-last-row', dict(tiles=1, rows=2, uniform=0, heights=[3]), {}),
        ('beta', dict(deblock=1, beta=7), {}), ('tc', dict(deblock=1, tc=-7), {}),
        ('merge', dict(merge=5), {}),
        ('transform-skip', dict(skip=1, extension=128, skip_max=4), {}),
        ('transform-skip-sps-bound', dict(skip=1, extension=128, skip_max=1), dict(blocks=(0, 3, 0, 0, 4, 4))),
        ('cross-420', dict(extension=128, cross=1), {}),
        ('cross-separate', dict(extension=128, cross=1), dict(chroma=3, separate=1)),
        ('list-monochrome', dict(extension=128, chroma_list=1), dict(chroma=0)),
        ('chroma-depth', dict(extension=128, chroma_list=1, chroma_depth=4), {}),
        ('chroma-count', dict(extension=128, chroma_list=1, chroma_offsets=[(0, 0)] * 7), {}),
        ('chroma-offset', dict(extension=128, chroma_list=1, chroma_offsets=[(-13, 0)]), {}),
        ('sao8', dict(extension=128, sao_luma=1), {}),
        ('sao12', dict(extension=128, sao_chroma=3), dict(depth=12)),
    ]
    for name, options, sequence in cases:
        yield name, make(**options), hevc.make(**(base | sequence)), 'Header'
    for extension in (64, 32, 16, 1):
        yield f'extension-{extension}', make(extension=extension), hevc.make(**base), 'Unsupported'
    w = hevc.syntax.Writer()
    w.bits = '0' * 32
    yield 'overlong-ue', hevc.finish(w, 34), hevc.make(**base), 'Limit'


def reference(binary, directory, name, pps, sps, context=None, temporal_id_plus1=None):
    if context is None:
        context = hevc.vps((sps[2] >> 4) & 15, (sps[2] >> 1) & 7)
    path = directory / (name + '.context')
    path.write_bytes(context + b'\0\0\0\1' + sps + b'\0\0\0\1' + pps)
    result = subprocess.run([str(binary), str(path)], text=True, capture_output=True)
    log = result.stderr
    assert result.returncode == 0 and 'Failed to read unit' not in log and 'out of range' not in log, (name, log)
    assert 'Picture Parameter Set' in log, (name, log)
    section = log.split('Picture Parameter Set', 1)[1]
    if temporal_id_plus1 is not None:
        header = re.findall(r'nuh_temporal_id_plus1\s+[01]+\s+=\s+(\d+)', section)
        assert list(map(int, header)) == [temporal_id_plus1], (name, section)
    fields = {}
    for key, value in re.findall(r'\[trace_headers[^\]]*\]\s+\d+\s+(\S+)\s+[01]+\s+=\s+(-?\d+)', section):
        fields.setdefault(key, int(value))
    assert fields['rbsp_stop_one_bit'] == 1
    return [fields.get(key, default) + offset for _, key, default, offset in FIELDS]


def main():
    trace = hevc.syntax.build_trace('HEVC', 'hevc-pps-trace')
    rows = []
    with tempfile.TemporaryDirectory(prefix='fern-hevc-pps-') as tmp:
        directory = Path(tmp)
        for name, path in hevc.generate(directory):
            units = hevc.units(path.read_bytes())
            sps = next(x for x in units if x[0] >> 1 & 63 == 33)
            pps = next(x for x in units if x[0] >> 1 & 63 == 34)
            vps = next(x for x in units if x[0] >> 1 & 63 == 32)
            rows.append((name, pps, sps, reference(trace, directory, name, pps, sps, b'\0\0\0\1' + vps)))
        for name, pps, sps in controls():
            rows.append((name, pps, sps, reference(trace, directory, name, pps, sps)))
    lines = ['// Generated by scripts/gen-video-hevc-pps-fixtures.py through FFmpeg CBS.',
             'pub struct Vector { name: string, bytes: string, sps: string, values: i64[] }', 'pub function vectors(): Vector[] {', '  return [']
    for name, pps, sps, values in rows:
        lines.append('    Vector { name: "' + name + '", bytes: "' + pps.hex() + '", sps: "' + sps.hex() + '", values: [' + ', '.join(str(x) + 'i64' for x in values) + '] },')
    lines += ['  ];', '}']
    lines += ['pub struct Invalid { name: string, bytes: string, sps: string, error: string }', 'pub function invalid(): Invalid[] {', '  return [']
    for name, pps, sps, error in invalid_controls():
        lines.append('    Invalid { name: "' + name + '", bytes: "' + pps.hex() + '", sps: "' + sps.hex() + '", error: "' + error + '" },')
    lines += ['  ];', '}']
    (ROOT / 'tests/stdlib/video_hevc_pps_vectors.fern').write_text('\n'.join(lines) + '\n')
    checks = ['// Serialize HEVC PPS metadata for independent comparisons.', 'import "std/video/hevc/pps";',
              'function flag(value: boolean): i64 { return if (value) { 1i64 } else { 0i64 }; }',
              'pub function values(p: pps.PictureParameters): i64[] {', '  return [']
    checks += ['    ' + ('flag(p.' + name + ')' if name in BOOLEANS else 'p.' + name + ' as i64') + ',' for name, *_ in FIELDS]
    checks += ['  ];', '}']
    (ROOT / 'tests/stdlib/video_hevc_pps_check.fern').write_text('\n'.join(checks) + '\n')
    compiler = os.environ.get('FERN') or subprocess.check_output([str(ROOT / 'bootstrap/bootstrap.sh'), 'stage0'], text=True).strip()
    subprocess.run([compiler, '-fmt', '-w', str(ROOT / 'tests/stdlib/video_hevc_pps_vectors.fern'),
                    str(ROOT / 'tests/stdlib/video_hevc_pps_check.fern')], check=True)
    print('Wrote', len(rows), 'PPS controls with', len(FIELDS), 'independent fields', flush=True)


if __name__ == '__main__':
    main()
