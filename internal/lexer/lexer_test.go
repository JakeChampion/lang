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
	_, _, err := Tokenize("@")
	if err == nil {
		t.Fatal("expected error on '@'")
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

func TestFloatKeyword(t *testing.T) {
	toks, _, err := Tokenize("float")
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Kind != Keyword || toks[0].Text != "float" {
		t.Errorf("got %v, want Keyword %q", toks[0], "float")
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
