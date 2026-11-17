#!/usr/bin/env python3
"""Write tests/stdlib/bidi_vectors.fern: the two conformance files of the
Unicode Bidirectional Algorithm, BidiTest.txt and BidiCharacterTest.txt,
with no comments, compressed with xz and in base64.

Run:  python3 scripts/gen-bidi-vectors.py 17.0.0 [--ucd DIR]

The files are fetched from unicode.org, or read from DIR. Use the Unicode
version of cmd/unicodegen/bididata.txt.
"""

import base64
import lzma
import os
import sys
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def ucd_file(version: str, ucd_dir: str, name: str) -> str:
    if ucd_dir:
        return (Path(ucd_dir) / name).read_text(encoding="utf-8")
    url = "https://www.unicode.org/Public/%s/ucd/%s" % (version, name)
    with urllib.request.urlopen(url) as r:
        return r.read().decode("utf-8")


def packed(text: str) -> str:
    lines = [line for line in text.splitlines() if line and not line.startswith("#")]
    data = ("\n".join(lines) + "\n").encode("utf-8")
    return base64.b64encode(lzma.compress(data, preset=9 | lzma.PRESET_EXTREME)).decode("ascii")


def main() -> None:
    args = sys.argv[1:]
    if not args or len(args) not in (1, 3) or len(args) == 3 and args[1] != "--ucd":
        sys.exit("usage: %s VERSION [--ucd DIR]" % os.path.basename(sys.argv[0]))
    version = args[0]
    ucd_dir = args[2] if len(args) == 3 else ""
    out = ["// The conformance files of UAX #9 of Unicode %s, with no comments," % version,
           "// compressed with xz and in base64. scripts/gen-bidi-vectors.py writes",
           "// this file. Do not edit it.",
           ""]
    for fn, name in (("class_test", "BidiTest.txt"), ("character_test", "BidiCharacterTest.txt")):
        out.append("// %s." % name)
        out.append("pub function %s(): string {" % fn)
        out.append('  return "%s";' % packed(ucd_file(version, ucd_dir, name)))
        out.append("}")
        out.append("")
    (ROOT / "tests/stdlib/bidi_vectors.fern").write_text("\n".join(out[:-1]) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
