package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The `::` path separator is the namespaced calling syntax for associated
// functions (`Point::origin()`) and module-qualified references
// (`helpers::add5`, `helpers::BONUS`). It produces the same FieldAccess as
// the `.` form, so it resolves through the existing assoc-fn + module
// paths. See #2700.
const pathSepSrc = `struct Point { x: i32, y: i32 }
trait Make { function make(a: i32, b: i32): Self; }
impl Make for Point { function make(a: i32, b: i32): Self { return Point { x: a, y: b }; } }
function main(): i32 {
    var p: Point = Point::make(20, 22);   // associated function via ::
    return p.x + p.y;                      // 42
}
`

func TestInterpPathSepCall(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(pathSepSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64PathSepCall(t *testing.T) {
	out, code := compileAndRunX86_64(t, pathSepSrc)
	if code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestArm64PathSepCall(t *testing.T) {
	out, code := compileAndRunArm64(t, pathSepSrc)
	if code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMPathSepCall(t *testing.T) {
	if code := runWasm(t, pathSepSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
