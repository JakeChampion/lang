package literate

import (
	"strings"
	"testing"
)

func TestDoctestsTangleAndName(t *testing.T) {
	src := strings.Join([]string{
		"```fern",
		"<<greet>>=",
		"pub fn greet(): i32 { return 1; }",
		"```",
		"```fern test name=my-example",
		"<<greet>>",
		"fn main(): i32 { return greet(); }",
		"```",
		"```fern test", // unnamed → positional default
		"fn main(): i32 { return 0; }",
		"```",
	}, "\n")
	doc := Parse(src)
	if !doc.HasDoctests() {
		t.Fatal("HasDoctests should be true")
	}
	tests, err := doc.Doctests()
	if err != nil {
		t.Fatalf("Doctests: %v", err)
	}
	if len(tests) != 2 {
		t.Fatalf("got %d doctests, want 2", len(tests))
	}
	// The first example's <<greet>> reference is expanded inline.
	if !strings.Contains(tests[0].Code, "pub fn greet(): i32 { return 1; }") {
		t.Errorf("doctest 1 should expand <<greet>>:\n%s", tests[0].Code)
	}
	if tests[0].Name != "my-example" {
		t.Errorf("doctest 1 name = %q, want my-example", tests[0].Name)
	}
	if !strings.Contains(tests[1].Name, "doctest 2") {
		t.Errorf("unnamed doctest should get a positional name, got %q", tests[1].Name)
	}
}

// A document without `test` blocks has no doctests.
func TestNoDoctests(t *testing.T) {
	doc := Parse("```fern\n<<*>>=\nfn main() {}\n```\n")
	if doc.HasDoctests() {
		t.Error("HasDoctests should be false")
	}
	tests, err := doc.Doctests()
	if err != nil || len(tests) != 0 {
		t.Errorf("expected no doctests, got %d err %v", len(tests), err)
	}
}
