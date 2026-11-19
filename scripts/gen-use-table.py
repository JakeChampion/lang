#!/usr/bin/env python3
"""Write internal/stdlib/std/shape/universal.fern: the category of each character
for the Universal Shaping Engine, as HarfBuzz gives it.

Run:  python3 -I scripts/gen-use-table.py UCDDIR MSUSEDIR

UCDDIR holds IndicSyllabicCategory.txt, IndicPositionalCategory.txt,
ArabicShaping.txt, DerivedCoreProperties.txt, UnicodeData.txt, Blocks.txt
and Scripts.txt of the Unicode Character Database
(https://www.unicode.org/Public/UCD/latest/ucd/). MSUSEDIR holds
IndicSyllabicCategory-Additional.txt and
IndicPositionalCategory-Additional.txt, from src/ms-use of HarfBuzz. The
script follows src/gen-use-table.py of HarfBuzz 15: the same files, the
same rules for each category and the same positions. The numbers of the
categories are those of hb-ot-shaper-use-machine.rl.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "internal" / "stdlib" / "std" / "shape" / "universal.fern"

DISABLED_SCRIPTS = {"Arabic", "Lao", "Samaritan", "Syriac", "Thai"}

# The numbers of hb-ot-shaper-use-machine.rl.
NUMBERS = {
    "O": 0, "B": 1, "N": 4, "GB": 5, "CGJ": 6, "SUB": 11, "H": 12, "HN": 13, "ZWNJ": 14, "WJ": 16, "R": 18,
    "CS": 43, "IS": 44, "Sk": 48, "G": 49, "J": 50, "SB": 51, "SE": 52, "HVM": 53, "HM": 54, "HR": 55, "RK": 56,
    "FAbv": 24, "FBlw": 25, "FPst": 26, "MAbv": 27, "MBlw": 28, "MPst": 29, "MPre": 30, "CMAbv": 31, "CMBlw": 32,
    "VAbv": 33, "VBlw": 34, "VPst": 35, "VPre": 22, "VMAbv": 37, "VMBlw": 38, "VMPst": 39, "VMPre": 23,
    "SMAbv": 41, "SMBlw": 42, "FMAbv": 45, "FMBlw": 46, "FMPst": 47,
}


def read(path, field=1, prefix=""):
    """The value of the field `field` of each code point of a UCD file."""
    out = {}
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        line = line.split("#", 1)[0]
        fields = [x.strip() for x in line.split(";")]
        if len(fields) == 1:
            continue
        lo, _, hi = fields[0].partition("..")
        for u in range(int(lo, 16), int(hi or lo, 16) + 1):
            out[u] = prefix + fields[field]
    return out


def default_ignorables(path):
    """The code points with the property Default_Ignorable_Code_Point."""
    out = set()
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        fields = [x.strip() for x in line.split("#", 1)[0].split(";")]
        if len(fields) > 1 and fields[1] == "Default_Ignorable_Code_Point":
            lo, _, hi = fields[0].partition("..")
            out.update(range(int(lo, 16), int(hi or lo, 16) + 1))
    return out


def unicode_data(path):
    """The general category of each code point that has a line of its own.
    As in HarfBuzz, the code points inside a First and Last range have none."""
    out = {}
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        f = line.split(";")
        out[int(f[0], 16)] = f[2]
    return out


def is_base(u, isc, udi, ugc, ajt):
    return (isc in ("Number", "Consonant", "Consonant_Head_Letter", "Tone_Letter", "Vowel_Independent")
            or ajt in ("jt_C", "jt_D", "jt_L", "jt_R") and isc != "Joiner"
            or ugc == "Lo" and isc in ("Avagraha", "Bindu", "Consonant_Final", "Consonant_Medial",
                                       "Consonant_Subjoined", "Vowel", "Vowel_Dependent"))


def is_base_other(u, isc, udi, ugc, ajt):
    return isc == "Consonant_Placeholder" or u in (0x2015, 0x2022, 0x25FB, 0x25FC, 0x25FD, 0x25FE)


def is_cgj(u, isc, udi, ugc, ajt):
    return isc == "Joiner" or udi and ugc in ("Mc", "Me", "Mn")


def is_sakot(u, isc, udi, ugc, ajt):
    return u == 0x1A60


def is_word_joiner(u, isc, udi, ugc, ajt):
    hangul_fillers = (0x115F, 0x1160, 0x3164, 0xFFA0, 0x1BCA0, 0x1BCA1, 0x1BCA2, 0x1BCA3)
    return (udi and u not in hangul_fillers and isc == "Other" and not is_cgj(u, isc, udi, ugc, ajt)) or ugc == "Cn"


def is_other(u, isc, udi, ugc, ajt):
    return ((ugc == "Po" or isc in ("Consonant_Dead", "Joiner", "Modifying_Letter", "Other"))
            and not is_base(u, isc, udi, ugc, ajt) and not is_base_other(u, isc, udi, ugc, ajt)
            and not is_cgj(u, isc, udi, ugc, ajt) and not isc == "Symbol_Modifier"
            and not is_word_joiner(u, isc, udi, ugc, ajt))


RULES = {
    "B": is_base,
    "N": lambda u, isc, udi, ugc, ajt: isc == "Brahmi_Joining_Number",
    "GB": is_base_other,
    "CGJ": is_cgj,
    "F": lambda u, isc, udi, ugc, ajt: isc == "Consonant_Final" and ugc != "Lo" or isc == "Consonant_Succeeding_Repha",
    "FM": lambda u, isc, udi, ugc, ajt: isc == "Syllable_Modifier",
    "M": lambda u, isc, udi, ugc, ajt: isc == "Consonant_Medial" and ugc != "Lo" or isc == "Consonant_Initial_Postfixed",
    "CM": lambda u, isc, udi, ugc, ajt: isc in ("Nukta", "Gemination_Mark", "Consonant_Killer"),
    "SUB": lambda u, isc, udi, ugc, ajt: isc == "Consonant_Subjoined" and ugc != "Lo",
    "CS": lambda u, isc, udi, ugc, ajt: isc == "Consonant_With_Stacker",
    "H": lambda u, isc, udi, ugc, ajt: isc == "Virama" and u != 0x0DCA,
    "HVM": lambda u, isc, udi, ugc, ajt: u == 0x0DCA,
    "HN": lambda u, isc, udi, ugc, ajt: isc == "Number_Joiner",
    "IS": lambda u, isc, udi, ugc, ajt: isc == "Invisible_Stacker" and not is_sakot(u, isc, udi, ugc, ajt),
    "G": lambda u, isc, udi, ugc, ajt: isc == "Hieroglyph",
    "HM": lambda u, isc, udi, ugc, ajt: isc == "Hieroglyph_Modifier",
    "HR": lambda u, isc, udi, ugc, ajt: isc == "Hieroglyph_Mirror",
    "J": lambda u, isc, udi, ugc, ajt: isc == "Hieroglyph_Joiner",
    "SB": lambda u, isc, udi, ugc, ajt: isc in ("Hieroglyph_Mark_Begin", "Hieroglyph_Segment_Begin"),
    "SE": lambda u, isc, udi, ugc, ajt: isc in ("Hieroglyph_Mark_End", "Hieroglyph_Segment_End"),
    "ZWNJ": lambda u, isc, udi, ugc, ajt: isc == "Non_Joiner",
    "O": is_other,
    "RK": lambda u, isc, udi, ugc, ajt: isc == "Reordering_Killer",
    "R": lambda u, isc, udi, ugc, ajt: isc in ("Consonant_Preceding_Repha", "Consonant_Prefixed"),
    "Sk": is_sakot,
    "SM": lambda u, isc, udi, ugc, ajt: isc == "Symbol_Modifier",
    "V": lambda u, isc, udi, ugc, ajt: isc == "Pure_Killer" or ugc != "Lo" and isc in ("Vowel", "Vowel_Dependent"),
    "VM": lambda u, isc, udi, ugc, ajt: (isc in ("Tone_Mark", "Cantillation_Mark", "Register_Shifter", "Visarga")
                                         or ugc != "Lo" and isc == "Bindu"),
    "WJ": is_word_joiner,
}

# The positions of the categories that have them.
POSITIONS = {
    "F": {"Abv": ["Top"], "Blw": ["Bottom"], "Pst": ["Right"]},
    "M": {"Abv": ["Top"], "Blw": ["Bottom", "Bottom_And_Left", "Bottom_And_Right"], "Pst": ["Right"],
          "Pre": ["Left", "Top_And_Bottom_And_Left"]},
    "CM": {"Abv": ["Top"], "Blw": ["Bottom", "Overstruck"]},
    "V": {"Abv": ["Top", "Top_And_Bottom", "Top_And_Bottom_And_Right", "Top_And_Right"],
          "Blw": ["Bottom", "Overstruck", "Bottom_And_Right"], "Pst": ["Right"],
          "Pre": ["Left", "Top_And_Left", "Top_And_Left_And_Right", "Left_And_Right"]},
    "VM": {"Abv": ["Top"], "Blw": ["Bottom", "Overstruck"], "Pst": ["Right"], "Pre": ["Left"]},
    "SM": {"Abv": ["Top"], "Blw": ["Bottom"]},
    "FM": {"Abv": ["Top"], "Blw": ["Bottom"], "Pst": ["Not_Applicable"]},
}


def category(u, isc, ipc, ajt, udi, ugc):
    """The USE category of `u`, from its properties."""
    if 0x1CE2 <= u <= 0x1CE8:
        isc = "Cantillation_Mark"
    if 0x0F18 <= u <= 0x0F19 or 0x0F3E <= u <= 0x0F3F:
        isc = "Vowel_Dependent"
    if u == 0x1CED:
        isc = "Tone_Mark"
    found = [k for k, rule in RULES.items() if rule(u, isc, udi, ugc, ajt)]
    assert len(found) == 1, (hex(u), isc, udi, ugc, ajt, found)
    use = found[0]
    if u in (0x11302, 0x11303, 0x114C1):
        ipc = "Top"
    sides = POSITIONS.get(use)
    if sides:
        side = [k for k, v in sides.items() if ipc in v]
        assert len(side) == 1, (hex(u), ipc, use)
        use += side[0]
    return NUMBERS[use]


def version(path):
    m = re.search(r"-(\d+\.\d+\.\d+)\.txt", Path(path).read_text(encoding="utf-8").splitlines()[0])
    return m.group(1) if m else "unknown"


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    ucd, ms = Path(sys.argv[1]), Path(sys.argv[2])
    isc = read(ucd / "IndicSyllabicCategory.txt")
    ipc = read(ucd / "IndicPositionalCategory.txt")
    for u, t in read(ms / "IndicSyllabicCategory-Additional.txt").items():
        isc[u] = "Syllable_Modifier" if t == "Consonant_Final_Modifier" else t
    for u, t in read(ms / "IndicPositionalCategory-Additional.txt").items():
        ipc[u] = "Not_Applicable" if t == "NA" else t
    ajt = read(ucd / "ArabicShaping.txt", 2, "jt_")
    udi = default_ignorables(ucd / "DerivedCoreProperties.txt")
    ugc = unicode_data(ucd / "UnicodeData.txt")
    scripts = read(ucd / "Scripts.txt")
    chars = set(isc) | set(ipc) | set(ajt) | udi
    table = {}
    for u in sorted(chars):
        if scripts.get(u, "Unknown") in DISABLED_SCRIPTS:
            continue
        table[u] = category(u, isc.get(u, "Other"), ipc.get(u, "Not_Applicable"), ajt.get(u, "jt_X"), u in udi,
                            ugc.get(u, "Cn"))
    # Each run starts where the category changes. A code point out of the
    # table has the category O, as in HarfBuzz.
    starts, values = [], []
    last = 0
    for u in range(0, 0x110000):
        v = table.get(u, 0)
        if v != last:
            starts.append(u)
            values.append(v)
            last = v
    lines = [
        "// Generated by scripts/gen-use-table.py from the Unicode Character Database",
        "// %s and the ms-use files of HarfBuzz -- do not edit by hand." % version(ucd / "IndicSyllabicCategory.txt"),
        "//",
        "// The category of each character for the Universal Shaping Engine, with the",
        "// numbers of hb-ot-shaper-use-machine.rl. A run of the table goes from its",
        "// start to the start of the next run. Before the first run, the category is",
        "// 0 (O).",
        "",
        "// The first code point of each run.",
        "const STARTS: i32[] = [",
    ]
    for k in range(0, len(starts), 12):
        lines.append("  " + ", ".join("0x%04X" % v for v in starts[k:k + 12]) + ",")
    lines += ["];", "", "// The category of each run.", "const CATEGORIES: i32[] = ["]
    for k in range(0, len(values), 24):
        lines.append("  " + ", ".join(str(v) for v in values[k:k + 24]) + ",")
    lines += [
        "];",
        "",
        "// The category of `c`.",
        "pub function category(c: i32): i32 {",
        "  let lo = 0;",
        "  let hi = STARTS.len();",
        "  while (lo < hi) {",
        "    let mid = (lo + hi) / 2;",
        "    if (STARTS[mid] <= c) {",
        "      lo = mid + 1;",
        "    } else {",
        "      hi = mid;",
        "    }",
        "  }",
        "  return if (lo == 0) { 0 } else { CATEGORIES[lo - 1] };",
        "}",
        "",
    ]
    OUT.write_text("\n".join(lines))


if __name__ == "__main__":
    main()
