package lexer

import "testing"

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
	// `~` is not a recognised punctuator (`@` now lexes — it leads the
	// `@derive(...)` attribute).
	_, _, err := Tokenize("~")
	if err == nil {
		t.Fatal("expected error on '~'")
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

// A string literal containing multi-byte UTF-8 characters must be
// preserved byte-for-byte. `b.WriteRune(c)` on a single source byte
// (`rune(l.src[l.i])`) re-encodes each byte of a multi-byte character
// as its own code point
// — turning `∃` (0xE2 0x88 0x83) into three mojibake runes. Strings
// are UTF-8 byte arrays here, so the token text must equal the
// original source bytes. Surfaced as a format → parse → format
// non-idempotency on a real example file.
// \xNN hex byte escapes (string + f-string literal portions), and the
// two-hex-digit requirement.
func TestStringLiteralHexEscape(t *testing.T) {
	toks, _, err := Tokenize(`"\x48\x69\x21"`)
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Text != "Hi!" {
		t.Errorf("string \\x: got %q, want %q", toks[0].Text, "Hi!")
	}
	// f-string literal portion.
	fts, _, err := Tokenize(`f"\x41={x}"`)
	if err != nil {
		t.Fatal(err)
	}
	if fts[0].Kind != FString || len(fts[0].FParts) == 0 || fts[0].FParts[0].Lit != "A=" {
		t.Errorf("f-string \\x: got %v, want first FPart Lit %q", fts[0].FParts, "A=")
	}
	// Needs two hex digits.
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

// The f-string literal scanner had the same byte-vs-rune bug as the
// plain-string scanner (a second `lit.WriteRune(c)` on a single
// source byte), so multi-byte UTF-8 in a literal *segment* of an
// f-string — e.g. the `café ` before `{x}` — was corrupted too.
// The literal pieces must be preserved byte-for-byte.
func TestFStringLiteralUTF8(t *testing.T) {
	cases := []struct {
		src  string
		want []FStringPart
	}{
		{src: `f"café {x}"`, want: []FStringPart{{Lit: "café "}, {Expr: "x"}}},
		{src: `f"{x} 日本語"`, want: []FStringPart{{Expr: "x"}, {Lit: " 日本語"}}},
		{src: `f"∃ {n} items"`, want: []FStringPart{{Lit: "∃ "}, {Expr: "n"}, {Lit: " items"}}},
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

func TestStringLiteralUnknownEscape(t *testing.T) {
	if _, _, err := Tokenize(`"a\zb"`); err == nil {
		t.Error("expected error on unknown escape")
	}
}

// f-strings produce a single FString token whose FParts hold
// alternating literal / interpolant pieces. The parser sub-parses
// each interpolant Expr text into an ast.Expr; the IR (via the
// checker's desugar) lowers the AST node to a `+`-chain. Cases
// covered: empty, literal-only, single / multi / leading /
// trailing interpolation, `{{` and `}}` brace escapes, escape
// sequences in literal segments, an interpolant containing a
// string literal (so the boundary scanner has to skip past the
// inner quotes).
func TestFStringParts(t *testing.T) {
	cases := []struct {
		src  string
		want []FStringPart
	}{
		{src: `f""`, want: nil},
		{src: `f"plain"`, want: []FStringPart{{Lit: "plain"}}},
		{src: `f"{x}"`, want: []FStringPart{{Expr: "x"}}},
		{src: `f"a{x}b"`, want: []FStringPart{{Lit: "a"}, {Expr: "x"}, {Lit: "b"}}},
		{src: `f"{a}{b}"`, want: []FStringPart{{Expr: "a"}, {Expr: "b"}}},
		{src: `f"{{lit}}"`, want: []FStringPart{{Lit: "{lit}"}}},
		{src: `f"hi\nthere"`, want: []FStringPart{{Lit: "hi\nthere"}}},
		{src: `f"k={"x"}"`, want: []FStringPart{{Lit: "k="}, {Expr: `"x"`}}},
		{src: `f"sum {a + b}"`, want: []FStringPart{{Lit: "sum "}, {Expr: "a + b"}}},
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

// f-string error cases: unterminated, newline inside, bare `}`,
// empty `{}` interpolant.
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

// Scientific-notation float literals: `[eE][+-]?[0-9]+` on either an
// integer base (`1e3`) or a fractional one (`1.5e-2`). The whole
// thing must come back as a single Float token whose text is
// ParseFloat-ready.
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

// Hex integer literals: `0x` / `0X` followed by hex digits come back
// as a single Number token whose text includes the prefix. Mixed
// case digits and an optional integer suffix are accepted.
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

// A bare `0x` with no hex digits is an error, not a silent `0`
// followed by an `x` identifier.
func TestHexLiteralNeedsDigits(t *testing.T) {
	if _, _, err := Tokenize("0x"); err == nil {
		t.Fatal("expected error for bare 0x literal, got nil")
	}
}

// A dangling `e` with no exponent digits is NOT an exponent — the
// number stops before it and the `e...` lexes as a separate
// identifier. Guards against eating the `e` of `1einvalid`.
func TestFloatBareEIsNotExponent(t *testing.T) {
	toks, _, err := Tokenize("1e foo")
	if err != nil {
		t.Fatal(err)
	}
	// `1` (Number) `e` (Ident) `foo` (Ident) EOF
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

// Chained numeric field access (`t.1.0`) must lex as three numbers
// joined by two dots — not as `t . 1.0` with `1.0` eaten as a float
// literal. The lexer tracks the previous token kind and suppresses
// the `.<digit>` → float upgrade when the prior token was `.`.
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

// Regression-pin that a `.<digit>` AFTER a non-dot context still
// upgrades to a float literal. Covers `var f = 1.5;` and the
// post-paren `(1.5)` case so the afterDot suppression doesn't
// over-fire.
func TestFloatLiteralStillWorksAfterNonDot(t *testing.T) {
	toks, _, err := Tokenize("var f = 1.5;")
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

// `float` is the width-unqualified alias for f64 (#5363), handled
// contextually in the parser's type position (like `str`). It must
// stay an Ident at the lexer level — NOT a reserved keyword — so
// the std/float module qualifier (`float.pi()`) and `float` locals
// keep working.
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
		// First token is the literal; second should be EOF.
		if toks[0].Kind != c.kind || toks[0].Text != c.text || toks[0].Suffix != c.suffix {
			t.Errorf("%s: got %v Text=%q Suffix=%q, want %v Text=%q Suffix=%q",
				c.src, toks[0].Kind, toks[0].Text, toks[0].Suffix, c.kind, c.text, c.suffix)
		}
	}
}

func TestNumericLiteralRejectsBadSuffix(t *testing.T) {
	for _, src := range []string{
		"42i33",
		"42i", // truncated suffix
		"1.5i32",
	} {
		if _, _, err := Tokenize(src); err == nil {
			t.Errorf("%s: expected lex error", src)
		}
	}
}
