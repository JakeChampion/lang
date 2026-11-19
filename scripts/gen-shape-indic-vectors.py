#!/usr/bin/env python3
"""Write tests/stdlib/shape_indic_vectors.fern: for each Indic script other
than Devanagari, a subset of its Noto Sans font and the glyphs that
HarfBuzz gives for a list of words and of random strings, for the tests
of std/shape.

Run:  python3 -I scripts/gen-shape-indic-vectors.py FONTDIR

FONTDIR holds NotoSansBengali-Regular.ttf, NotoSansGurmukhi-Regular.ttf,
NotoSansGujarati-Regular.ttf, NotoSansOriya-Regular.ttf,
NotoSansTamil-Regular.ttf, NotoSansTelugu-Regular.ttf,
NotoSansKannada-Regular.ttf and NotoSansMalayalam-Regular.ttf, from the
Noto project (notofonts.github.io). They are under the SIL Open Font
License 1.1, and each subset keeps each name of its font, so it keeps the
notice and the license. A subset holds the characters of its cases, with
the Indic features, and its glyphs have no outlines.

Each case is shaped twice: with the new script tag of the font, and with
that tag renamed, as the test renames it, so that the rules of the old
Indic specification apply, or the default shaper when the font has a
DFLT script. The random strings come from a fixed seed, so the script
writes the same file each time.
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

OUT = HERE.parent / "tests" / "stdlib" / "shape_indic_vectors.fern"

ZWJ, ZWNJ = "‍", "‌"

# For each script: its tag, its new tag, its HarfBuzz script, its font,
# its words, and the alphabet of its random strings.
SCRIPTS = [
    ("beng", "bng2", "Beng", "NotoSansBengali",
     ["বাংলা", "কি", "কে", "কৈ", "কো", "কৌ", "র্ক", "ক্র", "ক্ষ", "স্ত্র", "ত্" + ZWJ, "ক্য", "য়", "ড়", "ৎ",
      "র্কি", "শ্রী", "জ্ঞান", "বিদ্যালয়", "আমার", "সোনার", "ক্" + ZWNJ + "ষ", "ৰ্ক", "র" + ZWJ + "্য", "অা"],
     "কখগতনমযরলসহ্ািীুূৃেৈোৌংঃঁ়" + ZWJ + ZWNJ),
    ("guru", "gur2", "Guru", "NotoSansGurmukhi",
     ["ਪੰਜਾਬੀ", "ਕਿ", "ਕੀ", "ਕੁ", "ਕ੍ਰ", "ਪ੍ਰ", "ਸ੍ਵ", "ਕ੍ਹ", "ਗੁਰਮੁਖੀ", "ਖ਼", "ਸ਼", "ਕਿੰ", "ਕੀਂ", "ਅਾ", "ੲਿ",
      "ਕੱਟ", "ਕ੍ਯ", "ਕੵ",
      # A ligature of glyphs from two syllables, before the pre-base matra
      # of the next one: its cluster does not reach past that matra.
      "\u0a4d\u0a30\u0a4d\u0a4d\u0a2e\u0a3f"],
     "ਕਖਗਤਨਮਯਰਲਵਸਹ੍ਾਿੀੁੂੇੈੋੌਂੰੱ਼" + ZWJ + ZWNJ),
    ("gujr", "gjr2", "Gujr", "NotoSansGujarati",
     ["ગુજરાતી", "કિ", "ર્ક", "ક્ર", "ક્ષ", "જ્ઞ", "દ્ધ", "શ્ર", "કૅ", "કૉ", "ર્કિ", "અા", "ક્" + ZWJ + "ષ", "હૃ",
      "દૃ", "ર્ક્ષ",
      # Two marks on the parts of the rakar ligature of rkrf: no later
      # lookup joins them.
      "\u0afe\u0a95\u0aaf\u0aff\u0acd\u0ab0\u0afb\u0a99"],
     "કખગતદનમયરલવસહ્ાિીુૂૃેૈોૌંઃઁ઼" + ZWJ + ZWNJ),
    ("orya", "ory2", "Orya", "NotoSansOriya",
     ["ଓଡ଼ିଆ", "କି", "କେ", "କୈ", "କୋ", "କୌ", "ର୍କ", "କ୍ର", "କ୍ଷ", "ସ୍ତ୍ର", "କ୍ୟ", "ଁକ", "କଁ", "ର୍କି", "ଅା", "ଏୗ",
      "\u0b4d\u0b28\u0b4d\u0b4d\u0b22\u0b47"],
     "କଖଗତନମଯରଲସହ୍ାିୀୁୂୃେୈୋୌଂଃଁ଼ୖୗ" + ZWJ + ZWNJ),
    ("taml", "tml2", "Taml", "NotoSansTamil",
     ["தமிழ்", "கி", "கெ", "கே", "கை", "கொ", "கோ", "கௌ", "க்ஷ", "ஶ்ரீ", "க்", "ர்க", "அூ", "ஔ", "ஸ்ரீ", "கு",
      "கூ"],
     "கஙசதநமயரலவழளஷ்ாிீுூெேைொோௌௗஂ" + ZWJ + ZWNJ),
    ("telu", "tel2", "Telu", "NotoSansTelugu",
     ["తెలుగు", "కి", "కె", "కై", "కొ", "కో", "క్ర", "క్ష", "ర్క", "ర్" + ZWJ + "క", "స్త్ర", "కృ", "క్య", "ఒౌ", "కౕ",
      "ర్" + ZWJ],
     "కఖగతనమయరలవసహ్ాిీుూృెేైొోౌంఃౕౖ" + ZWJ + ZWNJ),
    ("knda", "knd2", "Knda", "NotoSansKannada",
     ["ಕನ್ನಡ", "ಕಿ", "ಕೆ", "ಕೈ", "ಕೊ", "ಕೋ", "ಕ್ರ", "ಕ್ಷ", "ರ್ಕ", "ರ್" + ZWJ + "ಕ", "ಸ್ತ್ರ", "ಕೃ", "ಕ್ಯ", "ಉಾ",
      "ರ್ಕಿ", "ರ್ಕ್ಷ"],
     "ಕಖಗತನಮಯರಲವಸಹ್ಾಿೀುೂೃೆೇೈೊೋೌಂಃೕೖ಼" + ZWJ + ZWNJ),
    ("mlym", "mlm2", "Mlym", "NotoSansMalayalam",
     ["മലയാളം", "കി", "കെ", "കേ", "കൈ", "കൊ", "കോ", "കൌ", "ക്ര", "ക്ഷ", "ൎക", "ര്" + ZWJ, "ൻ", "ന്റ",
      "സ്ത്ര", "ക്യ", "ഇൗ", "ഒാ"],
     "കഖഗതനമയരലവസഹ്ാിീുൂൃെേൈൊോൌൗംഃൎ" + ZWJ + ZWNJ),
]

RANDOM_CASES = 40


# The subset of the font at `path` that holds the characters of `texts`,
# with no outlines.
def subset(path, texts):
    unicodes = sorted({ord(c) for t in texts for c in t} | {0x20, 0x25CC})
    font = gen.subset_font(str(path), unicodes, gen.DEVA_FEATURES)
    empty = TTGlyphPen(None).glyph()
    for name in font.getGlyphOrder():
        font["glyf"][name] = empty
    return gen.saved(font)


def shaped(data, text, script):
    font = hb.Font(hb.Face(data))
    buf = hb.Buffer()
    buf.add_str(text)
    buf.direction = "ltr"
    buf.script = script
    buf.language = "zxx"
    hb.shape(font, buf, {})
    places = ["%d:%d:%d" % (p.x_advance, p.x_offset, p.y_offset) for p in buf.glyph_positions]
    return [i.codepoint for i in buf.glyph_infos], [i.cluster for i in buf.glyph_infos], places


def line(data, text, tag, script):
    glyphs, clusters, places = shaped(data, text, script)
    return "%s\t%s\t%s\t%s\t%s" % (tag, text, ",".join(map(str, glyphs)), ",".join(map(str, clusters)), ",".join(places))


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    fonts = Path(sys.argv[1])
    parts = ["// Generated by scripts/gen-shape-indic-vectors.py -- do not edit by hand.", ""]
    for tag, new_tag, script, name, words, alphabet in SCRIPTS:
        rng = random.Random(tag)
        randoms = ["".join(rng.choice(alphabet) for _ in range(rng.randint(2, 8))) for _ in range(RANDOM_CASES)]
        texts = words + randoms
        data = subset(fonts / (name + "-Regular.ttf"), texts)
        assert data.count(new_tag.encode()) >= 1
        old = data.replace(new_tag.encode(), (new_tag[:3] + "0").encode())
        lines = [line(data, t, "new", script) for t in texts] + [line(old, t, "old", script) for t in texts]
        parts += [
            "// A subset of %s, Copyright 2022 The Noto Project Authors, under the" % name,
            "// SIL Open Font License 1.1, with no outlines. The names of the font hold",
            "// the notice and the license.",
            "pub function %s_font(): string {" % tag,
            '  return "%s";' % base64.b64encode(data).decode("ascii"),
            "}",
            "",
            "// The cases of %s_font(): the spec, \"new\" or \"old\", the text, and the" % tag,
            "// glyphs, the clusters and the positions that HarfBuzz gives. With \"old\"",
            "// the tag %s of the font is renamed %s0." % (new_tag, new_tag[:3]),
            "pub function %s_cases(): string {" % tag,
            '  return "%s";' % "\\n".join(gen.literal(l) for l in lines),
            "}",
            "",
        ]
    OUT.write_text("\n".join(parts))


if __name__ == "__main__":
    main()
