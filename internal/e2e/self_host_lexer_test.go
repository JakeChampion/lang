package e2e

import (
	"os"
	"testing"
)

// First step of the self-host port: `examples/self_host/lexer.lang`
// is the Go lexer (`internal/lexer/lexer.go`) re-written in lang.
// Validates the language can express the lexer's logic end-to-end:
// union types for Token kinds, generic-shaped helpers, struct
// methods, mutual recursion (used in skip_trivia / advance via
// self-referential method calls), match with literal patterns
// over the Token union, byte-level string slicing for the
// scan_* routines.
//
// The .lang file's `main()` runs the lexer on a mixed-token input
// (keyword + ident + multi-char punct + integer suffix + string
// + line comment + float suffix + position tracking across a `\n`)
// and asserts the produced Token[] step-by-step. Exit code 0
// means every assertion passed; non-zero codes identify which
// arm failed.
func TestSelfHostLexerX86_64(t *testing.T) {
	src, err := os.ReadFile("../../examples/self_host/lexer.lang")
	if err != nil {
		t.Fatalf("read lexer.lang: %v", err)
	}
	_, code := compileAndRunX86_64(t, string(src))
	if code != 0 {
		t.Errorf("lang-port lexer assertion %d failed", code)
	}
}

func TestSelfHostLexerArm64(t *testing.T) {
	src, err := os.ReadFile("../../examples/self_host/lexer.lang")
	if err != nil {
		t.Fatalf("read lexer.lang: %v", err)
	}
	_, code := compileAndRunArm64(t, string(src))
	if code != 0 {
		t.Errorf("lang-port lexer assertion %d failed", code)
	}
}
