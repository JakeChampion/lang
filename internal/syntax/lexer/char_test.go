package lexer

import (
	"strings"
	"testing"
)

func TestCharAndByteLiterals(t *testing.T) {
	cases := []struct {
		src  string
		kind Kind
		val  int32
	}{
		{`'x'`, Char, 'x'},
		{`'['`, Char, '['},
		{`' '`, Char, ' '},
		{`'\n'`, Char, '\n'},
		{`'\t'`, Char, '\t'},
		{`'\r'`, Char, '\r'},
		{`'\0'`, Char, 0},
		{`'\\'`, Char, '\\'},
		{`'\''`, Char, '\''},
		{`'\"'`, Char, '"'},
		{`'"'`, Char, '"'},
		{`'\x7F'`, Char, 0x7F},
		{`'\u{41}'`, Char, 'A'},
		{`'\u{1F600}'`, Char, 0x1F600},
		{`'\u{10FFFF}'`, Char, 0x10FFFF},
		{`'é'`, Char, 'é'},
		{`'😀'`, Char, 0x1F600},
		{`b'x'`, Byte, 'x'},
		{`b'['`, Byte, '['},
		{`b'\n'`, Byte, '\n'},
		{`b'\0'`, Byte, 0},
		{`b'\''`, Byte, '\''},
		{`b'\\'`, Byte, '\\'},
		{`b'\x1B'`, Byte, 0x1B},
		{`b'\xFF'`, Byte, 0xFF},
	}
	for _, c := range cases {
		toks, _, err := Tokenize(c.src)
		if err != nil {
			t.Errorf("Tokenize(%s): unexpected error %v", c.src, err)
			continue
		}
		if len(toks) != 2 || toks[1].Kind != EOF {
			t.Errorf("Tokenize(%s): got %d tokens, want 1 + EOF", c.src, len(toks))
			continue
		}
		got := toks[0]
		if got.Kind != c.kind {
			t.Errorf("Tokenize(%s): kind %v, want %v", c.src, got.Kind, c.kind)
		}
		if got.Scalar != c.val {
			t.Errorf("Tokenize(%s): scalar %d, want %d", c.src, got.Scalar, c.val)
		}
		if got.Text != c.src {
			t.Errorf("Tokenize(%s): text %q, want the source spelling", c.src, got.Text)
		}
	}
}

func TestCharAndByteLiteralErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`''`, "empty character literal"},
		{`b''`, "empty byte literal"},
		{`'ab'`, "must hold exactly one character"},
		{`'\n\n'`, "must hold exactly one character"},
		{`b'ab'`, "must hold exactly one byte"},
		{`'a`, "unterminated character literal"},
		{`b'a`, "unterminated byte literal"},
		{`'`, "unterminated character literal"},
		{"'a\nb'", "unterminated character literal"},
		{"'\n'", "newline inside character literal"},
		{`b'é'`, "byte literal must be ASCII"},
		{`b'\u{41}'`, `a byte literal takes \xNN`},
		{`'\u{D800}'`, "surrogate"},
		{`'\u{110000}'`, "above the maximum Unicode scalar"},
		{`'\u{}'`, "needs at least one hex digit"},
		{`'\u{1234567}'`, "at most 6 hex digits"},
		{`'\u41'`, `\u escape needs braces`},
		{`'\u{41'`, `unterminated \u{...} escape`},
		{`'\xFF'`, "above ASCII in a character literal"},
		{`'\x'`, `\x escape needs two hex digits`},
		{`b'\xZZ'`, `\x escape needs two hex digits`},
		{`'\q'`, `unknown escape \q`},
		{`b'\q'`, `unknown escape \q`},
	}
	for _, c := range cases {
		_, _, err := Tokenize(c.src)
		if err == nil {
			t.Errorf("Tokenize(%s): accepted, want an error containing %q", c.src, c.want)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Tokenize(%s): error %q, want it to contain %q", c.src, err.Error(), c.want)
		}
	}
}

func TestBytePrefixOnlyBindsToQuote(t *testing.T) {
	toks, _, err := Tokenize("b b1 ab bytes")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"b", "b1", "ab", "bytes"} {
		if toks[i].Kind != Ident || toks[i].Text != want {
			t.Errorf("tok[%d] = %v, want Ident %q", i, toks[i], want)
		}
	}
}

func TestCharLiteralPositions(t *testing.T) {
	toks, _, err := Tokenize("'😀' x\n'a' y")
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Pos.Line != 1 || toks[0].Pos.Col != 1 {
		t.Errorf("literal at %v, want 1:1", toks[0].Pos)
	}
	if toks[1].Kind != Ident || toks[1].Pos.Line != 1 {
		t.Errorf("tok[1] = %v, want an Ident on line 1", toks[1])
	}
	if toks[2].Pos.Line != 2 || toks[2].Pos.Col != 1 {
		t.Errorf("second literal at %v, want 2:1", toks[2].Pos)
	}
}
