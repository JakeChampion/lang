package lexer

import (
	"errors"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestKeywordsAndIdentifiers(t *testing.T) {
	toks, _, err := Tokenize("function foo if returnFoo")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		k Kind
		s string
	}{
		{Keyword, "function"},
		{Ident, "foo"},
		{Keyword, "if"},
		{Ident, "returnFoo"},
		{EOF, ""},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(toks), len(want))
	}
	for i, w := range want {
		if toks[i].Kind != w.k || toks[i].Text != w.s {
			t.Errorf("tok[%d] = %v, want %v %q", i, toks[i], w.k, w.s)
		}
	}
}

func TestPunctuators(t *testing.T) {
	toks, _, err := Tokenize("== != <= >= && || + -")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"==", "!=", "<=", ">=", "&&", "||", "+", "-"} {
		if toks[i].Text != want {
			t.Errorf("tok[%d] = %q, want %q", i, toks[i].Text, want)
		}
	}
}

func TestComments(t *testing.T) {
	toks, _, err := Tokenize("// comment\n42\n// trailing")
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Kind != Number || toks[0].Text != "42" {
		t.Fatalf("expected number-token 42, got %v", toks[0])
	}
	if toks[1].Kind != EOF {
		t.Fatalf("expected EOF, got %v", toks[1])
	}
}

func TestPositions(t *testing.T) {
	toks, _, err := Tokenize("x\n  y")
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Pos.Line != 1 || toks[0].Pos.Col != 1 {
		t.Errorf("x at %v, want 1:1", toks[0].Pos)
	}
	if toks[1].Pos.Line != 2 || toks[1].Pos.Col != 3 {
		t.Errorf("y at %v, want 2:3", toks[1].Pos)
	}
}

func TestUnknownChar(t *testing.T) {
	_, _, err := Tokenize("~")
	if err == nil {
		t.Fatal("expected error on '~'")
	}
}

