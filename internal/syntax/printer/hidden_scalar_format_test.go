package printer

import (
	"strings"
	"testing"
	"unicode"
)

// A string literal keeps an escape, or gets one, for each scalar that a reader
// cannot see or that changes the order of the text on the screen.
func TestFormatEscapesHiddenScalars(t *testing.T) {
	const want = `function main(): i32 {
  let marks = "\u{FEFF}\u{200B}\u{202E}a\u{00A0}bé😀\u{2028}\u{0085}\u{E0041}—";
  let f = f"x\u{200D}{marks}\u{3000}";
  return marks.len() + f.len();
}
`
	raw := strings.NewReplacer(
		`\u{FEFF}`, "\ufeff", `\u{200B}`, "\u200b", `\u{202E}`, "\u202e", `\u{00A0}`, "\u00a0",
		`\u{2028}`, "\u2028", `\u{0085}`, "\u0085", `\u{E0041}`, "\U000e0041", `\u{200D}`, "\u200d",
		`\u{3000}`, "\u3000",
	).Replace(want)
	if raw == want {
		t.Fatal("the raw source has no hidden scalar")
	}
	for name, src := range map[string]string{"escapes": want, "raw scalars": raw, "lower case": strings.ToLower(want)} {
		if got := formatSrc(t, src); got != want {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", name, got, want)
		}
	}
}

// The ranges are those of the Unicode tables of Go: the C1 controls and the
// categories Cf, Zl, Zp and Zs, with no U+0020.
func TestHiddenScalarsAreTheUnicodeCategories(t *testing.T) {
	for r := rune(0x80); r <= unicode.MaxRune; r++ {
		want := r <= 0x9f || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Zs)
		if got := isHiddenScalar(r); got != want {
			t.Errorf("U+%04X: hidden is %v, want %v", r, got, want)
		}
	}
}
