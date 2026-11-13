#!/usr/bin/env python3
"""Generate AVC picture-identity controls without an entropy-data conformance claim."""
import importlib.util
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / (name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


syntax = load("gen-video-avc-syntax-fixtures")
pps = load("gen-video-avc-pps-fixtures")


def sequence(**options):
    return syntax.make(profile=66, width=3, height=2, crop=(0, 0, 0, 0), **options)


def slice_nal(*, first=0, frame=1, parameter=0, reference=2, idr=False, identity=0,
              order=2, a=0, b=0, bottom=False, zero=False):
    w = syntax.Writer()
    w.ue(first)
    w.ue(2 if idr else 0)
    w.ue(parameter)
    w.u(frame, 4)
    if idr:
        w.ue(identity)
    if order == 0:
        w.u(a, 4)
        if bottom:
            w.se(b)
    elif order == 1 and not zero:
        w.se(a)
        if bottom:
            w.se(b)
    return w.finish(reference << 5 | (5 if idr else 1))


def packed(groups):
    data = bytearray()
    offsets, flags, packets = [], [], []
    number = 0
    for group in groups:
        offsets.append(len(data))
        flags.append(any(unit[0] & 31 == 5 for unit in group))
        packets.append(b"".join(b"\0\0\0\1" + unit for unit in group).hex())
        for unit in group:
            data += (b"\0\0\0\0\1" if number % 2 == 0 else b"\0\0\1") + unit
            number += 1
    data += b"\0\0"
    return data.hex(), offsets, flags, packets


def controls():
    s, p = sequence(poc=2), pps.make()
    yield "frame and reference status", [[s, p, slice_nal(frame=1), slice_nal(first=6, frame=1, reference=3)],
                                          [slice_nal(frame=2)], [slice_nal(frame=2, reference=0)],
                                          [slice_nal(frame=3, reference=0)]]
    yield "IDR identity", [[s, p, slice_nal(frame=0, idr=True, identity=0)],
                           [slice_nal(frame=0, idr=True, identity=1)]]
    yield "POC least significant bits", [[sequence(poc=0), p, slice_nal(order=0, a=0, reference=0)],
                                         [slice_nal(order=0, a=2, reference=0)]]
    yield "POC bottom delta", [[sequence(poc=0), pps.make(bottom=1), slice_nal(order=0, bottom=True)],
                               [slice_nal(order=0, bottom=True, b=-1)]]
    yield "POC first delta", [[sequence(poc=1), p, slice_nal(order=1)], [slice_nal(order=1, a=1)]]
    yield "POC second delta", [[sequence(poc=1), pps.make(bottom=1), slice_nal(order=1, bottom=True)],
                               [slice_nal(order=1, bottom=True, b=-1)]]
    yield "POC always zero", [[sequence(poc=1, delta_zero=1), p, slice_nal(order=1, zero=True)],
                              [slice_nal(order=1, zero=True, frame=2)]]
    yield "PPS identity", [[s, p, pps.make(id=1), slice_nal(parameter=0)], [slice_nal(parameter=1)]]
    sei = b"\x06\x05\x10" + bytes(range(1, 17)) + b"\x80"
    yield "SEI belongs to next packet", [[s, p, slice_nal(frame=1)], [sei, slice_nal(frame=2)]]
    yield "AUD belongs to next packet", [[b"\x09\xf0", s, p, slice_nal(frame=1)],
                                         [b"\x09\xf0", slice_nal(frame=2)]]
    yield "sequence end forces a boundary", [[s, p, slice_nal(frame=0, idr=True), b"\x0a\x80"],
                                             [slice_nal(frame=0, idr=True)]]
    yield "parameter replacement", [[s, p, slice_nal(frame=1)],
                                    [sequence(poc=0), pps.make(bottom=1), slice_nal(order=0, bottom=True, b=1)]]
    yield from replacement_controls()


def replacement_sequence(**options):
    return syntax.make(**({'profile': 244, 'poc': 2, 'width': 3, 'height': 2,
                          'crop': (0, 0, 0, 0)} | options))


def replacement_controls():
    ten = replacement_sequence(depth=10)
    eight = replacement_sequence(depth=8)
    low, plain = pps.make(qp=-38), pps.make()
    first = [ten, low, slice_nal(frame=1)]
    yield "cached PPS accepts a compatible SPS replacement", [[ten, plain, slice_nal(frame=1)],
                                                              [eight, slice_nal(frame=2)]]
    yield "new PPS replaces the stale PPS before use", [first, [eight, plain, slice_nal(frame=2)]]
    yield "unused stale PPS does not reject another PPS", [[ten, low, pps.make(id=1), slice_nal(frame=1)],
                                                           [eight, slice_nal(frame=2, parameter=1)]]
    yield "cached PPS becomes valid again", [[ten, low, pps.make(id=1), slice_nal(frame=1)],
                                             [eight, slice_nal(frame=2, parameter=1)],
                                             [ten, slice_nal(frame=3)]]
    yield "repeated SPS keeps a cached PPS", [first, [ten, slice_nal(frame=2)]]
    scaled = pps.make(extension=True, transform=1, scaling=True, chroma=1)
    yield "scaled PPS survives a compatible depth change", [[ten, scaled, slice_nal(frame=1)],
                                                            [eight, slice_nal(frame=2)]]
    yield "separate SPS entries keep their PPS association", [[ten, replacement_sequence(id=1), low,
                                                              pps.make(id=1, sequence=1), slice_nal(frame=1)],
                                                             [replacement_sequence(id=1, depth=10),
                                                              slice_nal(frame=2, parameter=1)]]
    yield "all PPS cache entries survive repeated replacements", [
        [ten] + [pps.make(id=i) for i in range(256)] + [slice_nal(frame=1)],
        [eight, slice_nal(frame=2, parameter=255)],
        [ten, slice_nal(frame=3, parameter=0)],
        [eight, slice_nal(frame=4, parameter=127)],
    ]


def incompatible_pairs():
    yield "cached PPS follows the current bit depth", replacement_sequence(depth=10), replacement_sequence(depth=8), pps.make(qp=-38), "Header"
    yield "cached PPS follows the current scaling syntax", replacement_sequence(chroma=1), replacement_sequence(chroma=3), pps.make(extension=True, transform=1, scaling=True, chroma=1), "Truncated"


def invalid():
    s, p = sequence(poc=2), pps.make()
    yield "slice requires its PPS", [slice_nal()], "State"
    yield "PPS requires its SPS", [pps.make(sequence=1)], "State"
    yield "prefix has no picture", [s, p], "Truncated"
    yield "slice prefix is incomplete", [s, p, b"\x65"], "Truncated"
    yield "forbidden header bit", [s, p, bytes([0xC1, 0xFF])], "Header"
    yield "IDR must be referenced", [s, p, slice_nal(idr=True, reference=0)], "Header"
    yield "SEI must not be referenced", [s, p, b"\x66\x80"], "Header"
    yield "macroblock is outside the coded picture", [s, p, slice_nal(first=12)], "Header"
    yield "partitioned slices remain unsupported", [s, p, b"\x42\x80"], "Unsupported"
    yield "extended slices remain unsupported", [s, p, b"\x74\x80"], "Unsupported"
    yield "field pictures remain unsupported", [sequence(poc=2, only=0), p], "Unsupported"
    yield "slice groups remain unsupported", [s, pps.make(groups=2)], "Unsupported"
    yield "redundant pictures remain unsupported", [s, pps.make(redundant=1)], "Unsupported"
    for name, before, after, picture, category in incompatible_pairs():
        yield name, [before, picture, slice_nal(frame=1), after, slice_nal(frame=2)], category
    yield "full PPS cache drops on a stale parameter error", (
        [replacement_sequence(depth=10)]
        + [pps.make(id=i, qp=-38 if i == 255 else 0) for i in range(256)]
        + [slice_nal(frame=1), replacement_sequence(depth=8), slice_nal(frame=2, parameter=255)]
    ), "Header"


def main():
    lines = ['// Generated by scripts/gen-video-avc-input-fixtures.py.',
             '// Slice data is opaque. These controls do not establish entropy or pixel conformance.',
             'import "std/video/frame";',
             'pub struct Vector { name: string, bytes: string, offsets: i64[], idr: boolean[], packets: string[] }',
             'pub struct Invalid { name: string, bytes: string, error: frame.VideoError }',
             'pub function vectors(): Vector[] {', '  return [']
    for name, groups in controls():
        data, offsets, flags, packets = packed(groups)
        numbers = ', '.join(str(n) + 'i64' for n in offsets)
        bools = ', '.join(str(b).lower() for b in flags)
        encoded = ', '.join('"' + p + '"' for p in packets)
        lines.append(f'    Vector {{ name: "{name}", bytes: "{data}", offsets: [{numbers}], idr: [{bools}], packets: [{encoded}] }},')
    lines += ['  ];', '}', 'pub function invalid(): Invalid[] {', '  return [']
    for name, units, error in invalid():
        data = b''.join(b'\0\0\0\1' + unit for unit in units).hex()
        lines.append(f'    Invalid {{ name: "{name}", bytes: "{data}", error: frame.{error} }},')
    lines += ['  ];', '}']
    (ROOT / 'tests/stdlib/video_avc_input_vectors.fern').write_text('\n'.join(lines) + '\n')


if __name__ == '__main__':
    main()