func TestLexErrorCodes(t *testing.T) {
	for _, c := range []struct{ src, msg, code string }{
		{"#", "unexpected character '#'", "P001"},
		{`\`, `unexpected character '\\'`, "P001"},
		{"\x01", `unexpected character '\x01'`, "P001"},
		{"a \xff", "invalid UTF-8 byte 0xFF", "P001"},
		{`"oops`, "unterminated string literal", "P001"},
		{`"a\qb"`, `unknown escape \q in string literal`, "P001"},
		{`f"a\qb"`, `unknown escape \q in f-string literal`, "P001"},
		{`'\xFF'`, `\xFF is above ASCII in a character literal; write \u{FF} for the scalar`, "P001"},
		{`"\xff"`, `\xFF is above ASCII in a string literal; write \u{FF} for the scalar`, "P001"},
		{`f"\x80{x}"`, `\x80 is above ASCII in an f-string literal; write \u{80} for the scalar`, "P001"},
		{`"\u{D800}"`, `\u{D800} is a surrogate, not a Unicode scalar value`, "P001"},
		{`"\u{110000}"`, `\u{110000} is above the maximum Unicode scalar 10FFFF`, "P001"},
		{`f"\u{1F600"`, `unterminated \u{...} escape`, "P001"},
		{"42i33", `unknown numeric suffix "i33" on "42"`, "P002"},
		{"1.5i32", `integer suffix "i32" on float literal "1.5"`, "P002"},
		{"0x", "hex literal needs at least one digit after 0x", "P002"},
		{"0o19", "invalid digit '9' in a 0o literal", "P002"},
	} {
		_, _, err := Tokenize(c.src)
		var le *Error
		if !errors.As(err, &le) {
			t.Errorf("%q: got %v, want a *lexer.Error", c.src, err)
			continue
		}
		if le.Msg != c.msg || le.Code() != c.code {
			t.Errorf("%q: got %s %q, want %s %q", c.src, le.Code(), le.Msg, c.code, c.msg)
		}
	}
}

func TestAtSignLexes(t *testing.T) {
	toks, _, err := Tokenize("@derive")
	if err != nil {
		t.Fatalf("`@` should lex as a punctuator: %v", err)
	}
	if toks[0].Kind != Punct || toks[0].Text != "@" {
		t.Errorf("first token = %v, want Punct(@)", toks[0])
	}
}

func TestStringLiteralBasic(t *testing.T) {
	toks, _, err := Tokenize(`"hello"`)
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Kind != String || toks[0].Text != "hello" {
		t.Errorf("got %v, want String %q", toks[0], "hello")
	}
}

func TestStringLiteralEscapes(t *testing.T) {
	toks, _, err := Tokenize(`"a\t\nb\"c\\d"`)
	if err != nil {
		t.Fatal(err)
	}
	want := "a\t\nb\"c\\d"
	if toks[0].Text != want {
		t.Errorf("got %q, want %q", toks[0].Text, want)
	}
}

func TestStringLiteralHexEscape(t *testing.T) {
	toks, _, err := Tokenize(`"\x48\x69\x21"`)
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Text != "Hi!" {
		t.Errorf("string \\x: got %q, want %q", toks[0].Text, "Hi!")
	}
	fts, _, err := Tokenize(`f"\x41={x}"`)
	if err != nil {
		t.Fatal(err)
	}
	if fts[0].Kind != FString || len(fts[0].FParts) == 0 || fts[0].FParts[0].Lit != "A=" {
		t.Errorf("f-string \\x: got %v, want first FPart Lit %q", fts[0].FParts, "A=")
	}
	if _, _, err := Tokenize(`"\xZ1"`); err == nil {
		t.Error("expected error on \\x with non-hex digit")
	}
	if _, _, err := Tokenize(`"\x4"`); err == nil {
		t.Error("expected error on \\x with one hex digit")
	}
}

func TestStringLiteralUTF8(t *testing.T) {
	for _, s := range []string{"∃ over empty", "héllo wörld", "日本語", "emoji 🎉 ok"} {
		toks, _, err := Tokenize(`"` + s + `"`)
		if err != nil {
			t.Fatalf("tokenize %q: %v", s, err)
		}
		if toks[0].Kind != String || toks[0].Text != s {
			t.Errorf("got %q (% x), want %q (% x)", toks[0].Text, toks[0].Text, s, s)
		}
	}
}

func TestFStringLiteralUTF8(t *testing.T) {
	cases := []struct {
		src  string
		want []FStringPart
	}{
		{src: `f"café {x}"`, want: []FStringPart{{Lit: "café "}, {Expr: "x", Pos: ast.Position{Line: 1, Col: 10}}}},
		{src: `f"{x} 日本語"`, want: []FStringPart{{Expr: "x", Pos: ast.Position{Line: 1, Col: 4}}, {Lit: " 日本語"}}},
		{src: `f"∃ {n} items"`, want: []FStringPart{{Lit: "∃ "}, {Expr: "n", Pos: ast.Position{Line: 1, Col: 8}}, {Lit: " items"}}},
		{src: `f"héllo"`, want: []FStringPart{{Lit: "héllo"}}},
	}
	for _, c := range cases {
		toks, _, err := Tokenize(c.src)
		if err != nil {
			t.Errorf("%s: lex error: %v", c.src, err)
			continue
		}
		got := toks[0].FParts
		if len(got) != len(c.want) {
			t.Errorf("%s: got %d FParts, want %d:\ngot: %v", c.src, len(got), len(c.want), got)
			continue
		}
		for i, w := range c.want {
			if got[i] != w {
				t.Errorf("%s: FParts[%d] = %q, want %q", c.src, i, got[i], w)
			}
		}
	}
}

func TestStringLiteralUnterminated(t *testing.T) {
	if _, _, err := Tokenize(`"oops`); err == nil {
		t.Error("expected error on unterminated string")
	}
}

func TestStringEscapes(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`"a\x41\x7f"`, "aA\x7f"},
		{`"\0\'\"\\"`, "\x00'\"\\"},
		{`"\u{41}\u{e9}\u{20AC}\u{1F600}\u{10FFFF}"`, "Aé€😀\U0010FFFF"},
		{`"\u{D7FF}\u{E000}"`, "\uD7FF\uE000"},
	} {
		toks, _, err := Tokenize(c.src)
		if err != nil {
			t.Errorf("Tokenize(%s): %v", c.src, err)
			continue
		}
		if toks[0].Kind != String || toks[0].Text != c.want {
			t.Errorf("Tokenize(%s) = %v %q, want String %q", c.src, toks[0].Kind, toks[0].Text, c.want)
		}
	}
	toks, _, err := Tokenize(`f"\u{1F600}{x}\u{7B}"`)
	if err != nil {
		t.Fatal(err)
	}
	parts := toks[0].FParts
	if len(parts) != 3 || parts[0].Lit != "😀" || parts[1].Expr != "x" || parts[2].Lit != "{" {
		t.Errorf("f-string \\u{...} parts = %+v", parts)
	}
}

func TestStringLiteralUnknownEscape(t *testing.T) {
	if _, _, err := Tokenize(`"a\zb"`); err == nil {
		t.Error("expected error on unknown escape")
	}
}

