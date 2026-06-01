package lexer

import "testing"

// tokSpec is a (Kind, Text, Suffix) triple for asserting a token
// sequence; Suffix is only meaningful for Number / Float tokens.
type tokSpec struct {
	k      Kind
	s      string
	suffix string
}

// assertTokens tokenizes src and checks the leading tokens (plus a
// trailing EOF) match want exactly.
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

// A trailing dot with no following digit is NOT a fractional float —
// `1.` lexes as `Number "1"` then a `.` Punct (the float upgrade
// requires a digit after the dot). It is not a lex error.
func TestLexNumericTrailingDotNotFloat(t *testing.T) {
	assertTokens(t, "1.", []tokSpec{
		{Number, "1", ""},
		{Punct, ".", ""},
	})
}

// A leading dot with no integer part is NOT a float — `.5` lexes as a
// `.` Punct then `Number "5"`; the number scanner only starts on a
// digit, so `.5` never becomes a `0.5`-style float literal.
func TestLexNumericLeadingDotNotFloat(t *testing.T) {
	assertTokens(t, ".5", []tokSpec{
		{Punct, ".", ""},
		{Number, "5", ""},
	})
}

// Deeper chained numeric field access (`a.0.1`) must lex as
// Ident Dot Number Dot Number — the afterDot guard suppresses the
// `.<digit>` float upgrade for every selector past the first, not
// just one level (companion to TestChainedTupleNumericAccess which
// covers the `t.1.0` shape).
func TestLexNumericDeepTupleAccess(t *testing.T) {
	assertTokens(t, "a.0.1", []tokSpec{
		{Ident, "a", ""},
		{Punct, ".", ""},
		{Number, "0", ""},
		{Punct, ".", ""},
		{Number, "1", ""},
	})
}

// The exponent upgrade is suppressed after a `.` just like the
// fractional-dot upgrade: in `t.1e3` the `1` stays a selector Number
// and the `e3` lexes as a separate Ident rather than `1e3` being
// eaten as a single Float. Guards the `!l.afterDot` condition on the
// scientific-notation branch.
func TestLexNumericExponentSuppressedAfterDot(t *testing.T) {
	assertTokens(t, "t.1e3", []tokSpec{
		{Ident, "t", ""},
		{Punct, ".", ""},
		{Number, "1", ""},
		{Ident, "e3", ""},
	})
}

// A dangling `e` after a fractional literal (`5.0e`) is not an
// exponent — the Float stops at `5.0` and the `e` lexes as its own
// Ident (the float counterpart to TestFloatBareEIsNotExponent, which
// only covers an integer base `1e`).
func TestLexNumericBareEAfterFractionNotExponent(t *testing.T) {
	assertTokens(t, "5.0e", []tokSpec{
		{Float, "5.0", ""},
		{Ident, "e", ""},
	})
}

// Hex literals round-trip their full text (prefix included) for
// all-letter digit runs and the capital `0X` prefix. Existing
// TestHexLiteral covers `0x2A`/`0xff`/`0X0`/`0xDEADBEEF`; these pin
// the all-lowercase-letter and `0X<letters>` cases.
func TestLexNumericHexRoundTrip(t *testing.T) {
	cases := []string{"0xabcdef", "0xABCDEF", "0Xff", "0Xa1B2", "0x0"}
	for _, src := range cases {
		assertTokens(t, src, []tokSpec{{Number, src, ""}})
	}
}

// Hex literals accept integer typed suffixes (i*/u*). The suffix is
// split off the digit run and recorded in Token.Suffix; the Text
// keeps only `0x<digits>`.
func TestLexNumericHexWithIntSuffix(t *testing.T) {
	cases := []struct {
		src    string
		text   string
		suffix string
	}{
		{"0x10i64", "0x10", "i64"},
		{"0xffu8", "0xff", "u8"},
		{"0x1u16", "0x1", "u16"},
		{"0x20i8", "0x20", "i8"},
	}
	for _, c := range cases {
		assertTokens(t, c.src, []tokSpec{{Number, c.text, c.suffix}})
	}
}

