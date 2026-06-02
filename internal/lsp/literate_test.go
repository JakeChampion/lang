package lsp

import (
	"strings"
	"testing"
)

func literateDiagnosticsFor(src string) []Diagnostic {
	s := NewServer()
	s.updateDoc("file:///t.fern.md", src)
	return s.docs["file:///t.fern.md"].diags
}

// A clean literate document produces no diagnostics.
func TestLiterateLSP_Clean(t *testing.T) {
	src := "# ok\n```fern\n<<*>>=\nimport \"core/no_prelude\";\nfunction main(): i32 { return 0; }\n```\n"
	if got := literateDiagnosticsFor(src); len(got) != 0 {
		t.Fatalf("clean literate doc produced %d diagnostics: %+v", len(got), got)
	}
}

// A type error in a chunk is reported on the *document* line, not the
// tangled-intermediate line.
func TestLiterateLSP_DiagnosticRemapped(t *testing.T) {
	// Document lines:        1            2       3                          4 (error)                                5
	src := "```fern\n<<*>>=\nimport \"core/no_prelude\";\nfunction main(): i32 { return \"nope\"; }\n```\n"
	got := literateDiagnosticsFor(src)
	if len(got) == 0 {
		t.Fatal("expected a type-error diagnostic")
	}
	// The bad `return "nope"` is on document line 4 → 0-based line 3.
	if got[0].Range.Start.Line != 3 {
		t.Errorf("diagnostic remapped to line %d, want 3 (document line 4):\n%+v",
			got[0].Range.Start.Line, got[0])
	}
}

// A tangle error (undefined chunk reference) surfaces as a diagnostic at
// the reference's document line.
func TestLiterateLSP_TangleError(t *testing.T) {
	// <<missing>> is referenced on document line 3.
	src := "```fern\n<<*>>=\n<<missing>>\nfunction main(): i32 { return 0; }\n```\n"
	got := literateDiagnosticsFor(src)
	if len(got) == 0 {
		t.Fatal("expected an undefined-chunk diagnostic")
	}
	if !strings.Contains(got[0].Message, "missing") {
		t.Errorf("diagnostic should name the missing chunk, got %q", got[0].Message)
	}
	if got[0].Range.Start.Line != 2 {
		t.Errorf("undefined-chunk diagnostic on line %d, want 2 (document line 3)", got[0].Range.Start.Line)
	}
}

// A multi-file (`file=`) document doesn't crash and yields a clean slate
// (its in-editor diagnostics aren't covered by this slice yet).
func TestLiterateLSP_MultiFileNoCrash(t *testing.T) {
	src := "```fern file=main.fern entry\nfunction main(): i32 { return 0; }\n```\n"
	if got := literateDiagnosticsFor(src); len(got) != 0 {
		t.Errorf("multi-file doc should yield no diagnostics yet, got %+v", got)
	}
}
