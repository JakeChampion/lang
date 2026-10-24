package lexer

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNonASCIIIdentifiersRejected(t *testing.T) {
	for _, tc := range []struct {
		name, src, wantMsg string
		wantCol            int
	}{
		{"e-acute", "let café = 7;", "identifiers must be ASCII; found 'é'", 8},
		{"e-circumflex", "let cafê = 7;", "identifiers must be ASCII; found 'ê'", 8},
		{"greek-start", "let Ω = 7;", "identifiers must be ASCII; found 'Ω'", 5},
		{"cyrillic", "let привет = 7;", "identifiers must be ASCII; found 'п'", 5},
		{"em-dash", "x — 1", "unexpected character '—'", 3},
		{"emoji", "let 🎉 = 1;", "unexpected character '🎉'", 5},
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
			if lexErr.Pos.Col != tc.wantCol {
				t.Errorf("col = %d, want %d (first byte of the character)", lexErr.Pos.Col, tc.wantCol)
			}
		})
	}
}

func TestIdentifierTextIsWellFormed(t *testing.T) {
	toks, _, _ := Tokenize("caf")
	for _, tok := range toks {
		if !utf8.ValidString(tok.Text) {
			t.Errorf("token %v has invalid UTF-8 text %q", tok.Kind, tok.Text)
		}
	}
}

func TestNonASCIIAllowedInStringsAndComments(t *testing.T) {
	toks, comments, err := Tokenize(`let s = "café Ω 🎉"; // façade Ω`)
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

func TestInvalidUTF8Byte(t *testing.T) {
	_, _, err := Tokenize("let x = \xff;")
	if err == nil {
		t.Fatal("Tokenize succeeded on invalid UTF-8, want an error")
	}
	if got, want := err.(*Error).Msg, "invalid UTF-8 byte 0xFF"; got != want {
		t.Errorf("msg = %q, want %q", got, want)
	}
}

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

func TestContinuationByteIsNotWhitespace(t *testing.T) {
	_, _, err := Tokenize("let x\xa0= 1;")
	if err == nil {
		t.Fatal("Tokenize swallowed a 0xA0 continuation byte as whitespace")
	}
}

func TestInvalidUTF8InLiteralOrComment(t *testing.T) {
	for _, tc := range []struct {
		src       string
		line, col int
	}{
		{"let s = \"a\xb2\";", 1, 11},
		{"let x = 1;\n// caf\xe9\nlet y = 2;", 2, 7},
		{"let s = \"\xed\xa0\x80\";", 1, 10},
	} {
		_, _, err := Tokenize(tc.src)
		lerr, ok := err.(*Error)
		if !ok {
			t.Fatalf("Tokenize(%q) = %v, want a lex error", tc.src, err)
		}
		if !strings.HasPrefix(lerr.Msg, "invalid UTF-8 byte 0x") || lerr.Pos.Line != tc.line || lerr.Pos.Col != tc.col {
			t.Errorf("Tokenize(%q) = %s at %d:%d, want invalid UTF-8 at %d:%d", tc.src, lerr.Msg, lerr.Pos.Line, lerr.Pos.Col, tc.line, tc.col)
		}
	}
}
