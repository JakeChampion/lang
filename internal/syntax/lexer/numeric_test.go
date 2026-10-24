package lexer

import (
	"strings"
	"testing"
)

type tokSpec struct {
	k      Kind
	s      string
	suffix string
}

func assertTokens(t *testing.T, src string, want []tokSpec) {
	t.Helper()
	toks, _, err := Tokenize(src)
	if err != nil {
		t.Fatalf("%q: tokenize error: %v", src, err)
	}
	want = append(want, tokSpec{EOF, "", ""})
	if len(toks) != len(want) {
		t.Fatalf("%q: got %d tokens, want %d: %v", src, len(toks), len(want), toks)
	}
	for i, w := range want {
		if toks[i].Kind != w.k || toks[i].Text != w.s || toks[i].Suffix != w.suffix {
			t.Errorf("%q: tok[%d] = (%v %q suffix=%q), want (%v %q suffix=%q)",
				src, i, toks[i].Kind, toks[i].Text, toks[i].Suffix, w.k, w.s, w.suffix)
		}
	}
}

func TestLexNumericTrailingDotNotFloat(t *testing.T) {
	assertTokens(t, "1.", []tokSpec{
		{Number, "1", ""},
		{Punct, ".", ""},
	})
}

func TestLexNumericLeadingDotNotFloat(t *testing.T) {
	assertTokens(t, ".5", []tokSpec{
		{Punct, ".", ""},
		{Number, "5", ""},
	})
}

func TestLexNumericDeepTupleAccess(t *testing.T) {
	assertTokens(t, "a.0.1", []tokSpec{
		{Ident, "a", ""},
		{Punct, ".", ""},
		{Number, "0", ""},
		{Punct, ".", ""},
		{Number, "1", ""},
	})
}

func TestLexNumericExponentSuppressedAfterDot(t *testing.T) {
	assertTokens(t, "t.1e3", []tokSpec{
		{Ident, "t", ""},
		{Punct, ".", ""},
		{Number, "1", ""},
		{Ident, "e3", ""},
	})
}

func TestLexNumericBareEAfterFractionNotExponent(t *testing.T) {
	assertTokens(t, "5.0e", []tokSpec{
		{Float, "5.0", ""},
		{Ident, "e", ""},
	})
}

func TestLexNumericHexRoundTrip(t *testing.T) {
	cases := []string{"0xabcdef", "0xABCDEF", "0Xff", "0Xa1B2", "0x0"}
	for _, src := range cases {
		assertTokens(t, src, []tokSpec{{Number, src, ""}})
	}
}

func TestLexNumericHexWithIntSuffix(t *testing.T) {
	cases := []struct {
		src    string
		text   string
		suffix string
	}{
		{"0x10i64", "0x10", "i64"},
		{"0xffu8", "0xff", "u8"},
	}
	for _, c := range cases {
		assertTokens(t, c.src, []tokSpec{{Number, c.text, c.suffix}})
	}
}

func TestLexNumericHexGreedyNoSuffixSplit(t *testing.T) {
	assertTokens(t, "0xfff32", []tokSpec{{Number, "0xfff32", ""}})
}

func TestLexNumericHexNoFraction(t *testing.T) {
	assertTokens(t, "0x1.5", []tokSpec{
		{Number, "0x1", ""},
		{Punct, ".", ""},
		{Number, "5", ""},
	})
}

func TestLexNumericHexInvalidDigitErrors(t *testing.T) {
	if _, _, err := Tokenize("0xg"); err == nil {
		t.Fatal("expected error for 0xg (no hex digit), got nil")
	}
}

func TestLexNumericScientificWithFloatSuffix(t *testing.T) {
	assertTokens(t, "1.5e3f64", []tokSpec{{Float, "1.5e3", "f64"}})
}

func TestLexNumericIntSuffixOnExponentFloatRejected(t *testing.T) {
	if _, _, err := Tokenize("1e3i32"); err == nil {
		t.Fatal("expected error for 1e3i32 (int suffix on exponent float), got nil")
	}
}

func TestLexNumericZeroFloatSuffix(t *testing.T) {
	assertTokens(t, "0f32", []tokSpec{{Float, "0", "f32"}})
}

func TestLexNumericRemainingIntSuffixWidths(t *testing.T) {
	assertTokens(t, "100u64", []tokSpec{{Number, "100", "u64"}})
	assertTokens(t, "5usize", []tokSpec{{Number, "5", "usize"}})
	assertTokens(t, "0xffusize", []tokSpec{{Number, "0xff", "usize"}})
}

func TestLexNumericDigitSeparators(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want tokSpec
	}{
		{"1_000_000", tokSpec{Number, "1_000_000", ""}},
		{"1_000i64", tokSpec{Number, "1_000", "i64"}},
		{"0xff_ff", tokSpec{Number, "0xff_ff", ""}},
		{"0o7_7", tokSpec{Number, "0o7_7", ""}},
		{"0b1010_0101u8", tokSpec{Number, "0b1010_0101", "u8"}},
		{"1_000.000_1", tokSpec{Float, "1_000.000_1", ""}},
		{"1e1_0", tokSpec{Float, "1e1_0", ""}},
	} {
		assertTokens(t, tc.src, []tokSpec{tc.want})
	}
}

func TestLexNumericMisplacedSeparator(t *testing.T) {
	for _, src := range []string{"1_", "1__0", "1_.5", "1.5_", "1e5_", "0xff_", "0b1_", "1_i32"} {
		_, _, err := Tokenize(src)
		if err == nil || !strings.Contains(err.Error(), "a digit separator '_' must sit between two digits") {
			t.Errorf("%s: err = %v; want the misplaced-separator error", src, err)
		}
	}
	if _, _, err := Tokenize("0x_1"); err == nil || !strings.Contains(err.Error(), "needs at least one digit after 0x") {
		t.Errorf("0x_1: err = %v", err)
	}
	if _, _, err := Tokenize("0b1_2"); err == nil || !strings.Contains(err.Error(), `invalid digit '2' in a 0b literal`) {
		t.Errorf("0b1_2: err = %v", err)
	}
}

func TestLexTupleSelectorTakesNoSeparator(t *testing.T) {
	assertTokens(t, "t.1_0", []tokSpec{
		{Ident, "t", ""},
		{Punct, ".", ""},
		{Number, "1", ""},
		{Ident, "_0", ""},
	})
}
