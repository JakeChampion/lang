package lexer

// Determinism guard for the lexer.
//
// Tokenize is the first transformation in the compilation pipeline:
// every stage downstream (parser → modload → checker → IR → codegen
// → interp) reads from the token stream it produces. Nondeterminism
// here would propagate to every subsequent stage at once, and the
// determinism guards added at every later layer (parser #1719,
// modload #1708, ir, codegen, interp) would all light up at the
// same time on flaky failures.
//
// A linear scanner is normally deterministic by construction, but
// the same was true of modload until TestLoadDeterministic caught a
// real Go-map-iteration leak. Pinning the lexer here means a
// future change that introduces a map-driven keyword table (e.g.
// returning the kind via `for k, v := range keywords`) would fail
// loudly at the source rather than as a flaky downstream diff.
//
// Comparing tokens directly catches more than printer-byte-equality
// would: positions, kinds, and the FStringPart slices must all
// match exactly. Two tokenisations of the same source must produce
// equal slices in every field.

import (
	"reflect"
	"testing"
)

// determinismMatrix favours the lex paths most prone to ordering
// nondeterminism if the scanner ever started routing decisions
// through a Go map: keyword lookup (vs identifier), number literal
// parsing, string + f-string parts, multi-character operators,
// and the whole punctuation set in one go.
var determinismMatrix = map[string]string{
	"minimal": `function main(): i32 { return 0; }`,

	"keywords_and_idents": `
function classify(n: i32): i32 {
	if (n > 0) { return 1; } else if (n < 0) { return 0 - 1; }
	while (false) { break; }
	return 0;
}`,

	"numbers_and_operators": `
function main(): i32 {
	var a: i32 = 0xDEAD_BEEF;
	var b: f64 = 3.14e-2;
	var c: i32 = 42 + (a >> 4) & 0xFF | a;
	var d: bool = a == 0 || c != 0 && a < c;
	return c;
}`,

	"strings_and_fstrings": `
function main(): i32 {
	var s: string = "hello\nworld";
	var n: i32 = 42;
	print(f"hello, {s}, value={n}, end");
	print("plain literal");
	return 0;
}`,

	"comments_and_decls": `
// top-level comment
struct Point { x: i32, y: i32 } // trailing comment
enum Shape { Circle(i32), Rect(i32, i32) }
// before fn
function main(): i32 { return 0; }`,
}

// TestTokenizeDeterministic tokenises each program several times
// and asserts every tokenisation produces an identical token + comment
// slice. A failure means the lexer's token order or per-token contents
// depend on some non-deterministic process state — most likely Go
// map iteration order, but also possible: pointer hash dependence
// (in keyword interning) or goroutine scheduling.
func TestTokenizeDeterministic(t *testing.T) {
	for name, src := range determinismMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			firstToks, firstComments, err := Tokenize(src)
			if err != nil {
				t.Fatalf("tokenize: %v", err)
			}
			for i := 0; i < 4; i++ {
				toks, comments, err := Tokenize(src)
				if err != nil {
					t.Fatalf("tokenize run %d: %v", i+2, err)
				}
				if !reflect.DeepEqual(toks, firstToks) {
					t.Fatalf("token stream not deterministic on run %d: %d tokens vs %d tokens",
						i+2, len(toks), len(firstToks))
				}
				if !reflect.DeepEqual(comments, firstComments) {
					t.Fatalf("comment stream not deterministic on run %d: %d comments vs %d comments",
						i+2, len(comments), len(firstComments))
				}
			}
		})
	}
}
