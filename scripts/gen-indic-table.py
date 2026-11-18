#!/usr/bin/env python3
"""Write internal/stdlib/std/shape/indic.fern: the Indic category and the
Indic position of each character, as the Indic shaper of HarfBuzz gives
them.

Run:  python3 -I scripts/gen-indic-table.py IndicSyllabicCategory.txt IndicPositionalCategory.txt Blocks.txt

The three files come from the Unicode Character Database
(https://www.unicode.org/Public/UCD/latest/ucd/). The script follows
src/gen-indic-table.py of HarfBuzz 15: the same blocks, the same map of
the values, the same changes to the categories and the positions, and the
same positions of the matras for each block. The table also holds the
Khmer categories of the Khmer shaper. It leaves out the Myanmar
characters.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "internal" / "stdlib" / "std" / "shape" / "indic.fern"

ALLOWED_SINGLES = [0x00A0, 0x25CC]
ALLOWED_BLOCKS = [
    "Basic Latin", "Latin-1 Supplement", "Devanagari", "Bengali", "Gurmukhi", "Gujarati", "Oriya", "Tamil",
    "Telugu", "Kannada", "Malayalam", "Khmer", "Vedic Extensions", "General Punctuation", "Superscripts and Subscripts",
    "Devanagari Extended",
]

# The numbers of std/shape for the categories and the positions.
CATEGORIES = ["X", "C", "V", "N", "H", "ZWNJ", "ZWJ", "M", "SM", "", "A", "PLACEHOLDER", "DOTTEDCIRCLE",
              "RS", "MPst", "Repha", "Ra", "CM", "Symbol", "CS", "SMPst", "VAbv", "VBlw", "VPre", "VPst",
              "Robatic", "Xgroup", "Ygroup"]
POSITIONS = ["START", "RA_TO_BECOME_REPH", "PRE_M", "PRE_C", "BASE_C", "AFTER_MAIN", "ABOVE_C", "BEFORE_SUB",
             "BELOW_C", "AFTER_SUB", "BEFORE_POST", "POST_C", "AFTER_POST", "SMVD", "END"]

CATEGORY_MAP = {
    "Other": "X", "Avagraha": "Symbol", "Bindu": "SM", "Brahmi_Joining_Number": "PLACEHOLDER",
    "Cantillation_Mark": "A", "Consonant": "C", "Consonant_Dead": "C", "Consonant_Final": "CM",
    "Consonant_Head_Letter": "C", "Consonant_Initial_Postfixed": "C", "Consonant_Killer": "M",
    "Consonant_Medial": "CM", "Consonant_Placeholder": "PLACEHOLDER", "Consonant_Preceding_Repha": "Repha",
    "Consonant_Prefixed": "X", "Consonant_Subjoined": "CM", "Consonant_Succeeding_Repha": "CM",
    "Consonant_With_Stacker": "CS", "Gemination_Mark": "SM", "Invisible_Stacker": "H", "Joiner": "ZWJ",
    "Modifying_Letter": "X", "Non_Joiner": "ZWNJ", "Nukta": "N", "Number": "PLACEHOLDER",
    "Number_Joiner": "PLACEHOLDER", "Pure_Killer": "M", "Register_Shifter": "RS", "Syllable_Modifier": "SM",
    "Tone_Letter": "X", "Tone_Mark": "N", "Virama": "H", "Visarga": "SM", "Vowel": "V",
    "Vowel_Dependent": "M", "Vowel_Independent": "V",
}

POSITION_MAP = {
    "Not_Applicable": "END", "Left": "PRE_C", "Top": "ABOVE_C", "Bottom": "BELOW_C", "Right": "POST_C",
    "Bottom_And_Right": "POST_C", "Left_And_Right": "POST_C", "Top_And_Bottom": "BELOW_C",
    "Top_And_Bottom_And_Left": "BELOW_C", "Top_And_Bottom_And_Right": "POST_C", "Top_And_Left": "ABOVE_C",
    "Top_And_Left_And_Right": "POST_C", "Top_And_Right": "POST_C", "Overstruck": "AFTER_MAIN",
    "Visual_order_left": "PRE_M",
}

# The categories that HarfBuzz changes for the Indic shaper.
CATEGORY_OVERRIDES = {
    0x2015: "PLACEHOLDER", 0x2022: "PLACEHOLDER", 0x25FB: "PLACEHOLDER", 0x25FC: "PLACEHOLDER",
    0x25FD: "PLACEHOLDER", 0x25FE: "PLACEHOLDER",
    0x0930: "Ra", 0x09B0: "Ra", 0x09F0: "Ra", 0x0A30: "Ra", 0x0AB0: "Ra", 0x0B30: "Ra", 0x0BB0: "Ra",
    0x0C30: "Ra", 0x0CB0: "Ra", 0x0D30: "Ra",
    0x0953: "SM", 0x0954: "SM", 0x0A40: "MPst", 0x0A72: "C", 0x0A73: "C",
    0x1CED: "A", 0x0A51: "M",
    0x11301: "SM", 0x11302: "SM", 0x11303: "SM", 0x1133B: "N", 0x1133C: "N",
    0x0AFB: "N", 0x0B55: "N", 0x09FC: "PLACEHOLDER", 0x0C80: "PLACEHOLDER", 0x0D04: "PLACEHOLDER",
    0x25CC: "DOTTEDCIRCLE",
    # Khmer
    0x179A: "Ra", 0x17CC: "Robatic", 0x17C9: "Robatic", 0x17CA: "Robatic",
    0x17C6: "Xgroup", 0x17CB: "Xgroup", 0x17CD: "Xgroup", 0x17CE: "Xgroup", 0x17CF: "Xgroup", 0x17D0: "Xgroup",
    0x17D1: "Xgroup", 0x17C7: "Ygroup", 0x17C8: "Ygroup", 0x17DD: "Ygroup", 0x17D3: "Ygroup",
    0x17D9: "PLACEHOLDER",
}
CATEGORY_OVERRIDES.update({c: "A" for c in range(0x1CE2, 0x1CE9)})
CATEGORY_OVERRIDES.update({c: "Symbol" for c in list(range(0xA8F2, 0xA8F8)) + list(range(0x1CE9, 0x1CED))
                           + list(range(0x1CEE, 0x1CF2))})

POSITION_OVERRIDES = {0x0A51: "BELOW_C", 0x0B01: "BEFORE_SUB"}

POSITIONED = ("CM", "SM", "RS", "H", "M", "MPst")
CONSONANTS = ("C", "CS", "Ra", "CM", "V", "PLACEHOLDER", "DOTTEDCIRCLE")
MATRAS = ("M", "MPst")
SMVD = ("SM", "SMPst", "VD", "A", "Symbol")


def read(path):
    out = {}
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        line = line.split("#")[0].strip()
        if not line:
            continue
        cps, value = [x.strip() for x in line.split(";")]
        lo, _, hi = cps.partition("..")
        for c in range(int(lo, 16), int(hi or lo, 16) + 1):
            out[c] = value
    return out


def matra_right(u, block):
    if block == "Devanagari":
        return "AFTER_SUB"
    if block in ("Bengali", "Gurmukhi", "Gujarati", "Oriya", "Tamil", "Malayalam"):
        return "AFTER_POST"
    if block == "Telugu":
        return "BEFORE_SUB" if u <= 0x0C42 else "AFTER_SUB"
    if block == "Kannada":
        return "BEFORE_SUB" if u < 0x0CC3 or u > 0x0CD6 else "AFTER_SUB"
    return "AFTER_SUB"


def matra_top(u, block):
    return {"Devanagari": "AFTER_SUB", "Gurmukhi": "AFTER_POST", "Gujarati": "AFTER_SUB", "Oriya": "AFTER_MAIN",
            "Tamil": "AFTER_SUB", "Telugu": "BEFORE_SUB", "Kannada": "BEFORE_SUB"}.get(block, "AFTER_SUB")


def matra_bottom(u, block):
    return {"Devanagari": "AFTER_SUB", "Bengali": "AFTER_SUB", "Gurmukhi": "AFTER_POST", "Gujarati": "AFTER_POST",
            "Oriya": "AFTER_SUB", "Tamil": "AFTER_POST", "Telugu": "BEFORE_SUB", "Kannada": "BEFORE_SUB",
            "Malayalam": "AFTER_POST"}.get(block, "AFTER_SUB")


def matra_position(u, pos, block):
    if pos == "PRE_C":
        return "PRE_M"
    if pos == "POST_C":
        return matra_right(u, block)
    if pos == "ABOVE_C":
        return matra_top(u, block)
    if pos == "BELOW_C":
        return matra_bottom(u, block)
    raise ValueError("no matra position %s for U+%04X" % (pos, u))


# The category of a matra of Khmer, from its position.
SIDE_CATEGORIES = {"PRE_C": "VPre", "ABOVE_C": "VAbv", "BELOW_C": "VBlw", "POST_C": "VPst"}


def properties(u, cats, sides, blocks):
    cat = CATEGORY_MAP[cats.get(u, "Other")]
    side = sides.get(u, "Not_Applicable")
    if cat == "SM" and side == "Not_Applicable":
        cat = "SMPst"
    pos = POSITION_MAP[side]
    cat = CATEGORY_OVERRIDES.get(u, cat)
    if cat not in POSITIONED:
        pos = "END"
    if cat in CONSONANTS:
        pos = "BASE_C"
    elif cat in MATRAS and blocks.get(u) == "Khmer":
        cat = SIDE_CATEGORIES[pos]
    elif cat in MATRAS:
        pos = matra_position(u, pos, blocks.get(u, "No_Block"))
    elif cat in SMVD:
        pos = "SMVD"
    pos = POSITION_OVERRIDES.get(u, pos)
    return CATEGORIES.index(cat) | POSITIONS.index(pos) << 5


def version(path):
    m = re.search(r"-(\d+\.\d+\.\d+)\.txt", Path(path).read_text(encoding="utf-8").splitlines()[0])
    return m.group(1) if m else "unknown"


def main():
    if len(sys.argv) != 4:
        sys.exit(__doc__)
    cats, sides, blocks = read(sys.argv[1]), read(sys.argv[2]), read(sys.argv[3])
    default = POSITIONS.index("END") << 5
    chars = [u for u in sorted(set(cats) | set(sides) | set(CATEGORY_OVERRIDES))
             if u in ALLOWED_SINGLES or u in CATEGORY_OVERRIDES or blocks.get(u) in ALLOWED_BLOCKS]
    table = {u: properties(u, cats, sides, blocks) for u in chars}
    table = {u: v for u, v in table.items() if v != default}
    # Runs of code points, each run from its first entry to its last one,
    # split where 64 code points in a row take the default.
    runs = []
    for u in sorted(table):
        if runs and u - runs[-1][1] < 64:
            runs[-1][1] = u
        else:
            runs.append([u, u])
    lines = [
        "// Generated by scripts/gen-indic-table.py from the Unicode Character Database",
        "// %s -- do not edit by hand." % version(sys.argv[1]),
        "//",
        "// The Indic category of each character, from the bit 0, and its Indic",
        "// position, from the bit 5, with the numbers of std/shape. A character out",
        "// of the table has the category 0 (X) and the position 14 (END).",
        "",
        "// The first and the last code point of each run of the table.",
        "const RUNS: i32[] = [%s];" % ", ".join("0x%04X, 0x%04X" % (lo, hi) for lo, hi in runs),
        "",
        "// The values of the runs, one after the other.",
        "const VALUES: i32[] = [",
    ]
    values = [table.get(u, default) for lo, hi in runs for u in range(lo, hi + 1)]
    for k in range(0, len(values), 16):
        lines.append("  " + ", ".join(str(v) for v in values[k:k + 16]) + ",")
    lines += [
        "];",
        "",
        "// The category and the position of `c`.",
        "pub function properties(c: i32): i32 {",
        "  let at = 0;",
        "  let k = 0;",
        "  while (k + 1 < RUNS.len() && c >= RUNS[k]) {",
        "    if (c <= RUNS[k + 1]) {",
        "      return VALUES[at + c - RUNS[k]];",
        "    }",
        "    at += RUNS[k + 1] - RUNS[k] + 1;",
        "    k += 2;",
        "  }",
        "  return %d;" % default,
        "}",
        "",
    ]
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text("\n".join(lines))


if __name__ == "__main__":
    main()
