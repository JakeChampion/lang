package e2e

import "testing"

const pipeNamedArgSrc = `function mk(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return 9 |> mk(b = 1); }
`

func TestX86_64PipeNamedArg(t *testing.T) {
	if out, code := compileAndRunX86_64(t, pipeNamedArgSrc); code != 8 {
		t.Errorf("exit = %d, want 8\n%s", code, out)
	}
}

func TestArm64PipeNamedArg(t *testing.T) {
	if out, code := compileAndRunArm64(t, pipeNamedArgSrc); code != 8 {
		t.Errorf("exit = %d, want 8\n%s", code, out)
	}
}

func TestWASMPipeNamedArg(t *testing.T) {
	if code := runWasm(t, pipeNamedArgSrc); code != 8 {
		t.Errorf("wasm exit = %d, want 8", code)
	}
}
