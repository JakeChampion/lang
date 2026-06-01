package e2e

// Formatter idempotence + round-trip sweep over the real example
// corpus.
//
// The printer's own package tests (internal/printer) exercise format
// idempotence and parser round-trip, but only on hand-written
// snippets. This sweep runs the same two properties the CLI `-fmt`
// flag promises — and that README.md advertises as "format → parse →
// format is byte-stable" — across every shipping program under
// examples/, which is far more syntactically diverse than the unit
// snippets. It's a parse-only check (no modload / checker / codegen),
// so it stays fast and hermetic.
//
// For each example file:
//  1. parse the source (must succeed — these are shipping programs);
//  2. format it, then re-parse the formatted output (catches the
//     formatter emitting un-parseable text — e.g. a dropped last
//     call-argument lambda leaving a dangling `f(xs, )` comma);
//  3. format the re-parsed AST again and assert it equals the first
//     formatting (idempotence — catches cosmetic instabilities like
//     a string literal that mutates across passes).
//
// This sweep is what surfaced both the dropped-lambda formatter gap
// and the UTF-8 string-literal lexer corruption; it's the regression
// net for the whole format pipeline against real code.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/printer"
)

func TestFormatterExampleCorpusRoundTrip(t *testing.T) {
	var files []string
	for _, dir := range []string{"../../examples", "../../examples/tests"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir %s: %v", dir, err)
		}
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".fern" {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	if len(files) == 0 {
		t.Fatal("no .fern example files found — wrong working directory?")
	}

	for _, f := range files {
		f := f
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			prog, err := parser.Parse(string(src))
			if err != nil {
				t.Fatalf("parse of shipping example failed: %v", err)
			}
			once := printer.Format(prog)
			prog2, err := parser.Parse(once)
			if err != nil {
				t.Fatalf("formatted output failed to re-parse: %v\n--- formatted ---\n%s", err, once)
			}
			twice := printer.Format(prog2)
			if once != twice {
				t.Errorf("format not idempotent (first pass != second pass); first differing run is the second format")
			}
		})
	}
}
