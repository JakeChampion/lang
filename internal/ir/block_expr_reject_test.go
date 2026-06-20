package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// Block-expressions (`{ stmts; tail }` in an `if`/`match` value branch)
// now lower on every compiled backend (slice 2). This was the slice-1
// reject test — flipped, like the `as?` downcast codegen PR flipped its
// reject test, to assert the BlockExpr LOWERS cleanly (no error, no
// panic) at both the wasm pointer width (ptrW=4) and the native one
// (ptrW=8). End-to-end value correctness lives in the e2e differential
// tests; here we only guard that the IR layer accepts it.
func TestBlockExprCompiledLowers(t *testing.T) {
	src := `function main(): i32 {
		var e = 5;
		var x: i32 = if (e > 0) { var k = e + 1; k } else { 0 };
		return x;
	}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, ptrW := range []int{4, 8} {
		if _, err := LowerWith(prog, info, ptrW); err != nil {
			t.Fatalf("ptrW=%d: block-expression should lower cleanly now, got %v", ptrW, err)
		}
	}
}
