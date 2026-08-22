package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A `for..in` inside a nested named function. The desugar that lowers ForEach
// to the `.len()` + index loop walks statements, and a nested named function is
// an *ast.FuncDecl statement whose body is a separate block — it had no arm in
// that switch, so the ForEach survived to IR and every such program failed to
// compile with "ir: unsupported statement *ast.ForEach". Nothing reached it:
// the lambda arm covers `var f = function() {...}`, which is an EXPRESSION.
//
// sum over [1,2,3] = 6, plus the same loop one level deeper inside a lambda in
// the nested function = 12.
const foreachInNestedFuncSrc = `function total(xs: i32[]): i32 {
    function sum(acc: i32): i32 {
        var n: i32 = acc;
        for x in xs {
            n = n + x;
        }
        var again = function(): i32 {
            var m: i32 = 0;
            for y in xs {
                m = m + y;
            }
            return m;
        };
        return n + again();
    }
    return sum(0);
}
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    return total(a);
}
`

func TestInterpForEachInNestedFunc(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(foreachInNestedFuncSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 12 {
		t.Errorf("exit = %d, want 12\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64ForEachInNestedFunc(t *testing.T) {
	out, code := compileAndRunX86_64(t, foreachInNestedFuncSrc)
	if code != 12 {
		t.Errorf("exit = %d, want 12\n%s", code, out)
	}
}

func TestArm64ForEachInNestedFunc(t *testing.T) {
	out, code := compileAndRunArm64(t, foreachInNestedFuncSrc)
	if code != 12 {
		t.Errorf("exit = %d, want 12\n%s", code, out)
	}
}

func TestWASMForEachInNestedFunc(t *testing.T) {
	if code := runWasm(t, foreachInNestedFuncSrc); code != 12 {
		t.Errorf("wasm exit = %d, want 12", code)
	}
}
