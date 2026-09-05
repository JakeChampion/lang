package printer

import (
	"math/rand"
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

// scriptSides replays an edit script: the deleted and equal lines have to
// reproduce `a` exactly and the inserted and equal lines `b`, or the diff
// is not a diff of those two inputs at all.
func scriptSides(script []op) (string, string) {
	var a, b strings.Builder
	for _, o := range script {
		switch o.kind {
		case ' ':
			a.WriteString(o.line)
			b.WriteString(o.line)
		case '-':
			a.WriteString(o.line)
		case '+':
			b.WriteString(o.line)
		}
	}
	return a.String(), b.String()
}

func scriptEdits(script []op) int {
	n := 0
	for _, o := range script {
		if o.kind != ' ' {
			n++
		}
	}
	return n
}

// lcsEdits is the shortest possible edit-script length for a and b,
// computed straight from an LCS table. Quadratic in both time and memory,
// which is exactly why editScript may not use one — but it is the ground
// truth the search is checked against on inputs small enough to afford it.
func lcsEdits(a, b []string) int {
	m, n := len(a), len(b)
	prev := make([]int, n+1)
	cur := make([]int, n+1)
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				cur[j] = prev[j+1] + 1
			case prev[j] >= cur[j+1]:
				cur[j] = prev[j]
			default:
				cur[j] = cur[j+1]
			}
		}
		prev, cur = cur, prev
	}
	return m + n - 2*prev[0]
}

// The search has to agree with the LCS table on every small input: same
// two sides reconstructed, and no edit more than the minimum. A shortest
// script is not unique, so the ops themselves are not pinned — only the
// count is.
func TestEditScriptIsMinimal(t *testing.T) {
	rng := rand.New(rand.NewSource(20260905))
	lines := []string{"a\n", "b\n", "c\n", "d\n", "e\n"}
	for iter := 0; iter < 8000; iter++ {
		// A small alphabet makes ties between equally short scripts the
		// common case rather than a rarity.
		alphabet := 2 + rng.Intn(len(lines)-1)
		a := make([]string, rng.Intn(14))
		for i := range a {
			a[i] = lines[rng.Intn(alphabet)]
		}
		b := make([]string, rng.Intn(14))
		for i := range b {
			b[i] = lines[rng.Intn(alphabet)]
		}
		script := editScript(a, b, maxSearchCost)
		gotA, gotB := scriptSides(script)
		if gotA != strings.Join(a, "") || gotB != strings.Join(b, "") {
			t.Fatalf("script does not reproduce its inputs\na=%q b=%q\ngot a=%q b=%q", a, b, gotA, gotB)
		}
		if got, want := scriptEdits(script), lcsEdits(a, b); got != want {
			t.Fatalf("script has %d edits, minimum is %d\na=%q b=%q", got, want, a, b)
		}
	}
}

// Past its cost cap the search stops looking for the shortest script and
// splits at the furthest point it reached. Real sources never get there,
// so the path is driven here with a cap of 1: the script may be longer
// than the minimum, but it still has to be a diff of the two inputs and
// it still has to terminate.
func TestEditScriptBeyondCostCapStillReconstructs(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	lines := []string{"a\n", "b\n", "c\n"}
	for iter := 0; iter < 4000; iter++ {
		a := make([]string, rng.Intn(20))
		for i := range a {
			a[i] = lines[rng.Intn(len(lines))]
		}
		b := make([]string, rng.Intn(20))
		for i := range b {
			b[i] = lines[rng.Intn(len(lines))]
		}
		script := editScript(a, b, 1)
		gotA, gotB := scriptSides(script)
		if gotA != strings.Join(a, "") || gotB != strings.Join(b, "") {
			t.Fatalf("capped script does not reproduce its inputs\na=%q b=%q\ngot a=%q b=%q", a, b, gotA, gotB)
		}
	}
}

// The `-d` path on a large file with most of its lines changed is what
// OOM-killed the formatter (#8526): the LCS table it used to build is
// 20 GB at this size. Plain formatting was never the expensive half, so a
// gate that only formats proves nothing — this one has to diff.
//
// The vocabulary is deliberately small and shared between the two sides,
// so the alignment search cannot be short-circuited by discarding lines
// that occur on one side only.
func TestUnifiedDiffLargeMostlyChangedFile(t *testing.T) {
	const lines = 50000
	stmts := []string{
		"let total = total + width;",
		"if (n > 0) {",
		"}",
		"return acc;",
		"push(out, item);",
		"} else {",
		"var acc = 0;",
		"acc = step(acc, n);",
	}
	var before, after strings.Builder
	for i := 0; i < lines; i++ {
		stmt := stmts[i%len(stmts)]
		depth := (i / 3) % 4
		before.WriteString(strings.Repeat("  ", depth) + stmt + "\n")
		// Reindent by one level, and drop every 37th line outright so the
		// diff is not a pure one-for-one substitution.
		if i%37 != 0 {
			after.WriteString(strings.Repeat("  ", depth+1) + stmt + "\n")
		}
	}

	script := editScript(splitLines(before.String()), splitLines(after.String()), maxSearchCost)
	gotA, gotB := scriptSides(script)
	if gotA != before.String() || gotB != after.String() {
		t.Fatalf("script does not reproduce its inputs")
	}

	diff := UnifiedDiff(before.String(), after.String(), "big.fern", "big.fern")
	if !strings.HasPrefix(diff, "--- big.fern\n+++ big.fern\n@@ ") {
		t.Fatalf("malformed diff header: %.60q", diff)
	}
	if n := strings.Count(diff, "\n-"); n < lines/2 {
		t.Errorf("expected the diff to carry most of the file, got %d deletions", n)
	}
}
