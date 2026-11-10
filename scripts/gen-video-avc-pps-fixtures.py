#!/usr/bin/env python3
"""Generate AVC PPS controls with FFmpeg's independent coded-bitstream reader."""
import importlib.util
import os
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("avc_syntax", ROOT / "scripts/gen-video-avc-syntax-fixtures.py")
syntax = importlib.util.module_from_spec(spec)
spec.loader.exec_module(syntax)
references = syntax.references


def make(**options):
    w = syntax.Writer()
    if options.get("overlong"):
        w.bits = "0" * 32
        return w.finish(0x68)
    w.ue(options.get("id", 0))
    w.ue(options.get("sequence", 0))
    w.u(options.get("entropy", 0), 1)
    w.u(options.get("bottom", 0), 1)
    groups = options.get("groups", 1)
    w.ue(groups - 1)
    if groups > 1:
        kind = options.get("kind", 0)
        w.ue(kind)
        if kind == 0:
            for value in options.get("runs", [0] * groups):
                w.ue(value)
        elif kind == 2:
            for top, bottom in options.get("rectangles", [(0, 5)] * (groups - 1)):
                w.ue(top)
                w.ue(bottom)
        elif kind in (3, 4, 5):
            w.u(options.get("direction", 0), 1)
            w.ue(options.get("rate", 0))
        elif kind == 6:
            ids = options.get("map", [i % groups for i in range(8)])
            w.ue(len(ids) - 1)
            for group in ids:
                w.u(group, (groups - 1).bit_length())
    w.ue(options.get("l0", 0))
    w.ue(options.get("l1", 0))
    w.u(options.get("weighted", 0), 1)
    w.u(options.get("bipred", 0), 2)
    w.se(options.get("qp", 0))
    w.se(options.get("qs", 0))
    w.se(options.get("chroma_offset", 0))
    w.u(options.get("deblock", 1), 1)
    w.u(options.get("intra", 0), 1)
    w.u(options.get("redundant", 0), 1)
    if options.get("extension"):
        transform = options.get("transform", 0)
        w.u(transform, 1)
        w.u(options.get("scaling", 0), 1)
        if options.get("scaling"):
            for index in range(6 + (6 if options.get("chroma", 1) == 3 else 2) * transform):
                w.u(1, 1)
                delta = options.get("delta", -8)
                if delta == -8:
                    w.se(delta)
                else:
                    for at in range(16 if index < 6 else 64):
                        w.se(delta if at == 0 else 0)
        w.se(options.get("second_offset", 0))
    return w.finish(0x68)


def metadata(f):
    groups = f["num_slice_groups_minus1"] + 1
    kind = f.get("slice_group_map_type", -1)
    offset = f["chroma_qp_index_offset"]
    result = [f["pic_parameter_set_id"], f["seq_parameter_set_id"], f["entropy_coding_mode_flag"],
              f["bottom_field_pic_order_in_frame_present_flag"], groups, kind,
              f.get("slice_group_change_direction_flag", 0), f.get("slice_group_change_rate_minus1", -1) + 1,
              f.get("pic_size_in_map_units_minus1", -1) + 1,
              f["num_ref_idx_l0_default_active_minus1"] + 1, f["num_ref_idx_l1_default_active_minus1"] + 1,
              f["weighted_pred_flag"], f["weighted_bipred_idc"], f["pic_init_qp_minus26"] + 26,
              f["pic_init_qs_minus26"] + 26, offset, f.get("second_chroma_qp_index_offset", offset),
              f["deblocking_filter_control_present_flag"], f["constrained_intra_pred_flag"],
              f["redundant_pic_cnt_present_flag"], f.get("transform_8x8_mode_flag", 0),
              f.get("pic_scaling_matrix_present_flag", 0)]
    assert len(result) == 22
    return result


def reference(binary, sps, pps, directory):
    path = directory / "parameter-pair.bin"
    path.write_bytes(sps + b"\0\0\1" + pps)
    process = subprocess.run([str(binary), str(path)], check=True, capture_output=True, text=True)
    # The second header has its own trailing bits and overrides duplicate SPS names.
    section = process.stderr.split("Picture Parameter Set", 1)[1]
    fields = references.trace_fields(section)
    assert fields["rbsp_stop_one_bit"] == 1
    return metadata(fields)


def prefix_reference(binary, sps, pps, directory):
    # Annex B strips trailing zero bytes. A complete PPS NAL cannot end in zero.
    if not pps or pps[-1] == 0:
        return []
    try:
        return reference(binary, sps, pps, directory)
    except subprocess.CalledProcessError:
        return []


def packets(path):
    data = path.read_bytes()
    markers = list(re.finditer(b"\x00{2,}\x01", data))
    return [data[m.end():markers[i + 1].start() if i + 1 < len(markers) else len(data)].rstrip(b"\0")
            for i, m in enumerate(markers)]


