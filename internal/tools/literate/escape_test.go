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
		`<<x>>`:      `<<x>>`,
		`foo()`:      `foo()`,
		`a \<< b`:    `a \<< b`,
	}
	for in, want := range cases {
		if got := deEscapeRef(in); got != want {
			t.Errorf("deEscapeRef(%q) = %q, want %q", in, got, want)
		}
	}
}

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

func TestTangleEscapedRefColShift(t *testing.T) {
	src := "```fern\n<<*>>=\nfn main() {\n    <<body>>\n}\n```\n" +
		"```fern\n<<body>>=\n\\<<lit>> = 5;\n```\n"
	code, lm, err := Parse(src).Tangle()
	if err != nil {
		t.Fatalf("tangle: %v", err)
	}
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
	if lm[gi].ColShift != 3 {
		t.Errorf("ColShift = %d, want 3 (4 indent minus 1 stripped backslash)", lm[gi].ColShift)
	}
	if lm[gi].Lit != 9 {
		t.Errorf("Lit = %d, want 9 (the \\<<lit>> document line)", lm[gi].Lit)
	}
	if docCol := 7 - lm[gi].ColShift; docCol != 4 {
		t.Errorf("remapped column = %d, want 4 (the `l` of \\<<lit>>)", docCol)
	}
}

func TestTangleUnclosedFenceNoPhantomLine(t *testing.T) {
	src := "```fern\n<<*>>=\nfn main() {}\n"
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
