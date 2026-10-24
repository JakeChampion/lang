package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/syntax/printer"
	"github.com/jakechampion/lang/internal/tools/literate"
)

func TestFormatterExampleCorpusRoundTrip(t *testing.T) {
	var files []string
	for _, dir := range []string{
		"../../../examples",
		"../../../examples/cli",
		"../../../tests/proposals",
		"../../../examples/vcl",
		"../../../examples/wasm",
		"../../../tests/stdlib",
		"../../../bench",
		"../../../bench/perceus",
		"../../stdlib/std",
		"../../stdlib/core",
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir %s: %v", dir, err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".fern") || strings.HasSuffix(e.Name(), ".fern.md") {
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
			src, err := literate.ReadFile(f)
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
			for _, synth := range []string{"__range_hi_", "__range_go_", "__foreach_iter_", "__foreach_idx_", "__foreach_len_", "__forc_"} {
				if strings.Contains(once, synth) && !strings.Contains(string(src), synth) {
					t.Errorf("formatted output leaks the synthetic name %q, which the source does not spell\n--- formatted ---\n%s", synth, once)
				}
			}
		})
	}
}