def controls():
    common = syntax.make(poc=2)
    base = syntax.make(profile=66, poc=2)
    return [
        ("minimal", base, make()),
        ("boundary-fields", syntax.make(profile=110, depth=10, id=31),
         make(id=255, sequence=31, entropy=1, bottom=1, l0=31, l1=31, weighted=1, bipred=2,
              qp=-38, qs=-26, chroma_offset=-12, intra=1, redundant=1, extension=True, transform=1, second_offset=12)),
        ("dense-444", syntax.make(profile=244, chroma=3, separate=1, depth=14, only=0),
         make(extension=True, transform=1, chroma=3, scaling=1, delta=0, qp=-62)),
        ("monochrome-scaling", syntax.make(chroma=0, depth=10), make(extension=True, transform=1, scaling=1)),
        ("six-scaling-lists", common, make(extension=True, scaling=1, delta=127)),
        ("inherited-second-offset", common, make(chroma_offset=12)),
        ("interleaved-eight-groups", base, make(groups=8, runs=list(range(8)))),
        ("dispersed", base, make(groups=3, kind=1)),
        ("foreground", base, make(groups=3, kind=2, rectangles=[(0, 5), (2, 7)])),
        ("box-out", base, make(groups=2, kind=3, direction=1, rate=7)),
        ("raster", base, make(groups=2, kind=4)),
        ("wipe", base, make(groups=2, kind=5, direction=1, rate=1)),
        ("explicit-three-groups", base, make(groups=3, kind=6)),
        ("interlaced-explicit-map", syntax.make(profile=88, only=0), make(groups=3, kind=6)),
        ("binary-explicit-map", base, make(groups=2, kind=6)),
        ("five-group-explicit-map", base, make(groups=5, kind=6)),
        ("large-explicit-map", syntax.make(profile=66, width=127, height=127, poc=2),
         make(groups=8, kind=6, map=[i % 8 for i in range(16384)])),
    ]


def invalid():
    return [(name, make(**options), error) for name, options, error in [
        ("parameter-id", {"id": 256}, "Header"), ("sequence-id", {"sequence": 32}, "Header"),
        ("missing-sequence", {"sequence": 1}, "Header"), ("group-count", {"groups": 9}, "Header"),
        ("map-type", {"groups": 2, "kind": 7}, "Header"),
        ("run-length", {"groups": 2, "runs": [8, 0]}, "Header"),
        ("rectangle-order", {"groups": 2, "kind": 2, "rectangles": [(5, 0)]}, "Header"),
        ("rectangle-columns", {"groups": 2, "kind": 2, "rectangles": [(3, 4)]}, "Header"),
        ("rectangle-extent", {"groups": 2, "kind": 2, "rectangles": [(0, 8)]}, "Header"),
        ("change-group-count", {"groups": 3, "kind": 3}, "Header"),
        ("change-rate", {"groups": 2, "kind": 4, "rate": 8}, "Header"),
        ("map-size", {"groups": 3, "kind": 6, "map": [0, 1, 2]}, "Header"),
        ("map-identifier", {"groups": 3, "kind": 6, "map": [3] * 8}, "Header"),
        ("references-l0", {"l0": 32}, "Header"), ("references-l1", {"l1": 32}, "Header"),
        ("weighted-bipred", {"bipred": 3}, "Header"),
        ("quantizer-low", {"qp": -27}, "Header"), ("quantizer-high", {"qp": 26}, "Header"),
        ("switch-quantizer-low", {"qs": -27}, "Header"), ("switch-quantizer-high", {"qs": 26}, "Header"),
        ("chroma-low", {"chroma_offset": -13}, "Header"), ("chroma-high", {"chroma_offset": 13}, "Header"),
        ("second-chroma", {"extension": True, "second_offset": 13}, "Header"),
        ("scaling-delta", {"extension": True, "scaling": 1, "delta": 128}, "Header"),
        ("overlong-integer", {"overlong": True}, "Limit"),
    ]]


def vectors(directory):
    binary = syntax.build_trace()
    for name, path in references.generate(directory):
        units = packets(path)
        sps = next(p for p in units if p[0] & 31 == 7)
        pps = next(p for p in units if p[0] & 31 == 8)
        yield name, sps, pps, reference(binary, sps, pps, directory), True
    for name, sps, pps in controls():
        yield name, sps, pps, reference(binary, sps, pps, directory), False


def main():
    lines = ["// AVC PPS metadata from FFmpeg's independent coded-bitstream reader.", 'import "std/video/frame";',
             "pub struct Vector { name: string, sequence: string, bytes: string, values: i64[] }",
             "pub struct Invalid { name: string, sequence: string, bytes: string, error: frame.VideoError }", "pub struct Prefix { name: string, sequence: string, bytes: string, values: i64[] }", "pub function vectors(): Vector[] {", "  return ["]
    prefix_lines = []
    with tempfile.TemporaryDirectory() as temporary:
        directory = Path(temporary)
        binary = syntax.build_trace()
        for name, sps, pps, values, encoded in vectors(directory):
            lines.append(f'    Vector {{ name: "{name}", sequence: "{sps.hex()}", bytes: "{pps.hex()}", values: ['
                         + ", ".join(f"{n}i64" for n in values) + "] },")
            if encoded:
                for size in range(len(pps)):
                    prefix = pps[:size]
                    expected = prefix_reference(binary, sps, prefix, directory)
                    if expected:
                        print(f"{name}: prefix {size} is a complete shorter PPS")
                    prefix_lines.append(f'    Prefix {{ name: "{name}:{size}", sequence: "{sps.hex()}", bytes: "{prefix.hex()}", values: ['
                                        + ", ".join(f"{n}i64" for n in expected) + "] },")

    lines += ["  ];", "}", "pub function prefixes(): Prefix[] {", "  return [", *prefix_lines, "  ];", "}", "pub function invalid(): Invalid[] {", "  return ["]
    context = syntax.make(profile=66, poc=2).hex()
    for name, data, error in invalid():
        lines.append(f'    Invalid {{ name: "{name}", sequence: "{context}", bytes: "{data.hex()}", error: frame.{error} }},')
    lines += ["  ];", "}"]
    target = ROOT / "tests/stdlib/video_avc_pps_vectors.fern"
    target.write_text("\n".join(lines) + "\n")
    compiler = os.environ.get("FERN") or subprocess.check_output([str(ROOT / "bootstrap/bootstrap.sh"), "stage0"], text=True).strip()
    subprocess.run([compiler, "-fmt", "-w", str(target)], check=True)
    print("Wrote", target.relative_to(ROOT))


if __name__ == "__main__":
    main()
