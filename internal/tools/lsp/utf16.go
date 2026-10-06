package lsp

import "strings"

// The Language Server Protocol measures a Position's Character in UTF-16 code
// units unless the client negotiates otherwise; Fern measures columns in UTF-8
// bytes. The two agree only while a line is pure ASCII, and this server passed
// byte offsets straight through in both directions (#8468).
//
// Any non-ASCII character before the position of interest shifted everything
// after it: hover targeted the wrong token, go-to-definition landed mid-word,
// and a diagnostic squiggle underlined the wrong span. The drift is per
// character — 1 unit for a 2- or 3-byte rune, 0 for a 4-byte one (which is one
// byte wider than the surrogate pair it becomes) — so it accumulates in either
// direction depending on the mix.
//
// This is not exotic input here: the stdlib's own comments are full of
// box-drawing and arrow characters, and so are the diagnostics this server
// surfaces.
//
// Conversion happens at the protocol boundary only. Everything inside keeps
// byte columns, which is what ast.Position carries and what every locate /
// hover / rename path already compares against.

// utf16Len is how many UTF-16 code units s occupies — what LSP counts.
// A rune outside the BMP becomes a surrogate pair, so it counts 2.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// lineTextAt returns the text of src's 1-based line n, without its newline.
// Empty when n is out of range, which makes both converters fall back to
// treating the position as ASCII rather than panicking on a stale position.
func lineTextAt(src string, n int) string {
	if n < 1 {
		return ""
	}
	for i := 1; ; i++ {
		nl := strings.IndexByte(src, '\n')
		if i == n {
			if nl < 0 {
				return src
			}
			return strings.TrimSuffix(src[:nl], "\r")
		}
		if nl < 0 {
			return ""
		}
		src = src[nl+1:]
	}
}

// byteColForUTF16 converts a 0-based UTF-16 character offset within line into
// the 1-based byte column Fern uses. A position past the end of the line
// clamps to just past its last byte, which is what an editor sends for a
// cursor at end-of-line.
func byteColForUTF16(line string, u16 int) int {
	if u16 <= 0 {
		return 1
	}
	units := 0
	for i, r := range line {
		if units >= u16 {
			return i + 1
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return len(line) + 1
}

// utf16ColForByte converts a 1-based byte column within line into the 0-based
// UTF-16 character offset LSP expects. The inverse of byteColForUTF16.
func utf16ColForByte(line string, col int) int {
	if col <= 1 {
		return 0
	}
	b := col - 1
	if b > len(line) {
		b = len(line)
	}
	return utf16Len(line[:b])
}

// srcFor returns the text to measure positions in uri against, or "" when this
// docState does not hold that document.
//
// Two ways it can not: a workspace request hands the ENTRY file's docState to a
// request naming another file, and a rename or go-to-definition result can land
// in a different module than the one asked about. Converting a position with
// the WRONG file's text is worse than not converting — it silently moves the
// position — so those fall back to byte columns, which is what every position
// was before (#8468). The common case, a cursor in the document the request
// named, converts properly.
func srcFor(state *docState, uri string) string {
	if state == nil || state.uri != uri {
		return ""
	}
	return state.src
}
