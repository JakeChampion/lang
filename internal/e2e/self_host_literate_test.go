package e2e

import (
	"os"
	"testing"
)

// Self-host port of internal/literate: `examples/self_host/literate.fern`
// is the Knuth-style tangle engine re-written in Fern. Tangling is a
// pure `string -> string` transform that slots in ahead of
// `lexer.tokenize` (pipeline.fern), so literate support reaches the
// self-hosted compiler without touching the rest of the pipeline.
//
// The .fern file's `main()` asserts the engine against the same cases
// as the Go unit tests (internal/literate/literate_test.go): root-only
// tangle, out-of-order references, same-name concatenation,
// indentation-preserving expansion, display-only blocks, the three
// structured errors (missing root, undefined reference, cyclic
// reference), and the multi-file `file=PATH` tangle (two-module tangle
// with a shared chunk, same-path concatenation, entry resolution, and
// an undefined-ref error from a file-root — mirroring
// internal/literate/tanglefiles_test.go). Exit code 0 means every
// assertion passed; a non-zero code identifies which one failed.
func TestSelfHostLiterateX86_64(t *testing.T) {
	src, err := os.ReadFile("../../examples/self_host/literate.fern")
	if err != nil {
		t.Fatalf("read literate.fern: %v", err)
	}
	_, code := compileAndRunX86_64(t, string(src))
	if code != 0 {
		t.Errorf("fern-port literate assertion %d failed", code)
	}
}

func TestSelfHostLiterateArm64(t *testing.T) {
	src, err := os.ReadFile("../../examples/self_host/literate.fern")
	if err != nil {
		t.Fatalf("read literate.fern: %v", err)
	}
	_, code := compileAndRunArm64(t, string(src))
	if code != 0 {
		t.Errorf("fern-port literate assertion %d failed", code)
	}
}

func TestSelfHostLiterateWASM(t *testing.T) {
	src, err := os.ReadFile("../../examples/self_host/literate.fern")
	if err != nil {
		t.Fatalf("read literate.fern: %v", err)
	}
	if code := compileAndRunWasmbinMain(t, string(src)); code != 0 {
		t.Errorf("fern-port literate assertion %d failed", code)
	}
}
