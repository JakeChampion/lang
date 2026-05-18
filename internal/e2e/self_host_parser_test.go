package e2e

import (
	"os"
	"testing"
)

// Second step of the self-host port: `examples/self_host/parser.lang`
// is a recursive-descent parser written in lang, layered on top of
// the inlined lexer (the lexer source is copied into the same file
// because cross-module union-variant pattern matching — e.g.
// `lexer.TokIdent(x) => …` — isn't supported yet). Together they
// exercise: union types over Token *and* Expr/Stmt, struct methods
// with implicit struct→union return-position wrap, precedence
// climbing, recursive parser combinators that thread parser state
// via value semantics (each helper returns a fresh `Par`), nested
// `match` over union variants inside the validation harness.
//
// The .lang file's `main()` parses the source
//
//   var x = 1 + 2 * 3; var y = (1 + 2) * 3; return x + y;
//
// and asserts the resulting Stmt[] shape: precedence rules give
// `x = 1 + (2*3)`, parens override to `(1+2) * 3`, and `return x + y`
// is a binary `+` of two idents. Exit code 0 means every assertion
// passed; non-zero codes identify which arm failed.
func TestSelfHostParserX86_64(t *testing.T) {
	src, err := os.ReadFile("../../examples/self_host/parser.lang")
	if err != nil {
		t.Fatalf("read parser.lang: %v", err)
	}
	_, code := compileAndRunX86_64(t, string(src))
	if code != 0 {
		t.Errorf("lang-port parser assertion %d failed", code)
	}
}

func TestSelfHostParserArm64(t *testing.T) {
	src, err := os.ReadFile("../../examples/self_host/parser.lang")
	if err != nil {
		t.Fatalf("read parser.lang: %v", err)
	}
	_, code := compileAndRunArm64(t, string(src))
	if code != 0 {
		t.Errorf("lang-port parser assertion %d failed", code)
	}
}
