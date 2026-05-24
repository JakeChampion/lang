package printer

import (
	"strings"
	"testing"
)

// Identical inputs produce no diff at all — empty string out, so
// callers can use `len(diff) == 0` as a fast same-content check.
func TestUnifiedDiffEmptyWhenIdentical(t *testing.T) {
	src := "function f(): i32 {\n  return 1;\n}\n"
	got := UnifiedDiff(src, src, "a.fern", "b.fern")
	if got != "" {
		t.Errorf("expected empty diff for identical inputs, got:\n%s", got)
	}
}

// A single changed line shows up with the expected `-`/`+` prefix
// pair plus surrounding context.
func TestUnifiedDiffSingleLineChange(t *testing.T) {
	a := "line one\nline two\nline three\n"
	b := "line one\nline 2\nline three\n"
	got := UnifiedDiff(a, b, "a", "b")
	if !strings.Contains(got, "--- a\n") {
		t.Errorf("missing --- header:\n%s", got)
	}
	if !strings.Contains(got, "+++ b\n") {
		t.Errorf("missing +++ header:\n%s", got)
	}
	if !strings.Contains(got, "-line two\n") {
		t.Errorf("expected `-line two`:\n%s", got)
	}
	if !strings.Contains(got, "+line 2\n") {
		t.Errorf("expected `+line 2`:\n%s", got)
	}
}

// The hunk header carries the correct line-count metadata. Three
// equal lines around a single change produce `@@ -2,1 +2,1 @@` (the
// change is on line 2; only one line on each side); with default
// context 3 we'd get all surrounding equal lines included on each
// side too, so the actual numbers reflect that.
func TestUnifiedDiffHunkHeaderHasLineNumbers(t *testing.T) {
	a := "a\nb\nc\nd\ne\n"
	b := "a\nb\nC\nd\ne\n"
	got := UnifiedDiff(a, b, "x", "y")
	// Hunk header begins with @@; we don't pin exact line counts
	// (they vary with context window width) — just make sure a
	// well-formed `@@ -N,M +N,M @@` header appears.
	if !strings.Contains(got, "@@ -1,5 +1,5 @@") {
		t.Errorf("expected hunk header `@@ -1,5 +1,5 @@`:\n%s", got)
	}
}

// Multiple change regions far apart get separate hunks. The default
// context is 3 lines, so changes 7 lines apart shouldn't merge.
func TestUnifiedDiffSeparatesDistantChanges(t *testing.T) {
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	b := "1!\n2\n3\n4\n5\n6\n7\n8\n9\n10!\n"
	got := UnifiedDiff(a, b, "x", "y")
	hunkCount := strings.Count(got, "@@ -")
	if hunkCount != 2 {
		t.Errorf("expected 2 hunks for distant changes, got %d:\n%s", hunkCount, got)
	}
}

// Insertions and deletions both appear with the right prefix; the
// surviving context lines stay intact.
func TestUnifiedDiffInsertAndDelete(t *testing.T) {
	a := "alpha\nbeta\ngamma\n"
	b := "alpha\nBETA\ngamma\nDELTA\n"
	got := UnifiedDiff(a, b, "x", "y")
	if !strings.Contains(got, "-beta\n") {
		t.Errorf("expected `-beta`:\n%s", got)
	}
	if !strings.Contains(got, "+BETA\n") {
		t.Errorf("expected `+BETA`:\n%s", got)
	}
	if !strings.Contains(got, "+DELTA\n") {
		t.Errorf("expected `+DELTA` (insertion):\n%s", got)
	}
}

// Files that differ only by a missing trailing newline still emit a
// diff (the diff helper preserves the line content as-stored).
func TestUnifiedDiffHandlesMissingTrailingNewline(t *testing.T) {
	a := "x\ny\n"
	b := "x\ny"
	got := UnifiedDiff(a, b, "x", "y")
	if got == "" {
		t.Errorf("expected a diff when trailing newline differs")
	}
}
