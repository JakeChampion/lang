#!/usr/bin/env python3
"""Write internal/stdlib/std/opus/modes.fern from pinned libopus.

Usage: gen-opus-modes.py LIBOPUS_DIR

The module holds the static data of the one CELT mode that Opus uses: 48 kHz,
frames of 960 samples, and an overlap of 120 samples. The data comes from
celt/modes.c, celt/quant_bands.c and celt/static_modes_float.h, with the
FFT and MDCT tables. The script reads each table, checks
its length, and writes it as a Fern constant. The data is part of libopus, so
the module carries the libopus notice.
"""

import hashlib
import re
import struct
import sys
from pathlib import Path

OUT = Path(__file__).resolve().parent.parent / "internal/stdlib/std/opus/modes.fern"
PIN = "22244de5a79bd1d6d623c32e72bf1954b56235be"
HASHES = {
    "celt/modes.c": "03e61ddf326812e4d0d7c00067319af74e1cc5653e2c9551e5a322cad1f73480",
    "celt/quant_bands.c": "bec6e7ac19ff02b83480c6ca6a01c084566781856a1e3442b7468bf25a4b937b",
    "celt/static_modes_float.h": "c3cf8439cdcf9281a5a0a66eefd5f9d0c82537c6f88e33b53ace9c9b3880f60c",
    "COPYING": "01e1167d54a096d123cf6dfbbeb19587278845c6481d2d66d545669846079551",
}

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
    ("fft_twiddles48000_960", "static_modes_float.h", "FFT_TWIDDLES", "f32", 2 * 480),
    ("mdct_twiddles960", "static_modes_float.h", "MDCT_TWIDDLES", "f32", 1800),
]

# The FFT of each MDCT size, from the largest to the smallest. FFT_BITREV
# holds the bit-reverse tables one after the other.
FFT_SIZES = [480, 240, 120, 60]


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


def table(name, kind, vals):
    """The lines of a Fern array constant."""
    row = 8 if kind == "f32" else 16
    lines = ["", f"pub const {name}: {kind}[] = ["]
    for i in range(0, len(vals), row):
        lines.append("  " + ", ".join(fern_value(v, kind) for v in vals[i:i + row]) + ",")
    return lines + ["];"]


def fft_states(path):
    """The size, scale, shift, factors and bit-reverse table of each FFT."""
    src = path.read_text()
    scale, shift, factors, bitrev = [], [], [], []
    for i, n in enumerate(FFT_SIZES):
        m = re.search(r"fft_state48000_960_" + str(i) + r"\s*=\s*\{(.*?)\};", src, re.S)
        if m is None:
            sys.exit(f"gen-opus-modes: no FFT state {i}")
        body = re.sub(r"/\*.*?\*/", "", m.group(1), flags=re.S)
        head = [v.strip() for v in body.split("{")[0].split(",") if v.strip()]
        facs = [v.strip() for v in body.split("{")[1].split("}")[0].split(",") if v.strip()]
        if int(head[0]) != n or len(facs) != 16:
            sys.exit(f"gen-opus-modes: FFT state {i} is not of size {n}")
        scale.append(head[1])
        shift.append(head[2])
        factors += facs
        rev = values(src, f"fft_bitrev{n}")
        if len(rev) != n:
            sys.exit(f"gen-opus-modes: fft_bitrev{n} has {len(rev)} values")
        bitrev += rev
    return (table("FFT_SIZE", "i32", [str(n) for n in FFT_SIZES]) + table("FFT_SCALE", "f32", scale)
            + table("FFT_SHIFT", "i32", shift) + table("FFT_FACTORS", "i32", factors)
            + table("FFT_BITREV", "i32", bitrev))


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    root = Path(sys.argv[1])
    for name, digest in HASHES.items():
        if hashlib.sha256((root / name).read_bytes()).hexdigest() != digest:
            sys.exit(f"gen-opus-modes: {name} does not match libopus {PIN}")
    celt = root / "celt"
    out = [
        "// The static data of the CELT mode that Opus uses: 48 kHz, frames of 960",
        "// samples and an overlap of 120 samples. scripts/gen-opus-modes.py writes",
        "// this file from libopus " + PIN + ".",
        "// The generator verifies each source file hash. Do not edit this file.",
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
        "",
        "pub const MDCT_SIZE: i32 = 1920;",
    ]
    for cname, fname, name, kind, length in TABLES:
        vals = values((celt / fname).read_text(), cname)
        if len(vals) != length:
            sys.exit(f"gen-opus-modes: {cname} has {len(vals)} values, not {length}")
        out += table(name, kind, vals)
    out += fft_states(celt / "static_modes_float.h")
    OUT.write_text("\n".join(out) + "\n")


if __name__ == "__main__":
    main()
