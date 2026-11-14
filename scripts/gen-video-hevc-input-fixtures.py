#!/usr/bin/env python3
"""Generate HEVC picture-boundary controls without entropy-data conformance claims."""
import importlib.util
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / (name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


hevc = load("gen-video-hevc-fixtures")
pps = load("gen-video-hevc-pps-fixtures")


def sequence(**options):
    return hevc.make(**(dict(width=64, height=48, crop=(0, 0, 0, 0)) | options))


def opaque(kind, payload=b"\x80", temporal=0, layer=0):
    return bytes([kind << 1 | layer >> 5, (layer & 31) << 3 | temporal + 1]) + payload


def slice_nal(kind=1, first=True, parameter=0, temporal=0, dependent=False):
    writer = hevc.syntax.Writer()
    writer.u(int(first), 1)
    if 16 <= kind <= 23:
        writer.u(0, 1)
    writer.ue(parameter)
    if not first:
        writer.u(int(dependent), 1)
        writer.u(1, 2)
    writer.u(5, 4)
    value = hevc.finish(writer, kind)
    return value[:1] + bytes([temporal + 1]) + value[2:]


def temporal_pps(temporal, **options):
    value = pps.make(**options)
    return value[:1] + bytes([temporal + 1]) + value[2:]


def controls():
    s, p = sequence(), pps.make()
    yield "multiple slices and repeated first flags", [[s, p, slice_nal(), slice_nal(first=False)], [slice_nal()]]
    yield "IRAP differs from IDR", [[s, p, slice_nal(kind=16), slice_nal(kind=16, first=False)]] + [
        [slice_nal(kind=kind)] for kind in range(17, 22)]
    yield "VCL types and temporal layers", [[sequence(sub=2), p, slice_nal(kind=0, temporal=2),
                                            slice_nal(kind=0, temporal=2, first=False)],
                                           [slice_nal(temporal=1)], [slice_nal(kind=6)], [slice_nal(kind=8, temporal=1)]]
    yield "PPS temporal identifiers follow their pictures", [[sequence(sub=6), p, slice_nal()]] + [
        [temporal_pps(temporal), slice_nal(temporal=temporal)] for temporal in range(1, 7)]
    yield "unused higher PPS does not reject a lower picture", [
        [sequence(sub=6), p, temporal_pps(5, id=1), slice_nal()], [slice_nal(temporal=5, parameter=1)]]
    yield "stale PPS retains its temporal identifier", [
        [sequence(sub=6), temporal_pps(3), pps.make(id=1), slice_nal(temporal=3)],
        [sequence(sub=4), slice_nal(parameter=1)], [slice_nal(temporal=3)]]
    yield "PPS replacement updates its temporal identifier", [
        [sequence(sub=6), temporal_pps(3), slice_nal(temporal=3)],
        [temporal_pps(1), slice_nal(temporal=1)], [p, slice_nal()]]
    aud = opaque(35, b"\x50")
    yield "AUD and leading metadata", [[aud, hevc.vps(), s, p, opaque(39), slice_nal(kind=19)],
                                       [aud, opaque(39), slice_nal()]]
    yield "prefix SEI starts the next picture", [[s, p, slice_nal()], [opaque(39), slice_nal()]]
    yield "suffix filler and both end markers stay with the picture", [
        [s, p, slice_nal(), opaque(40), opaque(38, b"\xff\x80"), opaque(36, b""), opaque(37, b"")]]
    yield "EOS starts the next coded sequence with IRAP", [[s, p, slice_nal(), opaque(36, b"")], [slice_nal(kind=19)]]
    yield "EOB ends the last coded picture", [[s, p, slice_nal(), opaque(37, b"")]]
    yield "dependent segments stay with the first slice", [[s, pps.make(dependent=1), slice_nal(),
                                                           slice_nal(first=False, dependent=True)], [slice_nal()]]
    yield "maximum parameter identifiers", [[sequence(id=15), pps.make(id=63, sequence=15), slice_nal(parameter=63)]]
    yield "all parameter cache slots", [[sequence(id=i) for i in range(16)]
                                        + [pps.make(id=i, sequence=i % 16) for i in range(64)]
                                        + [slice_nal()], [slice_nal(parameter=63)]]
    ten, eight, low = sequence(depth=10), sequence(depth=8), pps.make(qp=-38)
    yield "compatible SPS replacement keeps the cached PPS", [[ten, p, slice_nal()], [eight, slice_nal()]]
    yield "new PPS replaces an invalid old record", [[ten, low, slice_nal()], [eight, p, slice_nal()]]
    yield "unused stale PPS does not reject another picture", [[ten, low, pps.make(id=1), slice_nal()],
                                                              [eight, slice_nal(parameter=1)]]
    yield "old PPS becomes valid again", [[ten, low, pps.make(id=1), slice_nal()],
                                          [eight, slice_nal(parameter=1)], [ten, slice_nal()]]
    yield "separate sequences keep their PPS associations", [[ten, sequence(id=1), low,
                                                             pps.make(id=1, sequence=1), slice_nal()],
                                                            [sequence(id=1, depth=10), slice_nal(parameter=1)], [slice_nal()]]
    yield "tiles and entropy synchronization retain packet bytes", [[sequence(width=128, height=128),
                                                                     pps.make(tiles=1, columns=2, rows=2, sync=1),
                                                                     slice_nal(), slice_nal(first=False)], [slice_nal()]]
    yield "twelve-bit parameter context", [[sequence(depth=12, chroma=3), pps.make(qp=-50), slice_nal(kind=20)]]


def incompatible_pairs():
    yield "cached PPS follows the current bit depth", sequence(depth=10), sequence(depth=8), pps.make(qp=-38), "syntax"
    yield "cached PPS follows the current tile grid", sequence(width=128, height=128), sequence(width=64, height=128), pps.make(tiles=1, columns=2), "tile_grid"


def invalid():
    s, p = sequence(), pps.make()
    yield "slice requires its PPS", [slice_nal()], "State"
    yield "PPS requires its SPS", [p], "State"
    yield "prefix has no picture", [s, p], "Truncated"
    yield "NAL header is incomplete", [b"\x02"], "Truncated"
    yield "slice prefix is incomplete", [s, p, b"\x02\x01"], "Truncated"
    yield "forbidden header bit", [b"\x82\x01\x80"], "Header"
    yield "temporal identifier is zero", [b"\x02\x00\x80"], "Header"
    yield "nonzero layers remain unsupported", [opaque(1, layer=1)], "Unsupported"
    for kind in (10, 22, 41, 48):
        yield f"reserved NAL type {kind}", [opaque(kind)], "Unsupported"
    yield "IRAP temporal layer must be zero", [s, p, slice_nal(kind=19, temporal=1)], "Header"
    yield "temporal layer must fit its SPS", [s, p, slice_nal(temporal=1)], "Header"
    yield "VPS temporal identifier must be zero", [opaque(32, temporal=1)], "Header"
    higher_sps = sequence(sub=6)
    yield "SPS temporal identifier must be zero", [higher_sps[:1] + b"\x02" + higher_sps[2:]], "Header"
    yield "active PPS cannot exceed its picture temporal identifier", [higher_sps, temporal_pps(1), slice_nal()], "Header"
    yield "IRAP cannot activate a higher temporal PPS", [higher_sps, temporal_pps(1), slice_nal(kind=19)], "Header"
    yield "cached PPS temporal identifier survives SPS replacement", [
        higher_sps, temporal_pps(3), slice_nal(temporal=3), sequence(sub=4), slice_nal()], "Header"
    yield "separate color planes remain unsupported", [sequence(chroma=3, separate=1)], "Unsupported"
    yield "a picture needs its first segment", [s, p, slice_nal(first=False)], "Header"
    yield "subsequent segment keeps its PPS", [s, p, pps.make(id=1), slice_nal(), slice_nal(first=False, parameter=1)], "Header"
    yield "subsequent segment keeps its NAL type", [s, p, slice_nal(), slice_nal(kind=3, first=False)], "Header"
    yield "subsequent segment keeps its temporal layer", [sequence(sub=1), p, slice_nal(), slice_nal(first=False, temporal=1)], "Header"
    yield "end marker needs a picture", [opaque(36, b"")], "Header"
    yield "suffix cannot precede the first picture", [s, p, opaque(40)], "Header"
    yield "filler cannot precede the first picture", [s, p, opaque(38, b"\xff\x80")], "Header"
    yield "suffix cannot follow an end marker", [s, p, slice_nal(), opaque(36, b""), opaque(40)], "Header"
    yield "new picture after EOS needs its first segment", [s, p, slice_nal(), opaque(36, b""), slice_nal(kind=19, first=False)], "Header"
    yield "new picture after EOS must be IRAP", [s, p, slice_nal(), opaque(36, b""), slice_nal()], "Header"
    yield "EOB permits no later NAL", [s, p, slice_nal(), opaque(37, b""), slice_nal(kind=19)], "Header"
    yield "AUD picture type is reserved", [opaque(35, b"\x70")], "Header"
    yield "AUD trailing bits are exact", [opaque(35, b"\x11")], "Header"
    yield "end marker has no RBSP payload", [opaque(37)], "Header"
    yield "AUD is the first unit of its access unit", [s, opaque(35, b"\x50")], "Header"
    yield "one AUD per access unit", [opaque(35, b"\x50"), opaque(35, b"\x50")], "Header"
    yield "PPS identifier is bounded", [s, pps.make(id=64)], "Header"
    yield "trailing parameter records need a picture", [s, p, slice_nal(), s], "Truncated"
    for name, before, after, picture, _ in incompatible_pairs():
        yield name, [before, picture, slice_nal(), after, slice_nal()], "Header"


def packed(groups):
    data, offsets, irap, idr, packets = bytearray(), [], [], [], []
    count = 0
    for group in groups:
        offsets.append(len(data))
        kind = next(unit[0] >> 1 & 63 for unit in group if unit[0] >> 1 & 63 < 32)
        irap.append(16 <= kind <= 21)
        idr.append(kind in (19, 20))
        packets.append(b"".join(b"\0\0\0\1" + unit for unit in group).hex())
        for unit in group:
            data += (b"\0\0\0\0\1" if count % 2 == 0 else b"\0\0\1") + unit
            count += 1
    data += b"\0\0"
    return data.hex(), offsets, irap, idr, packets


def main():
    lines = ['// Generated by scripts/gen-video-hevc-input-fixtures.py.',
             '// Slice data is opaque. These controls do not establish entropy or pixel conformance.',
             'import "std/video/frame";',
             'pub struct Vector { name: string, bytes: string, offsets: i64[], irap: boolean[], idr: boolean[], packets: string[] }',
             'pub struct Invalid { name: string, bytes: string, error: frame.VideoError }',
             'pub function vectors(): Vector[] {', '  return [']
    for name, groups in controls():
        data, offsets, irap, idr, packets = packed(groups)
        numbers = ', '.join(str(n) + 'i64' for n in offsets)
        bools = lambda values: ', '.join(str(b).lower() for b in values)
        encoded = ', '.join('"' + p + '"' for p in packets)
        lines.append(f'    Vector {{ name: "{name}", bytes: "{data}", offsets: [{numbers}], irap: [{bools(irap)}], idr: [{bools(idr)}], packets: [{encoded}] }},')
    lines += ['  ];', '}', 'pub function invalid(): Invalid[] {', '  return [']
    for name, units, error in invalid():
        data = b''.join(b'\0\0\0\1' + unit for unit in units).hex()
        lines.append(f'    Invalid {{ name: "{name}", bytes: "{data}", error: frame.{error} }},')
    lines += ['  ];', '}']
    (ROOT / 'tests/stdlib/video_hevc_input_vectors.fern').write_text('\n'.join(lines) + '\n')


if __name__ == '__main__':
    main()
