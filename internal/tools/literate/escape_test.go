package literate

import (
	"strings"
	"testing"
)

func TestChunkRefRejectsEscaped(t *testing.T) {
	if _, _, ok := chunkRef(`\<<name>>`); ok {
		t.Error("an escaped \\<<name>> must not be treated as a reference")
	}
	if _, _, ok := chunkRef(`    \<<name>>`); ok {
		t.Error("an indented escaped reference must not be a reference")
	}
	if _, name, ok := chunkRef(`<<name>>`); !ok || name != "name" {
		t.Errorf("a plain <<name>> is still a reference (got ok=%v name=%q)", ok, name)
	}
}

func TestDeEscapeRef(t *testing.T) {
	cases := map[string]string{
		`\<<x>>`:     `<<x>>`,
		`    \<<x>>`: `    <<x>>`,
		`<<x>>`:      `<<x>>`,   // not escaped
		`foo()`:      `foo()`,   // unrelated line
		`a \<< b`:    `a \<< b`, // backslash not at line start → unchanged
	}
	for in, want := range cases {
		if got := deEscapeRef(in); got != want {
			t.Errorf("deEscapeRef(%q) = %q, want %q", in, got, want)
		}
	}
}

// An escaped reference tangles to a literal `<<name>>` and does NOT
// require the chunk to be defined (no undefined-reference error).
func TestTangleEscapedRefIsLiteral(t *testing.T) {
	src := "```fern\n<<*>>=\nbefore\n\\<<NOTACHUNK>>\nafter\n```\n"
	code, _, err := Parse(src).Tangle()
	if err != nil {
		t.Fatalf("tangle: %v (escaped ref should not need a definition)", err)
	}
	if !strings.Contains(code, "\n<<NOTACHUNK>>\n") {
		t.Errorf("expected a literal <<NOTACHUNK>> line, got:\n%s", code)
	}
	if strings.Contains(code, `\<<`) {
		t.Errorf("the escaping backslash should be stripped, got:\n%s", code)
	}
}

// The diagnostic-remap ColShift accounts for the backslash stripped from
// an escaped chunk marker, so a column at/after the marker maps back to
// the correct document column instead of being off by one. Regression
// for L1 in docs/ADVERSARIAL-REVIEW-2026-06.md.
func TestTangleEscapedRefColShift(t *testing.T) {
	// Root references <<body>> at 4-space indent; <<body>> contains an
	// escaped marker. Tangled middle line is "    <<lit>> = 5;".
	src := "```fern\n<<*>>=\nfn main() {\n    <<body>>\n}\n```\n" +
		"```fern\n<<body>>=\n\\<<lit>> = 5;\n```\n"
	code, lm, err := Parse(src).Tangle()
	if err != nil {
		t.Fatalf("tangle: %v", err)
	}
	// Find the generated line carrying the escaped marker.
	var gi = -1
	for i, line := range strings.Split(code, "\n") {
		if strings.Contains(line, "<<lit>> = 5;") {
			gi = i
			break
		}
	}
	if gi < 0 || gi >= len(lm) {
		t.Fatalf("could not locate generated <<lit>> line in:\n%s", code)
	}
	// indent is 4 spaces, one backslash stripped → ColShift 4-1 = 3.
	if lm[gi].ColShift != 3 {
		t.Errorf("ColShift = %d, want 3 (4 indent minus 1 stripped backslash)", lm[gi].ColShift)
	}
	if lm[gi].Lit != 9 {
		t.Errorf("Lit = %d, want 9 (the \\<<lit>> document line)", lm[gi].Lit)
	}
	// `lit` sits at generated column 7 ("    <<l"); the inverse remap
	// (genCol - ColShift) must land on document column 4 ("\<<l").
	if docCol := 7 - lm[gi].ColShift; docCol != 4 {
		t.Errorf("remapped column = %d, want 4 (the `l` of \\<<lit>>)", docCol)
	}
}

// An unclosed fern fence at EOF (with a trailing newline) must not
// absorb the trailing-newline split artifact as a phantom blank body
// line. Regression for L5 in docs/ADVERSARIAL-REVIEW-2026-06.md.
func TestTangleUnclosedFenceNoPhantomLine(t *testing.T) {
	src := "```fern\n<<*>>=\nfn main() {}\n" // no closing ```, trailing \n
	code, lm, err := Parse(src).Tangle()
	if err != nil {
		t.Fatalf("tangle: %v", err)
	}
	if code != "fn main() {}" {
		t.Errorf("tangled code = %q, want %q (no phantom trailing blank line)", code, "fn main() {}")
	}
	if len(lm) != 1 {
		t.Errorf("lineMap has %d entries, want 1 (phantom line leaked?): %+v", len(lm), lm)
	}
}

// An escaped reference does not count as a use (so it can't keep a chunk
// "reached" for the unused-chunk lint, nor appear in weave cross-refs).
func TestEscapedRefNotCountedAsUse(t *testing.T) {
	src := "```fern\n<<*>>=\n\\<<helper>>\n```\n```fern\n<<helper>>=\nx\n```\n"
	if u := Parse(src).usedIn()["helper"]; len(u) != 0 {
		t.Errorf("escaped \\<<helper>> should not count as a use, got users %v", u)
	}
}