func TestFStringParts(t *testing.T) {
	cases := []struct {
		src  string
		want []FStringPart
	}{
		{src: `f""`, want: nil},
		{src: `f"plain"`, want: []FStringPart{{Lit: "plain"}}},
		{src: `f"{x}"`, want: []FStringPart{{Expr: "x", Pos: ast.Position{Line: 1, Col: 4}}}},
		{src: `f"a{x}b"`, want: []FStringPart{{Lit: "a"}, {Expr: "x", Pos: ast.Position{Line: 1, Col: 5}}, {Lit: "b"}}},
		{src: `f"{a}{b}"`, want: []FStringPart{{Expr: "a", Pos: ast.Position{Line: 1, Col: 4}}, {Expr: "b", Pos: ast.Position{Line: 1, Col: 7}}}},
		{src: `f"{{lit}}"`, want: []FStringPart{{Lit: "{lit}"}}},
		{src: `f"hi\nthere"`, want: []FStringPart{{Lit: "hi\nthere"}}},
		{src: `f"k={"x"}"`, want: []FStringPart{{Lit: "k="}, {Expr: `"x"`, Pos: ast.Position{Line: 1, Col: 6}}}},
		{src: `f"sum {a + b}"`, want: []FStringPart{{Lit: "sum "}, {Expr: "a + b", Pos: ast.Position{Line: 1, Col: 8}}}},
		{src: "\n\nf\"{x}\"", want: []FStringPart{{Expr: "x", Pos: ast.Position{Line: 3, Col: 4}}}},
	}
	for _, c := range cases {
		toks, _, err := Tokenize(c.src)
		if err != nil {
			t.Errorf("%s: lex error: %v", c.src, err)
			continue
		}
		if len(toks) != 2 {
			t.Errorf("%s: got %d tokens, want 2 (FString + EOF):\ngot: %v", c.src, len(toks), toks)
			continue
		}
		if toks[0].Kind != FString {
			t.Errorf("%s: got first token kind=%v, want FString", c.src, toks[0].Kind)
			continue
		}
		got := toks[0].FParts
		if len(got) != len(c.want) {
			t.Errorf("%s: got %d FParts, want %d:\ngot: %v", c.src, len(got), len(c.want), got)
			continue
		}
		for i, w := range c.want {
			if got[i] != w {
				t.Errorf("%s: FParts[%d] = %v, want %v", c.src, i, got[i], w)
			}
		}
	}
}

func TestFStringErrors(t *testing.T) {
	for _, src := range []string{
		`f"unterminated`,
		"f\"with\nnewline\"",
		`f"bare}brace"`,
		`f"empty{}"`,
	} {
		if _, _, err := Tokenize(src); err == nil {
			t.Errorf("expected error for %q, got none", src)
		}
	}
}

func TestFloatLiteral(t *testing.T) {
	toks, _, err := Tokenize("1.5 0.25 12.0")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		k Kind
		s string
	}{
		{Float, "1.5"},
		{Float, "0.25"},
		{Float, "12.0"},
		{EOF, ""},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(toks), len(want))
	}
	for i, w := range want {
		if toks[i].Kind != w.k || toks[i].Text != w.s {
			t.Errorf("tok[%d] = %v, want %v %q", i, toks[i], w.k, w.s)
		}
	}
}

func TestFloatScientificNotation(t *testing.T) {
	toks, _, err := Tokenize("1e3 1.5e10 1.5E3 2.0e-2 6.022e+23")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		k Kind
		s string
	}{
		{Float, "1e3"},
		{Float, "1.5e10"},
		{Float, "1.5E3"},
		{Float, "2.0e-2"},
		{Float, "6.022e+23"},
		{EOF, ""},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %v", len(toks), len(want), toks)
	}
	for i, w := range want {
		if toks[i].Kind != w.k || toks[i].Text != w.s {
			t.Errorf("tok[%d] = (%v %q), want (%v %q)", i, toks[i].Kind, toks[i].Text, w.k, w.s)
		}
	}
}

func TestHexLiteral(t *testing.T) {
	toks, _, err := Tokenize("0x2A 0xff 0X0 0xDEADBEEF 0x10u8")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		k      Kind
		s      string
		suffix string
	}{
		{Number, "0x2A", ""},
		{Number, "0xff", ""},
		{Number, "0X0", ""},
		{Number, "0xDEADBEEF", ""},
		{Number, "0x10", "u8"},
		{EOF, "", ""},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %v", len(toks), len(want), toks)
	}
	for i, w := range want {
		if toks[i].Kind != w.k || toks[i].Text != w.s || toks[i].Suffix != w.suffix {
			t.Errorf("tok[%d] = (%v %q suffix=%q), want (%v %q suffix=%q)",
				i, toks[i].Kind, toks[i].Text, toks[i].Suffix, w.k, w.s, w.suffix)
		}
	}
}

func TestHexLiteralNeedsDigits(t *testing.T) {
	if _, _, err := Tokenize("0x"); err == nil {
		t.Fatal("expected error for bare 0x literal, got nil")
	}
}

