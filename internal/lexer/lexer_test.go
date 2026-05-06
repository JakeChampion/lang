package lexer

import "testing"

func TestKeywordsAndIdentifiers(t *testing.T) {
	toks, err := Tokenize("function foo if returnFoo")
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
	toks, err := Tokenize("== != <= >= && || + -")
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
	toks, err := Tokenize("// comment\n42\n// trailing")
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Kind != Number || toks[0].Text != "42" {
		t.Fatalf("expected number 42, got %v", toks[0])
	}
	if toks[1].Kind != EOF {
		t.Fatalf("expected EOF, got %v", toks[1])
	}
}

func TestPositions(t *testing.T) {
	toks, err := Tokenize("x\n  y")
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
	_, err := Tokenize("@")
	if err == nil {
		t.Fatal("expected error on '@'")
	}
}

func TestStringLiteralBasic(t *testing.T) {
	toks, err := Tokenize(`"hello"`)
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Kind != String || toks[0].Text != "hello" {
		t.Errorf("got %v, want String %q", toks[0], "hello")
	}
}

func TestStringLiteralEscapes(t *testing.T) {
	toks, err := Tokenize(`"a\t\nb\"c\\d"`)
	if err != nil {
		t.Fatal(err)
	}
	want := "a\t\nb\"c\\d"
	if toks[0].Text != want {
		t.Errorf("got %q, want %q", toks[0].Text, want)
	}
}

func TestStringLiteralUnterminated(t *testing.T) {
	if _, err := Tokenize(`"oops`); err == nil {
		t.Error("expected error on unterminated string")
	}
}

func TestStringLiteralUnknownEscape(t *testing.T) {
	if _, err := Tokenize(`"a\zb"`); err == nil {
		t.Error("expected error on unknown escape")
	}
}

func TestFloatLiteral(t *testing.T) {
	toks, err := Tokenize("1.5 0.25 12.0")
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
	toks, err := Tokenize("float")
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].Kind != Keyword || toks[0].Text != "float" {
		t.Errorf("got %v, want Keyword %q", toks[0], "float")
	}
}
