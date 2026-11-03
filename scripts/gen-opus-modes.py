#!/usr/bin/env python3
"""Writes internal/stdlib/std/opus/modes.fern from a libopus 1.4 checkout.

Usage: gen-opus-modes.py LIBOPUS_DIR

The module holds the static data of the one CELT mode that Opus uses: 48 kHz,
frames of 960 samples, and an overlap of 120 samples. The data comes from
celt/modes.c, celt/quant_bands.c and celt/static_modes_float.h. The script reads each table, checks
its length, and writes it as a Fern constant. The data is part of libopus, so
the module carries the libopus notice.
"""

import re
import struct
import sys
from pathlib import Path

OUT = Path(__file__).resolve().parent.parent / "internal/stdlib/std/opus/modes.fern"

# Each table: the C name, the file, the Fern name, the Fern element type, and
# the length.
TABLES = [
    ("eband5ms", "modes.c", "EBANDS", "i32", 22),
    ("band_allocation", "modes.c", "ALLOC", "i32", 11 * 21),
    ("logN400", "static_modes_float.h", "LOGN", "i32", 21),
    ("cache_index50", "static_modes_float.h", "CACHE_INDEX", "i32", 105),
    ("cache_bits50", "static_modes_float.h", "CACHE_BITS", "i32", 392),
    ("cache_caps50", "static_modes_float.h", "CACHE_CAPS", "i32", 168),
    ("window120", "static_modes_float.h", "WINDOW", "f32", 120),
    ("eMeans", "quant_bands.c", "E_MEANS", "f32", 25),
    ("e_prob_model", "quant_bands.c", "E_PROB_MODEL", "i32", 4 * 2 * 42),
]


def notice(root):
    """The copyright lines and the license of the libopus COPYING file."""
    lines = (root / "COPYING").read_text().strip().splitlines()
    return ["// " + line if line else "//" for line in lines]


def float_build(src):
    """`src` without the FIXED_POINT branches of its #ifdef blocks."""
    out = []
    skip = False
    for line in src.splitlines():
        if line.startswith("#ifdef FIXED_POINT"):
            skip = True
        elif skip and line.startswith(("#else", "#endif")):
            skip = False
        elif not skip:
            out.append(line)
    return "\n".join(out)


def values(src, name):
    """The initializer of the C array `name`, without comments."""
    src = float_build(src)
    m = re.search(r"\b" + name + r"\s*(?:\[[^\]]*\]\s*)+=\s*\{(.*?)\};", src, re.S)
    if m is None:
        sys.exit(f"gen-opus-modes: no table {name}")
    body = re.sub(r"/\*.*?\*/", "", m.group(1), flags=re.S).replace("{", "").replace("}", "")
    return [v.strip() for v in body.split(",") if v.strip()]


def fern_value(v, kind):
    """A Fern literal. A float is the exact decimal of its f32 value, so that a
    parse through f64 cannot round it a second time."""
    if kind == "f32":
        x = struct.unpack("<f", struct.pack("<f", float(v.rstrip("f"))))[0]
        return repr(x) + "f32"
    return str(int(v, 0))


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    root = Path(sys.argv[1])
    celt = root / "celt"
    out = [
        "// The static data of the CELT mode that Opus uses: 48 kHz, frames of 960",
        "// samples and an overlap of 120 samples. scripts/gen-opus-modes.py writes",
        "// this file from libopus 1.4. Do not edit it.",
        "//",
    ] + notice(root) + [
        "",
        "pub const SAMPLE_RATE: i32 = 48000;",
        "",
        "pub const OVERLAP: i32 = 120;",
        "",
        "pub const BANDS: i32 = 21;",
        "",
        "pub const MAX_LM: i32 = 3;",
        "",
        "pub const SHORT_MDCT_SIZE: i32 = 120;",
        "",
        "pub const ALLOC_VECTORS: i32 = 11;",
        "",
        "pub const PREEMPH: f32 = 0.85000610f32;",
    ]
    for cname, fname, name, kind, length in TABLES:
        vals = values((celt / fname).read_text(), cname)
        if len(vals) != length:
            sys.exit(f"gen-opus-modes: {cname} has {len(vals)} values, not {length}")
        out.append("")
        out.append(f"pub const {name}: {kind}[] = [")
        row = 8 if kind == "f32" else 16
        for i in range(0, length, row):
            out.append("  " + ", ".join(fern_value(v, kind) for v in vals[i:i + row]) + ",")
        out.append("];")
    OUT.write_text("\n".join(out) + "\n")


if __name__ == "__main__":
    main()
