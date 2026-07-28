// Package gasstr decodes the GNU-as string literals the code generators emit
// into .ascii / .asciz / .string directives.
//
// It exists because strconv.Unquote is the wrong tool for this and fails
// SILENTLY. A Go quoted string is UTF-8 text, so Unquote decodes each byte it
// reads as a rune and re-encodes it: a lone 0x80 is invalid UTF-8, decodes to
// utf8.RuneError, and comes back out as U+FFFD's three bytes (ef bf bd) — no
// error returned, just three wrong bytes where one right one belonged, and a
// literal whose length no longer matches the .4byte length the generator
// emitted alongside it.
//
// GNU as has no such notion: a .asciz operand is a byte string, and both
// emitters (x86_64's escapeForGAS and its arm64 twin) deliberately pass bytes
// >= 0x80 through raw. So assembling the SAME text in-process and via gcc
// produced different data — a divergence invisible in any program whose
// literals are pure ASCII, and one that corrupts every binary blob carried in
// \xNN escapes (the self-host wasm component framing in watbin.fern is exactly
// that, which is how this surfaced).
//
// Decoding is therefore byte-wise, never rune-wise.
package gasstr

import "fmt"

// Unquote decodes one double-quoted GNU-as string operand into its bytes,
// resolving the escapes GAS recognises and passing every other byte —
// including any byte >= 0x80 — through unchanged.
func Unquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("not a quoted string: %q", s)
	}
	body := s[1 : len(s)-1]
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i >= len(body) {
			return "", fmt.Errorf("trailing backslash in %q", s)
		}
		switch e := body[i]; {
		case e == 'n':
			out = append(out, '\n')
		case e == 't':
			out = append(out, '\t')
		case e == 'r':
			out = append(out, '\r')
		case e == 'b':
			out = append(out, '\b')
		case e == 'f':
			out = append(out, '\f')
		case e == 'v':
			out = append(out, '\v')
		case e == 'a':
			out = append(out, 7)
		case e == '\\' || e == '"' || e == '\'':
			out = append(out, e)
		case e >= '0' && e <= '7':
			// Octal, one to three digits — GAS truncates to a byte.
			v := 0
			n := 0
			for n < 3 && i < len(body) && body[i] >= '0' && body[i] <= '7' {
				v = v*8 + int(body[i]-'0')
				i++
				n++
			}
			i-- // the loop's own i++ steps past the last digit
			out = append(out, byte(v))
		case e == 'x':
			// Hex, one or more digits — GAS keeps the low byte.
			v := 0
			n := 0
			for i+1 < len(body) && isHex(body[i+1]) {
				v = v*16 + hexVal(body[i+1])
				i++
				n++
			}
			if n == 0 {
				return "", fmt.Errorf("\\x with no hex digits in %q", s)
			}
			out = append(out, byte(v))
		default:
			return "", fmt.Errorf("unknown escape \\%c in %q", e, s)
		}
	}
	return string(out), nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}
