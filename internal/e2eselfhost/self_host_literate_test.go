package e2eselfhost

import (
	"github.com/jakechampion/lang/internal/e2eharness"
	"testing"
)

// Self-host port of internal/literate: `compiler/literate.fern`
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
	runner := x86_64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "literate.fern")
	binPath := buildSelfHostBinFor(t, dir, "literate.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port literate assertion %d failed", code)
	}
}

func TestSelfHostLiterateArm64(t *testing.T) {
	qemu := arm64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "literate.fern")
	binPath := buildSelfHostBinFor(t, dir, "literate.fern", "prog", e2eharness.TargetArm64Linux)
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port literate assertion %d failed", code)
	}
}

func TestSelfHostLiterateWASM(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "literate.fern")
	core := buildSelfHostBinFor(t, dir, "literate.fern", "prog.wasm", e2eharness.TargetWasm32Wasi)
	cmd := e2eharness.RunWasmCore(t, core)
	_, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("fern-port literate assertion %d failed", code)
	}
}
