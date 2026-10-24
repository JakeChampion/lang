package constfold_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

var matrix = map[string]string{
	"minimal": `function main(): i32 { return 0; }`,

	"nested_arithmetic": `
function main(): i32 {
	return ((1 + 2) * (3 + 4)) - (5 * 6) + (7 + 8) * (9 - 10);
}`,

	"bitwise": `
function main(): i32 {
	return (0xF0 | 0x0F) & 0xFF ^ (0x12 >> 1);
}`,

	"if_const_condition": `
function main(): i32 {
	if (1 + 1 == 2) {
		return 100;
	} else {
		return 200;
	}
	return 0;
}`,

	"mixed_consts_locals": `
function main(): i32 {
	let x: i32 = 10;
	let y: i32 = (3 + 4) * (5 - 2);
	if (x > 0) { return y; }
	return 0;
}`,
}

func TestFoldDeterministic(t *testing.T) {
	for name, src := range matrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			first := mustParseFoldPrint(t, src)
			for i := 0; i < 4; i++ {
				again := mustParseFoldPrint(t, src)
				if again != first {
					t.Fatalf("constfold not deterministic on run %d: output differs (%d vs %d bytes)",
						i+2, len(first), len(again))
				}
			}
		})
	}
}

func TestFoldIdempotent(t *testing.T) {
	for name, src := range matrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			prog, err := parser.Parse(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := constfold.Fold(prog, nil); err != nil {
				t.Fatalf("fold pass 1: %v", err)
			}
			once := printer.Print(prog)
			if err := constfold.Fold(prog, nil); err != nil {
				t.Fatalf("fold pass 2: %v", err)
			}
			twice := printer.Print(prog)
			if once != twice {
				t.Fatalf("constfold not idempotent: first pass = %d bytes, second pass = %d bytes",
					len(once), len(twice))
			}
		})
	}
}

func mustParseFoldPrint(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	return printer.Print(prog)
}
