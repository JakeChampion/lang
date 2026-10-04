package interp

import (
	"strings"
	"unicode/utf8"
)

// builderText replaces each malformed maximal subpart with one U+FFFD.
// DecodeRune alone consumes one byte on a truncated sequence; this boundary
// must instead consume its complete valid prefix, without eating the next
// scalar or a continuation that violates a lead's restricted second byte.
func builderText(raw []byte) String {
	if utf8.Valid(raw) {
		return String(raw)
	}
	var out strings.Builder
	out.Grow(len(raw))
	for pos := 0; pos < len(raw); {
		lead := raw[pos]
		if lead < utf8.RuneSelf {
			start := pos
			for pos < len(raw) && raw[pos] < utf8.RuneSelf {
				pos++
			}
			out.Write(raw[start:pos])
			continue
		}
		width := 0
		switch {
		case lead >= 0xc2 && lead <= 0xdf:
			width = 2
		case lead >= 0xe0 && lead <= 0xef:
			width = 3
		case lead >= 0xf0 && lead <= 0xf4:
			width = 4
		}
		used := 1
		for used < width && pos+used < len(raw) {
			next := raw[pos+used]
			if next < 0x80 || next > 0xbf {
				break
			}
			if used == 1 && (lead == 0xe0 && next < 0xa0 ||
				lead == 0xed && next > 0x9f ||
				lead == 0xf0 && next < 0x90 ||
				lead == 0xf4 && next > 0x8f) {
				break
			}
			used++
		}
		if used == width {
			out.Write(raw[pos : pos+used])
		} else {
			out.WriteRune(utf8.RuneError)
		}
		pos += used
	}
	return String(out.String())
}
