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
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/printer"
)

func TestFormatterExampleCorpusRoundTrip(t *testing.T) {
	var files []string
	// Every .fern directory under examples/ ships as a program the CLI
	// -fmt flag promises to round-trip cleanly, so the sweep takes all of
	// them rather than the three it used to name — proposals/ alone is 59
	// files of deliberately unusual syntax, which is the corpus most likely
	// to find a printer gap. internal/stdlib/{std,core}/ ship as the standard
	// library every program imports; a formatter regression there would
	// silently rewrite library code on every `-fmt -w` pass against a module
	// that uses these helpers. Coverage extension closed e.g. the
	// dropped-lambda + UTF-8 lexer corruption already caught on examples.
	for _, dir := range []string{
		"../../examples",
		"../../examples/bench",
		"../../examples/cli",
		"../../examples/proposals",
		"../../examples/tests",
		"../../examples/wasm",
		"../../internal/stdlib/std",
		"../../internal/stdlib/core",
	} {
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
			// Content preservation: formatting must not DROP whole
			// declarations. Idempotence alone can't catch this — a
			// formatter that silently omits every trait/impl still
			// round-trips stably (both passes lack them), which is
			// exactly how the trait/impl-emission gap hid. Compare the
			// per-kind declaration counts across the reparse.
			for _, c := range []struct {
				kind      string
				got, want int
			}{
				{"funcs", len(prog2.Funcs), len(prog.Funcs)},
				{"structs", len(prog2.Structs), len(prog.Structs)},
				{"enums", len(prog2.Enums), len(prog.Enums)},
				{"unions", len(prog2.Unions), len(prog.Unions)},
				{"traits", len(prog2.Traits), len(prog.Traits)},
				{"impls", len(prog2.Impls), len(prog.Impls)},
				{"consts", len(prog2.Consts), len(prog.Consts)},
			} {
				if c.got != c.want {
					t.Errorf("format dropped %s: reparsed %d, want %d\n--- formatted ---\n%s", c.kind, c.got, c.want, once)
				}
			}
			twice := printer.Format(prog2)
			if once != twice {
				t.Errorf("format not idempotent (first pass != second pass); first differing run is the second format")
			}
			// No parse-time desugar reaches the output. Idempotence is blind to
			// this class — format → parse → format is a fixed point on the
			// leaked expansion too, since the second pass has nothing left to
			// desugar (#6770) — so the property to assert is that the formatter
			// never writes a name only the compiler can synthesise back over the
			// user's source. `-fmt -w` would make that permanent, and the names
			// carry a counter or the loop's line/column, so the same code moved
			// down a line reformats to different text.
			for _, synth := range []string{"__range_hi_", "__foreach_iter_", "__foreach_idx_", "__foreach_len_", "__forc_"} {
				if strings.Contains(once, synth) && !strings.Contains(string(src), synth) {
					t.Errorf("formatted output leaks the synthetic name %q, which the source does not spell\n--- formatted ---\n%s", synth, once)
				}
			}
		})
	}
}
