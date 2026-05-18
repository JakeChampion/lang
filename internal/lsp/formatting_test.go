package lsp

import (
	"testing"
)

func formattingFor(src string) []textEdit {
	s := NewServer()
	s.updateDoc("file:///t", src)
	return runFormatting(s.docs["file:///t"])
}

func TestFormatting_NormalisesWhitespace(t *testing.T) {
	// Tab-indented + extra spaces — the formatter rewrites to its
	// two-space style. We should get a single edit replacing the
	// whole document.
	src := "function main(): i32 {\n\t\treturn 0;\n}\n"
	got := formattingFor(src)
	if len(got) != 1 {
		t.Fatalf("expected one whole-document edit, got %d (%+v)", len(got), got)
	}
	if got[0].Range.Start.Line != 0 || got[0].Range.Start.Character != 0 {
		t.Errorf("edit start = %+v, want (0,0)", got[0].Range.Start)
	}
	if got[0].NewText == src {
		t.Errorf("newText matches input — formatter should normalise whitespace")
	}
}

func TestFormatting_PreservesComments(t *testing.T) {
	// The format-on-save guarantee that motivated unblocking this
	// feature: a comment on its own line + a trailing comment both
	// survive the round trip.
	src := "// header note\n" +
		"function main(): i32 {\n" +
		"  // inline note\n" +
		"  var x: i32 = 7; // trailing\n" +
		"  return x;\n" +
		"}\n"
	got := formattingFor(src)
	if len(got) != 1 {
		t.Fatalf("expected one edit, got %d", len(got))
	}
	out := got[0].NewText
	for _, want := range []string{"// header note", "// inline note", "// trailing"} {
		if !contains(out, want) {
			t.Errorf("formatted output missing %q\n---\n%s", want, out)
		}
	}
}

func TestFormatting_AlreadyFormattedReturnsEmpty(t *testing.T) {
	// Idempotency contract: format → format produces no edits.
	src := "function main(): i32 {\n  return 0;\n}\n"
	first := formattingFor(src)
	if len(first) != 1 {
		t.Fatalf("first format should produce one edit, got %d", len(first))
	}
	// Now format the already-formatted output.
	s := NewServer()
	s.updateDoc("file:///t", first[0].NewText)
	second := runFormatting(s.docs["file:///t"])
	if len(second) != 0 {
		t.Errorf("expected no edits on already-formatted source, got %d (%+v)", len(second), second)
	}
}

func TestFormatting_BailsOnParseError(t *testing.T) {
	// Unterminated function: the parser returns an error and the
	// formatter would emit partial output. We'd rather decline the
	// format than silently destroy the user's broken code.
	src := "function main(): i32 { return"
	got := formattingFor(src)
	if got != nil {
		t.Errorf("expected nil for parse-error source, got %+v", got)
	}
}

func contains(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
