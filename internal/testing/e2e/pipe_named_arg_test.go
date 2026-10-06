package e2e

import "testing"

// A named argument on a piped call. `x |> f(b = 1)` prepends x to f's argument
// list, and ArgNames is parallel to Args — so without growing it too, the names
// sat one slot left of the arguments they name and the call was rejected as
// "positional argument after named argument" (E077). Valid code, refused.
//
// mk(a, b) = a - b, so `9 |> mk(b = 1)` binds a=9, b=1 and returns 8. A
// misalignment that bound the values the other way round would return -8, which
// is a different exit status rather than a silent pass.
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
