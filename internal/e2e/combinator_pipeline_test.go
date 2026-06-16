package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Fluent generic combinator pipeline — the headline ergonomic payoff of #2663:
// generic receiver-method dispatch over the element type (`xs.map(...)`) plus
// unannotated arrow-lambda return inference (#3360, `(x) => expr`) let
// `xs.map(f).filter(g).fold(init, h)` read left-to-right instead of the old
// inside-out `fold(filter(map(xs,f),g),init,h)` with `(x): U =>` annotations.
//
// [1,2,3,4,5].map(x*2) = [2,4,6,8,10]; .filter(>4) = [6,8,10]; .fold(0,+) = 24.
//
// Generic stdlib methods, so monomorphised — exercised on interp / arm64 / wasm
// (the x86-64 e2e helper skips monomorph; the combinator methods + arrow
// inference are covered there by their own unit/e2e tests).
const combinatorPipelineSrc = `import "std/array";
function main(): i32 {
    return [1, 2, 3, 4, 5]
        .map((x: i32) => x * 2)
        .filter((x: i32) => x > 4)
        .fold(0, (acc: i32, x: i32) => acc + x);
}
`

func TestInterpCombinatorPipeline(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(combinatorPipelineSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 24 {
		t.Errorf("exit = %d, want 24\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestArm64CombinatorPipeline(t *testing.T) {
	if out, code := compileAndRunArm64(t, combinatorPipelineSrc); code != 24 {
		t.Errorf("exit = %d, want 24\n%s", code, out)
	}
}

func TestWASMCombinatorPipeline(t *testing.T) {
	if code := runWasm(t, combinatorPipelineSrc); code != 24 {
		t.Errorf("wasm exit = %d, want 24", code)
	}
}
