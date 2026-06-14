package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Operator overloading: `+` `-` `*` `/` on a struct/enum desugar to the
// type's conventionally-named method (add/sub/mul/div), mirroring the
// existing composite `==` (eq) / `<` (cmp) overloads. Here a 1-D vector:
// (20+4)=24, -4=20, *4=80, /4=20 → r.x = 20. See #2706.
const operatorOverloadSrc = `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function (self: V) sub(o: V): V { return V { x: self.x - o.x }; }
function (self: V) mul(o: V): V { return V { x: self.x * o.x }; }
function (self: V) div(o: V): V { return V { x: self.x / o.x }; }
function main(): i32 {
    var a: V = V { x: 20 };
    var b: V = V { x: 4 };
    var r: V = a + b;
    r = r - b;
    r = r * b;
    r = r / b;
    return r.x;
}
`

func TestInterpOperatorOverload(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(operatorOverloadSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 20 {
		t.Errorf("exit = %d, want 20\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64OperatorOverload(t *testing.T) {
	out, code := compileAndRunX86_64(t, operatorOverloadSrc)
	if code != 20 {
		t.Errorf("exit = %d, want 20\n%s", code, out)
	}
}

func TestArm64OperatorOverload(t *testing.T) {
	out, code := compileAndRunArm64(t, operatorOverloadSrc)
	if code != 20 {
		t.Errorf("exit = %d, want 20\n%s", code, out)
	}
}

func TestWASMOperatorOverload(t *testing.T) {
	if code := runWasm(t, operatorOverloadSrc); code != 20 {
		t.Errorf("wasm exit = %d, want 20", code)
	}
}