// Edge case: a hex literal whose trailing letters happen to spell a
// suffix-looking run is still consumed greedily as hex digits — `f`
// is a valid hex digit, so `0xfff32` is the single Number `0xfff32`
// with NO suffix (the suffix scanner never sees a leading `i`/`u`/`f`
// boundary because the hex-digit loop already ate everything).
func TestLexNumericHexGreedyNoSuffixSplit(t *testing.T) {
	assertTokens(t, "0xfff32", []tokSpec{{Number, "0xfff32", ""}})
}

// Hex literals do not take a fractional part: `0x1.5` stops the hex
// run at `0x1`, then `.` Punct, then `Number "5"` — the float
// fractional-dot upgrade is gated on `!isHex`.
func TestLexNumericHexNoFraction(t *testing.T) {
	assertTokens(t, "0x1.5", []tokSpec{
		{Number, "0x1", ""},
		{Punct, ".", ""},
		{Number, "5", ""},
	})
}

// A `0xg` with a non-hex first character consumes zero hex digits and
// is therefore the same "needs at least one digit" error as a bare
// `0x` (companion to TestHexLiteralNeedsDigits).
func TestLexNumericHexInvalidDigitErrors(t *testing.T) {
	if _, _, err := Tokenize("0xg"); err == nil {
		t.Fatal("expected error for 0xg (no hex digit), got nil")
	}
}

// A float suffix is valid on a scientific-notation base: `1.5e3f64`
// lexes as a single Float with text `1.5e3` and suffix `f64`.
func TestLexNumericScientificWithFloatSuffix(t *testing.T) {
	assertTokens(t, "1.5e3f64", []tokSpec{{Float, "1.5e3", "f64"}})
}

// An integer suffix on an exponent-promoted float is rejected: the
// exponent makes `1e3` a Float, so `1e3i32` is "integer suffix on
// float literal" — the same rule TestNumericLiteralRejectsBadSuffix
// checks for the fractional `1.5i32`, but via the exponent path.
func TestLexNumericIntSuffixOnExponentFloatRejected(t *testing.T) {
	if _, _, err := Tokenize("1e3i32"); err == nil {
		t.Fatal("expected error for 1e3i32 (int suffix on exponent float), got nil")
	}
}

// An integer literal text with an `f*` suffix is promoted to a Float
// token: `0f32` is Float with text `0` and suffix `f32` (boundary at
// zero; TestNumericLiteralSuffixes covers `42f32`).
func TestLexNumericZeroFloatSuffix(t *testing.T) {
	assertTokens(t, "0f32", []tokSpec{{Float, "0", "f32"}})
}

// The remaining int-suffix widths not already exercised by
// TestNumericLiteralSuffixes (i8/i16/u16/u64), to pin the full
// validNumericSuffix accept set on the integer path.
func TestLexNumericRemainingIntSuffixWidths(t *testing.T) {
	cases := []struct {
		src    string
		text   string
		suffix string
	}{
		{"7i8", "7", "i8"},
		{"42i16", "42", "i16"},
		{"9u16", "9", "u16"},
		{"100u64", "100", "u64"},
	}
	for _, c := range cases {
		assertTokens(t, c.src, []tokSpec{{Number, c.text, c.suffix}})
	}
}

// Underscore digit separators and a binary `0b` prefix are NOT
// supported by the lexer — pinning current behavior so a future
// add is a deliberate, test-visible change. `1_000` lexes as
// `Number "1"` then `Ident "_000"`; `0b101` as `Number "0"` then
// `Ident "b101"`.
func TestLexNumericNoUnderscoreOrBinary(t *testing.T) {
	assertTokens(t, "1_000", []tokSpec{
		{Number, "1", ""},
		{Ident, "_000", ""},
	})
	assertTokens(t, "0b101", []tokSpec{
		{Number, "0", ""},
		{Ident, "b101", ""},
	})
}
