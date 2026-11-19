#!/usr/bin/env python3
"""Write tests/stdlib/shape_script_vectors.fern: for Hebrew, Thai, Lao,
Khmer, Myanmar and six scripts of the Universal Shaping Engine, a subset of
a Noto font and the glyphs that HarfBuzz gives for a list of words and of
random strings, for the tests of std/shape.

Run:  python3 -I scripts/gen-shape-script-vectors.py FONTDIR

FONTDIR holds NotoSansHebrew-Regular.ttf, NotoSansThai-Regular.ttf,
NotoSansLao-Regular.ttf, NotoSansKhmer-Regular.ttf,
NotoSansMyanmar-Regular.ttf, NotoSansSinhala-Regular.ttf,
NotoSansBalinese-Regular.ttf, NotoSansJavanese-Regular.ttf,
NotoSerifTibetan-Regular.ttf, NotoSansMongolian-Regular.ttf and
NotoSansNKo-Regular.ttf, from the Noto project (notofonts.github.io), and
NotoSansCJKkr-Regular.otf, from github.com/notofonts/noto-cjk. They
are under the SIL Open Font License 1.1, and each subset keeps each name of
its font, so it keeps the notice and the license. A subset holds the
characters of its cases, and its glyphs have no outlines.

Each case is shaped twice: with the font, and with one tag of the font
renamed, as the test renames it. For Hebrew the tag is the GPOS feature
mark, so that the shaper composes the presentation forms. For Thai it is
the script thai, so that the shaper uses the glyphs of the private use
area. The Thai subset gets such glyphs, with no outlines. For Lao it is the
script tag. For Khmer it is the feature pref. A second Khmer subset has its
feature pstf renamed cfar, and the test renames cfar. For Myanmar it is the
script mym2, so that the font has no script. A second Myanmar subset has
the old script tag mymr, which takes the default shaper, and the test
renames it. For the scripts of the Universal Shaping Engine it is the
script tag, so that the font takes the default shaper. The Hebrew subset
does not have U+FB2C and U+FB2D, so that each of them decomposes by one
step only. The random strings come from a fixed seed, so the script writes
the same file each time.
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
            "mkmk", "dist", "abvm", "blwm", "rphf", "pref", "nukt", "akhn", "rkrf", "half", "vatu", "cjct", "haln", "isol", "init", "medi", "fina", "ljmo", "vjmo", "tjmo", "blwf", "abvf", "pstf", "cfar", "pres", "abvs", "blws", "psts"]

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

MYANMAR_WORDS = ["မြန်မာ", "မင်္ဂလာ", "သင်္ချိုင်း", "ကျွန်ုပ်", "ကြ", "ကျ", "ကွ", "ကှ", "ကေ", "ကော်", "ကို", "ကုံ",
                 "ကြော", "ကြွေ", "ဿ", "ပ္ပ", "ဥ", "၎င်း", "က\ufe00\u1031", "\u1031", "\u103a", "ၵ\u1062", "\u200d"]
MYANMAR_ALPHABET = "ကခဂငစဆဇညတထဒနပဖဗမယရလဝသဟအ" + "\u1039\u103a\u103b\u103c\u103d\u103e\u102b\u102c\u102d\u102e\u102f\u1030\u1031\u1032\u1036\u1037\u1038" + ZWJ

# The words of the scripts of the Universal Shaping Engine, and the
# alphabets of their random strings.
SINHALA_WORDS = ["ශ්\u200dරී ලංකා", "ක\u0dd9", "ක\u0dda", "ක\u0ddc", "ක\u0ddd", "ක\u0dde", "ක්\u200dය", "ක්\u200dර",
                 "ර්\u200dක", "ද්\u200dධ", "ස්ත්\u200dරී", "අ\u0dcf", "ඔ\u0dca", "\u0dca", "ක\u0dca\u200d", "ක\u034f\u0dca"]
SINHALA_ALPHABET = "කගචජටඩතදනපබමයරලවසහළ" + "\u0dca\u0dca\u0dcf\u0dd0\u0dd1\u0dd2\u0dd3\u0dd4\u0dd6\u0dd8\u0dd9\u0dda\u0ddb\u0ddc\u0dde\u0d82\u0d83" + ZWJ + ZWNJ
BALINESE_WORDS = ["ᬩᬮᬶ", "ᬓ\u1b3e", "ᬓ\u1b44ᬭ", "ᬭ\u1b44ᬓ", "ᬓ\u1b3f", "ᬓ\u1b40", "ᬅᬓ\u1b44ᬱᬭ", "ᬓ\u1b03", "ᬓ\u1b44ᬓ\u1b3e"]
BALINESE_ALPHABET = "ᬓᬕᬗᬘᬚᬢᬤᬦᬧᬩᬫᬬᬭᬮᬯᬲᬳ" + "\u1b44\u1b44\u1b35\u1b36\u1b37\u1b38\u1b3a\u1b3e\u1b3f\u1b40\u1b42\u1b00\u1b01\u1b02\u1b03\u1b04\u1b34" + ZWJ + ZWNJ
JAVANESE_WORDS = ["ꦗꦮ", "ꦏ\ua9ba", "ꦏ\ua9bd", "ꦏ\ua9c0ꦫ", "ꦫ\ua9c0ꦏ", "ꦏ\ua9bf", "ꦏ\ua9bc", "ꦏ\ua9be", "ꦏ\ua9ba\ua9b4"]
JAVANESE_ALPHABET = "ꦏꦒꦔꦕꦗꦠꦢꦤꦥꦧꦩꦪꦫꦭꦮꦱꦲ" + "\ua9c0\ua9c0\ua9b4\ua9b6\ua9b7\ua9b8\ua9b9\ua9ba\ua9bb\ua9bc\ua9bd\ua9be\ua9bf\ua980\ua981\ua982\ua983" + ZWJ + ZWNJ
TIBETAN_WORDS = ["བོད་", "སྐད", "ཀྵ", "ཀྐ", "ཀཱི", "ཀ\u0f39", "སྒྲ", "\u0f71", "ཀ\u0f7e\u0f72", "ཀ\u0f80\u0f71"]
TIBETAN_ALPHABET = "ཀགངདནབམརལས" + "\u0f90\u0f92\u0fb1\u0fb2\u0f71\u0f72\u0f74\u0f7a\u0f7c\u0f7e\u0f84\u0f39"
MONGOLIAN_WORDS = ["ᠮᠣᠩᠭᠣᠯ", "ᠠ\u180b", "ᠠᠢ", "ᠨ\u180bᠠ", "ᠠ\u180eᠠ", "ᠬᠠ", "ᠭ\u180cᠠ", "ᠨ\u180f"]
MONGOLIAN_ALPHABET = "ᠠᠡᠢᠣᠤᠨᠬᠭᠮᠯᠰᠲ" + "\u180b\u180c\u180d\u180e\u180f\u18a9" + ZWJ + ZWNJ
NKO_WORDS = ["ߒߞߏ", "ߊ\u07eb", "ߓߊ", "(ߓ)", "ߓ\u07f3ߊ", "ߞ\u200dߞ"]
NKO_ALPHABET = "ߊߋߌߍߎߏߐߒߓߔߕߖߗߘߙߚߛߜߝߞߟߠߡߢߣߤ" + "\u07eb\u07ec\u07ed\u07ee\u07ef\u07f0\u07f1\u07f2\u07f3" + ZWJ + "()"

HANGUL_WORDS = ["한국어", "\u1112\u1161\u11ab", "\u1100\u119e", "\u1112\u119e\u11ab", "가\u302e", "\u302e", "\u302f\u302e",
                "가\u11a8", "가\u11c3", "\u1100\u1161\u11c3", "\ua960\u1161", "\u1100\ud7b0", "한\u302f"]
HANGUL_ALPHABET = ("\u1100\u1102\u1103\u1105\u1106\u1107\u1109\u110b\u110c\u1112\u1113\u111d\u1140\ua960"
                   "\u1161\u1165\u1169\u116e\u1173\u1175\u119e\u11a1\ud7b0"
                   "\u11a8\u11ab\u11af\u11b7\u11b8\u11bc\u11c3\u11eb\ud7cb" "가나다한국\u302e\u302f")

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
    ("mymr", "mymr", "mym2", "Mymr", "NotoSansMyanmar", set(), None, MYANMAR_WORDS, MYANMAR_ALPHABET),
    ("mymr", "mymr_old", "mymr", "Mymr", "NotoSansMyanmar", set(), (b"mym2", b"mymr"), MYANMAR_WORDS, MYANMAR_ALPHABET),
    ("sinh", "sinh", "sinh", "Sinh", "NotoSansSinhala", set(), None, SINHALA_WORDS, SINHALA_ALPHABET),
    ("bali", "bali", "bali", "Bali", "NotoSansBalinese", set(), None, BALINESE_WORDS, BALINESE_ALPHABET),
    ("java", "java", "java", "Java", "NotoSansJavanese", set(), None, JAVANESE_WORDS, JAVANESE_ALPHABET),
    ("tibt", "tibt", "tibt", "Tibt", "NotoSerifTibetan", set(), None, TIBETAN_WORDS, TIBETAN_ALPHABET),
    ("mong", "mong", "mong", "Mong", "NotoSansMongolian", set(), None, MONGOLIAN_WORDS, MONGOLIAN_ALPHABET),
    ("nko ", "nko", "nko ", "Nkoo", "NotoSansNKo", set(), None, NKO_WORDS, NKO_ALPHABET),
    ("hang", "hang", "hang", "Hang", "NotoSansCJKkr", set(), None, HANGUL_WORDS, HANGUL_ALPHABET),
]

RANDOM_CASES = 40

# The scripts that go from right to left.
RTL = ("Hebr", "Nkoo")


# The subset of the font at `path` that holds the characters of `texts`
# but those of `left_out`, with no outlines. A Thai subset also gets an
# empty glyph for each code point of PUA, each with its own advance.
def subset(path, texts, left_out, thai):
    unicodes = sorted(({ord(c) for t in texts for c in t} | {0x20, 0x25CC}) - left_out)
    font = gen.subset_font(str(path), unicodes, FEATURES)
    empty = TTGlyphPen(None).glyph()
    if "glyf" in font:
        for name in font.getGlyphOrder():
            font["glyf"][name] = empty
    else:
        # A CFF font: each charstring draws nothing. The advances stay in hmtx.
        strings = font["CFF "].cff.topDictIndex[0].CharStrings
        for name in strings.keys():
            strings[name].program = ["endchar"]
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
    buf.direction = "rtl" if script in RTL else "ltr"
    buf.script = script
    buf.language = "zxx"
    hb.shape(font, buf, {})
    infos = list(buf.glyph_infos)
    positions = list(buf.glyph_positions)
    if script in RTL:
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
        path = fonts / (name + "-Regular.ttf")
        data = subset(path if path.exists() else path.with_suffix(".otf"), texts, left_out, script == "Thai")
        if edit:
            assert data.count(edit[0]) >= 1
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
