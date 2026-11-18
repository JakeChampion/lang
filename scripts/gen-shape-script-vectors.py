#!/usr/bin/env python3
"""Write tests/stdlib/shape_script_vectors.fern: for Hebrew, Thai, Lao and
Khmer, a subset of a Noto Sans font and the glyphs that HarfBuzz gives for a list
of words and of random strings, for the tests of std/shape.

Run:  python3 -I scripts/gen-shape-script-vectors.py FONTDIR

FONTDIR holds NotoSansHebrew-Regular.ttf, NotoSansThai-Regular.ttf,
NotoSansLao-Regular.ttf and NotoSansKhmer-Regular.ttf, from the Noto project (notofonts.github.io). They
are under the SIL Open Font License 1.1, and each subset keeps each name of
its font, so it keeps the notice and the license. A subset holds the
characters of its cases, and its glyphs have no outlines.

Each case is shaped twice: with the font, and with one tag of the font
renamed, as the test renames it. For Hebrew the tag is the GPOS feature
mark, so that the shaper composes the presentation forms. For Thai it is
the script thai, so that the shaper uses the glyphs of the private use
area. The Thai subset gets such glyphs, with no outlines. For Lao it is the
script tag. For Khmer it is the feature pref. A second Khmer subset has its
feature pstf renamed cfar, and the test renames cfar. The Hebrew subset does not have U+FB2C and U+FB2D, so that each
of them decomposes by one step only. The random strings come from a fixed
seed, so the script writes the same file each time.
"""

import base64
import importlib.util
import random
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("gen_shape_vectors", HERE / "gen-shape-vectors.py")
gen = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gen)

import uharfbuzz as hb  # noqa: E402
from fontTools.pens.ttGlyphPen import TTGlyphPen  # noqa: E402

OUT = HERE.parent / "tests" / "stdlib" / "shape_script_vectors.fern"

ZWJ, ZWNJ = "\u200d", "\u200c"

FEATURES = ["ccmp", "locl", "rlig", "calt", "clig", "liga", "rclt", "rtla", "rtlm", "ltra", "ltrm", "kern", "mark",
            "mkmk", "dist", "abvm", "blwm", "pref", "blwf", "abvf", "pstf", "cfar", "pres", "abvs", "blws", "psts"]

# The code points of the private use area that old Thai fonts use.
PUA = list(range(0xF700, 0xF71B)) + list(range(0xF884, 0xF89F))

# For each script: its tag, the name of its functions, the tag that the
# second font renames, its HarfBuzz script, its font, the code points that
# its subset leaves out, a tag of the subset to rename and its new name, its
# words, and the alphabet of its random strings.
KHMER_WORDS = ["ភាសាខ្មែរ", "ក្រ", "ស្ត្រី", "ក\u17be", "ក\u17c0", "ក\u17c4", "ក\u17c5", "ក្ក", "\u17c0", "ក\u17c9",
               "ក\u200d\u17ca", "ង\u200c\u17c9", "ក្រុម", "ព្រះ", "ស\u17c0វភ\u17c5", "ក\u17cf", "ក្ស", "ក្រ\u17c5", "ឥ", "ឰ",
               "ង្ក្រ", "ង្រ្ក",
               # A ligature from a ZWJ is not default ignorable.
               "\u17bc\u17be\u1799\u200d\u17ca\u200c"]
KHMER_ALPHABET = "កខគងចជញដតនបពមយរលវសហអ" + "\u17d2" * 4 + "\u17b6\u17b7\u17b8\u17bb\u17bc\u17be\u17c0\u17c1\u17c2\u17c4\u17c6\u17c7\u17c9\u17cb" + ZWJ + ZWNJ

SCRIPTS = [
    ("hebr", "hebr", "mark", "Hebr", "NotoSansHebrew", {0xFB2C, 0xFB2D}, None,
     ["ש\u05b8\u05c1לו\u05b9ם", "ע\u05b4ב\u05b0ר\u05b4ית", "ב\u05b0\u05bcר\u05b5אש\u05b4\u05c1ית", "ש\u05bc\u05c1", "ש\u05bc\u05c2", "ב\u05bc", "ו\u05b9", "י\u05b4", "א\u05b7", "א\u05b8", "ײ\u05b7", "ב\u05bf", "כ\u05bf", "פ\u05bf",
      # A form that the font does not have: it decomposes by one step when
      # no mark follows it, and fully when one does.
      "\ufb2c", "\ufb2c\u05b8", "\ufb2d", "\ufb2a", "\ufb49",
      # A sheva or a hiriq after a patah or a qamats goes after a meteg or
      # a mark below.
      "ב\u05b8\u05b0\u05bd", "ב\u05b7\u05b4\u0591", "ב\u05b0\u05b8\u05bd",
      "(שלום)", "[א]", "<ב>", "{ג}", "«ש»", "בית־ספר", "א" + ZWJ + "ב", "ג\u05b5׳"],
     "אבגדהוזחטיכלמנסעפצקרשתךםןףץװױײ" + "\u05b0\u05b1\u05b2\u05b3\u05b4\u05b5\u05b6\u05b7\u05b8\u05b9\u05bb\u05bc"
     "\u05bd\u05bf\u05c1\u05c2\u0591\u05a5" + "()[]" + ZWJ + " "),
    ("thai", "thai", "thai", "Thai", "NotoSansThai", set(), None,
     ["ภาษาไทย", "สว\u0e31สด\u0e35", "น\u0e49ำ", "กำ", "ป\u0e48ำ", "ป\u0e34\u0e48น", "ป\u0e48", "ป\u0e31\u0e49", "ญ\u0e38", "ฐ\u0e39", "ฎ\u0e38", "ฏ\u0e39", "ก\u0e48", "ก\u0e34\u0e48ำ", "ำ", " ำ",
      "ก\u0e4d\u0e48", "ญ\u0e34", "ก\u0e3a\u0e38", "(ไทย)"],
     "กขคงจญฎฏฐณดตนบปผฝพฟมยรลวสหอฮ" + "\u0e31ำ\u0e34\u0e35\u0e36\u0e37\u0e38\u0e39\u0e3a\u0e47\u0e48\u0e49"
     "\u0e4a\u0e4b\u0e4c\u0e4d" + "าเแโใไ" + ZWJ + ZWNJ),
    ("lao ", "lao", "lao ", "Laoo", "NotoSansLao", set(), None,
     ["ພາສາລາວ", "ສະບາຍດ\u0eb5", "ນ\u0ec9ຳ", "ກຳ", "ກ\u0eb4\u0ec9ຳ", "ຳ", "ໝ\u0eb9\u0ec8", "ຫ\u0ebcາຍ"],
     "ກຂຄງຈຊຍດຕຖທນບປຜຝພຟມຢຣລວສຫອຮ" + "\u0eb1ຳ\u0eb4\u0eb5\u0eb6\u0eb7\u0eb8\u0eb9\u0ebb\u0ebc\u0ec8\u0ec9"
     "\u0eca\u0ecb\u0ecc\u0ecd" + "າເແໂໃໄ" + ZWJ + ZWNJ),
    ("khmr", "khmr", "pref", "Khmr", "NotoSansKhmer", set(), None, KHMER_WORDS, KHMER_ALPHABET),
    ("khmr", "khmr_cfar", "cfar", "Khmr", "NotoSansKhmer", set(), (b"pstf", b"cfar"), KHMER_WORDS, KHMER_ALPHABET),
]

