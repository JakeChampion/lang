package ir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// Block-expressions (slice 1) are interpreter-only — every compiled
// backend must reject a *ast.BlockExpr cleanly (a clear error, never a
// panic), mirroring the `as?` downcast slice-1 reject. Exercise both the
// wasm pointer width (ptrW=4) and the native one (ptrW=8) so neither
// lowering path panics.
func TestBlockExprCompiledReject(t *testing.T) {
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
		_, err := LowerWith(prog, info, ptrW)
		if err == nil {
			t.Fatalf("ptrW=%d: expected a clean reject error, got nil", ptrW)
		}
		if !strings.Contains(err.Error(), "block-expression") {
			t.Errorf("ptrW=%d: error should mention block-expression, got %v", ptrW, err)
		}
	}
}
