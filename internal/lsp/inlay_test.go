package lsp

import (
	"strings"
	"testing"
)

func inlayHintsFor(src string) []inlayHint {
	s := NewServer()
	s.updateDoc("file:///t", src)
	// Wide range covering the whole document.
	return runInlayHints(s.docs["file:///t"], "file:///t", Range{
		Start: Position{Line: 0, Character: 0},
		End:   Position{Line: 9999, Character: 0},
	})
}

func TestInlayHints_InferredVar(t *testing.T) {
	src := "function main(): i32 {\n  var x = 7;\n  return x;\n}\n"
	got := inlayHintsFor(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 inlay hint, got %d: %+v", len(got), got)
	}
	if got[0].Label != ": i32" {
		t.Errorf("hint label = %q, want %q", got[0].Label, ": i32")
	}
	if got[0].Kind != inlayHintKindType {
		t.Errorf("hint kind = %d, want %d (type)", got[0].Kind, inlayHintKindType)
	}
	// Position should be right after the `x`. The line is "  var x = 7;"
	// — `x` is at 0-based col 6, name end at col 7.
	if got[0].Position.Line != 1 || got[0].Position.Character != 7 {
		t.Errorf("hint position = %+v, want (1, 7)", got[0].Position)
	}
}

func TestInlayHints_SkipsAnnotated(t *testing.T) {
	src := "function main(): i32 {\n  var x: i32 = 7;\n  return x;\n}\n"
	got := inlayHintsFor(src)
	if len(got) != 0 {
		t.Errorf("annotated var should produce no hint, got %+v", got)
	}
}

func TestInlayHints_FiltersByRange(t *testing.T) {
	src := "function main(): i32 {\n  var x = 1;\n  var y = 2;\n  return x + y;\n}\n"
	s := NewServer()
	s.updateDoc("file:///t", src)
	// Only ask about line 2 (the y declaration).
	got := runInlayHints(s.docs["file:///t"], "file:///t", Range{
		Start: Position{Line: 2, Character: 0},
		End:   Position{Line: 2, Character: 100},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 hint in range, got %d (all: %+v)", len(got), inlayHintsFor(src))
	}
	if !strings.Contains(got[0].Label, "i32") {
		t.Errorf("expected i32 hint, got %+v", got[0])
	}
}
