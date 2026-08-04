package constfold_test

// Determinism + idempotence guard for constant folding.
//
// constfold.Fold mutates *ast.Program in place: literal arithmetic
// collapses to single numbers, all-constant-arg calls to pure
// builtins reduce, dead branches behind compile-time constants get
// the dead arm dropped. Two distinct properties matter:
//
//   - Determinism: Fold(parse(src)) produces the same AST every
//     run. The walker is slice-based today, so determinism is
//     constructive — but the same was true of modload until
//     TestLoadDeterministic caught a real Go-map-iteration leak.
//
//   - Idempotence: Fold(Fold(p)) == Fold(p). A real concern even
//     when each individual rewrite is deterministic: if the
//     walker visits children before parents, a folded child can
//     reveal a new fold-eligible parent on the SECOND pass, and
//     the result keeps changing. We assert the fixed-point
//     property by folding twice, printing both, comparing bytes.
//
// printer.Print is the witness; comparing across two folds + a
// fresh re-parse + fold catches drift in either property.

import (
	"testing"

	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/printer"
)

// matrix favours surfaces where a fold rewrites parents based on
// just-folded children: nested arithmetic (innermost folds first),
// constant-condition if (whole branch elides), and the array /
// string / struct constructors that might fold their literal args
// before the surrounding expression sees them.
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
	var x: i32 = 10;
	var y: i32 = (3 + 4) * (5 - 2);
	if (x > 0) { return y; }
	return 0;
}`,
}

// TestFoldDeterministic asserts Fold is deterministic across
// re-parses: parse twice, fold twice, both printer outputs must
// be byte-identical. A failure means the walker introduced map-
// iteration or other process-state dependence.
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

// TestFoldIdempotent asserts Fold reaches its fixed point in a
// single pass: Fold(Fold(parse(src))) == Fold(parse(src)). A
// failure means the walker visits children in an order that
// reveals new fold opportunities on a second pass — the result
// would keep changing across redundant Fold calls.
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