RANDOM_CASES = 40


# The subset of the font at `path` that holds the characters of `texts`
# but those of `left_out`, with no outlines. A Thai subset also gets an
# empty glyph for each code point of PUA, each with its own advance.
def subset(path, texts, left_out, thai):
    unicodes = sorted(({ord(c) for t in texts for c in t} | {0x20, 0x25CC}) - left_out)
    font = gen.subset_font(str(path), unicodes, FEATURES)
    empty = TTGlyphPen(None).glyph()
    for name in font.getGlyphOrder():
        font["glyf"][name] = empty
    if thai:
        names = ["pua%04X" % u for u in PUA]
        font.setGlyphOrder(font.getGlyphOrder() + names)
        for k, name in enumerate(names):
            font["glyf"].glyphs[name] = empty
            font["hmtx"].metrics[name] = (300 + k, 0)
        for t in font["cmap"].tables:
            if t.isUnicode():
                t.cmap.update(zip(PUA, names))
    return gen.saved(font)


def shaped(data, text, script):
    font = hb.Font(hb.Face(data))
    buf = hb.Buffer()
    buf.add_str(text)
    buf.direction = "rtl" if script == "Hebr" else "ltr"
    buf.script = script
    buf.language = "zxx"
    hb.shape(font, buf, {})
    infos = list(buf.glyph_infos)
    positions = list(buf.glyph_positions)
    if script == "Hebr":
        infos.reverse()
        positions.reverse()
    places = ["%d:%d:%d" % (p.x_advance, p.x_offset, p.y_offset) for p in positions]
    return [i.codepoint for i in infos], [i.cluster for i in infos], places


def line(data, text, which, script):
    glyphs, clusters, places = shaped(data, text, script)
    return "%s\t%s\t%s\t%s\t%s" % (which, text, ",".join(map(str, glyphs)), ",".join(map(str, clusters)), ",".join(places))


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    fonts = Path(sys.argv[1])
    parts = ["// Generated by scripts/gen-shape-script-vectors.py -- do not edit by hand.", ""]
    for tag, key, renamed, script, name, left_out, edit, words, alphabet in SCRIPTS:
        rng = random.Random(tag if key == tag.strip() else key)
        randoms = ["".join(rng.choice(alphabet) for _ in range(rng.randint(2, 8))) for _ in range(RANDOM_CASES)]
        texts = words + randoms
        data = subset(fonts / (name + "-Regular.ttf"), texts, left_out, script == "Thai")
        if edit:
            assert data.count(edit[0]) == 1 and data.count(edit[1]) == 0
            data = data.replace(*edit)
        assert data.count(renamed.encode()) >= 1
        old = data.replace(renamed.encode(), (renamed[:3] + "0").encode())
        lines = [line(data, t, "new", script) for t in texts] + [line(old, t, "old", script) for t in texts]
        parts += [
            "// A subset of %s, Copyright 2022 The Noto Project Authors, under the" % name,
            "// SIL Open Font License 1.1, with no outlines. The names of the font hold",
            "// the notice and the license.",
            "pub function %s_font(): string {" % key,
            '  return "%s";' % base64.b64encode(data).decode("ascii"),
            "}",
            "",
            "// The cases of %s_font(): \"new\" or \"old\", the text, and the glyphs, the" % key,
            "// clusters and the positions that HarfBuzz gives. With \"old\" the tag",
            "// \"%s\" of the font is renamed \"%s0\"." % (renamed, renamed[:3]),
            "pub function %s_cases(): string {" % key,
            '  return "%s";' % "\\n".join(gen.literal(entry) for entry in lines),
            "}",
            "",
        ]
    OUT.write_text("\n".join(parts))


if __name__ == "__main__":
    main()
