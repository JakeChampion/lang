package lexer

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The lexer scans bytes, so classifying with `unicode.IsLetter` treated a
// UTF-8 continuation byte as the Latin-1 character of that value. `é` is
// C3 A9 and A9 is `©` (not a letter) → rejected with a mojibake message
// pointing at the second byte of a character; `ê` is C3 AA and AA is `ª`
// (category Lo) → silently ACCEPTED, yielding an identifier whose text
// was not valid UTF-8. The accepted set was "code points whose
// continuation bytes happen to be ª, µ or º in Latin-1" — not a design.
//
// Identifiers are now ASCII-only by decision (docs/STRINGS-SOTA.md D11),
// and the diagnostic names the real character. #5628.
func TestNonASCIIIdentifiersRejected(t *testing.T) {
	for _, tc := range []struct {
		name, src, wantMsg string
		wantCol            int
	}{
		{"e-acute", "var café = 7;", "identifiers must be ASCII; found 'é'", 8},
		// The one that used to compile.
		{"e-circumflex", "var cafê = 7;", "identifiers must be ASCII; found 'ê'", 8},
		{"greek-start", "var Ω = 7;", "identifiers must be ASCII; found 'Ω'", 5},
		{"cyrillic", "var привет = 7;", "identifiers must be ASCII; found 'п'", 5},
		// A non-letter still gets the generic message.
		{"em-dash", "x — 1", "unexpected character '—'", 3},
		{"emoji", "var 🎉 = 1;", "unexpected character '🎉'", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Tokenize(tc.src)
			if err == nil {
				t.Fatalf("Tokenize(%q) succeeded, want an error", tc.src)
			}
			lexErr, ok := err.(*Error)
			if !ok {
				t.Fatalf("error is %T, want *lexer.Error", err)
			}
			if lexErr.Msg != tc.wantMsg {
				t.Errorf("msg = %q, want %q", lexErr.Msg, tc.wantMsg)
			}
			// The caret must land on the FIRST byte of the offending
			// character, not on a continuation byte.
			if lexErr.Pos.Col != tc.wantCol {
				t.Errorf("col = %d, want %d (first byte of the character)", lexErr.Pos.Col, tc.wantCol)
			}
		})
	}
}

// Every identifier token's text must be valid UTF-8 — the old byte-wise
// scan could stop mid-character and hand back a lone lead byte.
func TestIdentifierTextIsWellFormed(t *testing.T) {
	// `caf` lexes, then the é errors; the point is that no token ever
	// carries the stray C3.
	toks, _, _ := Tokenize("caf")
	for _, tok := range toks {
		if !utf8.ValidString(tok.Text) {
			t.Errorf("token %v has invalid UTF-8 text %q", tok.Kind, tok.Text)
		}
	}
}

// Non-ASCII is still free inside string literals and comments — only
// identifiers are constrained. This is the half of the contract that
// would be easy to break while tightening the other half.
func TestNonASCIIAllowedInStringsAndComments(t *testing.T) {
	toks, comments, err := Tokenize(`var s = "café Ω 🎉"; // façade Ω`)
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	var got string
	for _, tok := range toks {
		if tok.Kind == String {
			got = tok.Text
		}
	}
	if got != "café Ω 🎉" {
		t.Errorf("string literal = %q, want %q", got, "café Ω 🎉")
	}
	if len(comments) != 1 || !strings.Contains(comments[0].Text, "façade Ω") {
		t.Errorf("comments = %+v, want one containing %q", comments, "façade Ω")
	}
}

// A byte that starts no valid UTF-8 sequence is reported as a byte, not
// as U+FFFD — "invalid UTF-8" and "unsupported character" are different
// problems with different fixes.
func TestInvalidUTF8Byte(t *testing.T) {
	_, _, err := Tokenize("var x = \xff;")
	if err == nil {
		t.Fatal("Tokenize succeeded on invalid UTF-8, want an error")
	}
	if got, want := err.(*Error).Msg, "invalid UTF-8 byte 0xFF"; got != want {
		t.Errorf("msg = %q, want %q", got, want)
	}
}

// ASCII identifiers keep lexing exactly as before, including the
// underscore and digit rules.
func TestASCIIIdentifiersUnchanged(t *testing.T) {
	toks, _, err := Tokenize("_x a1 Z_9 __init__ x9y")
	if err != nil {
		t.Fatalf("Tokenize: %v", err)
	}
	for i, want := range []string{"_x", "a1", "Z_9", "__init__", "x9y"} {
		if toks[i].Kind != Ident || toks[i].Text != want {
			t.Errorf("tok[%d] = %v, want Ident %q", i, toks[i], want)
		}
	}
}

// A stray 0xA0 is a UTF-8 continuation byte, but `unicode.IsSpace` on
// that byte is true (it is NBSP in Latin-1), so the old trivia skipper
// would silently swallow it as whitespace.
func TestContinuationByteIsNotWhitespace(t *testing.T) {
	_, _, err := Tokenize("var x\xa0= 1;")
	if err == nil {
		t.Fatal("Tokenize swallowed a 0xA0 continuation byte as whitespace")
	}
}