func TestRadixLiteral(t *testing.T) {
	toks, _, err := Tokenize("0o755 0O17 0b1010 0B1 0o170000u32 0b11111111u8")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ text, suffix string }{
		{"0o755", ""}, {"0O17", ""}, {"0b1010", ""}, {"0B1", ""}, {"0o170000", "u32"}, {"0b11111111", "u8"},
	}
	if len(toks) != len(want)+1 {
		t.Fatalf("got %d tokens, want %d: %v", len(toks), len(want)+1, toks)
	}
	for i, w := range want {
		if toks[i].Kind != Number || toks[i].Text != w.text || toks[i].Suffix != w.suffix {
			t.Errorf("tok[%d] = (%v %q suffix=%q), want (Number %q suffix=%q)", i, toks[i].Kind, toks[i].Text, toks[i].Suffix, w.text, w.suffix)
		}
	}
}

func TestRadixLiteralRejectsBadDigits(t *testing.T) {
	for _, src := range []string{"0o", "0b", "0o78", "0b102", "0o9"} {
		if _, _, err := Tokenize(src); err == nil {
			t.Errorf("%s: expected a lex error, got nil", src)
		}
	}
}

func TestFloatBareEIsNotExponent(t *testing.T) {
	toks, _, err := Tokenize("1e foo")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		k Kind
		s string
	}{
		{Number, "1"},
		{Ident, "e"},
		{Ident, "foo"},
		{EOF, ""},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %v", len(toks), len(want), toks)
	}
	for i, w := range want {
		if toks[i].Kind != w.k || toks[i].Text != w.s {
			t.Errorf("tok[%d] = (%v %q), want (%v %q)", i, toks[i].Kind, toks[i].Text, w.k, w.s)
		}
	}
}

func TestChainedTupleNumericAccess(t *testing.T) {
	toks, _, err := Tokenize("t.1.0")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		k Kind
		s string
	}{
		{Ident, "t"},
		{Punct, "."},
		{Number, "1"},
		{Punct, "."},
		{Number, "0"},
		{EOF, ""},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %v", len(toks), len(want), toks)
	}
	for i, w := range want {
		if toks[i].Kind != w.k || toks[i].Text != w.s {
			t.Errorf("tok[%d] = (%v %q), want (%v %q)", i, toks[i].Kind, toks[i].Text, w.k, w.s)
		}
	}
}

func TestFloatLiteralStillWorksAfterNonDot(t *testing.T) {
	toks, _, err := Tokenize("let f = 1.5;")
	if err != nil {
		t.Fatal(err)
	}
	var floats []string
	for _, tok := range toks {
		if tok.Kind == Float {
			floats = append(floats, tok.Text)
		}
	}
	if len(floats) != 1 || floats[0] != "1.5" {
		t.Errorf("got floats %v, want exactly one [\"1.5\"]", floats)
	}
}

func TestFloatIsNoLongerAKeyword(t *testing.T) {
	toks, _, err := Tokenize("float")
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Kind != Ident || toks[0].Text != "float" {
		t.Errorf("got %v, want Ident %q", toks[0], "float")
	}
}

func TestCompoundAssignTokens(t *testing.T) {
	toks, _, err := Tokenize("+= -= *= /= %= &= |= ^= <<= >>=")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<=", ">>="}
	for i, w := range want {
		if toks[i].Kind != Punct || toks[i].Text != w {
			t.Errorf("tok[%d] = %v, want Punct %q", i, toks[i], w)
		}
	}
}

func TestQuestionMarkPunct(t *testing.T) {
	toks, _, err := Tokenize("?")
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Kind != Punct || toks[0].Text != "?" {
		t.Errorf("got %v, want Punct %q", toks[0], "?")
	}
}

func TestNumericLiteralSuffixes(t *testing.T) {
	cases := []struct {
		src    string
		kind   Kind
		text   string
		suffix string
	}{
		{"42", Number, "42", ""},
		{"42i64", Number, "42", "i64"},
		{"7u8", Number, "7", "u8"},
		{"0u32", Number, "0", "u32"},
		{"1.5", Float, "1.5", ""},
		{"1.5f64", Float, "1.5", "f64"},
		{"42f32", Float, "42", "f32"},
	}
	for _, c := range cases {
		toks, _, err := Tokenize(c.src)
		if err != nil {
			t.Errorf("%s: tokenize error: %v", c.src, err)
			continue
		}
		if toks[0].Kind != c.kind || toks[0].Text != c.text || toks[0].Suffix != c.suffix {
			t.Errorf("%s: got %v Text=%q Suffix=%q, want %v Text=%q Suffix=%q",
				c.src, toks[0].Kind, toks[0].Text, toks[0].Suffix, c.kind, c.text, c.suffix)
		}
	}
}

func TestNumericLiteralRejectsBadSuffix(t *testing.T) {
	for _, src := range []string{
		"42i33",
		"42i",
		"1.5i32",
	} {
		if _, _, err := Tokenize(src); err == nil {
			t.Errorf("%s: expected lex error", src)
		}
	}
}
