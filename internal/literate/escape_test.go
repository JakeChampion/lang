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

// An escaped reference does not count as a use (so it can't keep a chunk
// "reached" for the unused-chunk lint, nor appear in weave cross-refs).
func TestEscapedRefNotCountedAsUse(t *testing.T) {
	src := "```fern\n<<*>>=\n\\<<helper>>\n```\n```fern\n<<helper>>=\nx\n```\n"
	if u := Parse(src).usedIn()["helper"]; len(u) != 0 {
		t.Errorf("escaped \\<<helper>> should not count as a use, got users %v", u)
	}
}
