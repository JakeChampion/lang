package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// std/error provides the canonical `Error` supertype: any type with a
// `message()` can be returned as `dyn error.Error`, propagated through `?`
// (each concrete error boxes into `dyn error.Error`), and handled
// uniformly. handler(true) → Ok(43); handler(false) → Err whose
// .message() is "missing" (len 7). 43 + 7 = 50. See std/error.fern.
const stdErrorSrc = `import "std/error" as error;
struct NotFound { what: string }
impl error.Error for NotFound { function message(self: Self): string { return self.what; } }
function find(ok: boolean): Result[i32, NotFound] {
    if (ok) { return Ok(42); }
    return Err(NotFound { what: "missing" });
}
function handler(ok: boolean): Result[i32, dyn error.Error] {
    var v: i32 = find(ok)?;
    return Ok(v + 1);
}
function main(): i32 {
    var a: i32 = match (handler(true)) { Ok(v) => v, Err(e) => 0 };
    var b: i32 = match (handler(false)) { Ok(v) => 0, Err(e) => e.message().len() };
    return a + b;
}
`

func TestInterpStdError(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(stdErrorSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 50 {
		t.Errorf("exit = %d, want 50\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64StdError(t *testing.T) {
	if out, code := compileAndRunX86_64(t, stdErrorSrc); code != 50 {
		t.Errorf("exit = %d, want 50\n%s", code, out)
	}
}

func TestArm64StdError(t *testing.T) {
	if out, code := compileAndRunArm64(t, stdErrorSrc); code != 50 {
		t.Errorf("exit = %d, want 50\n%s", code, out)
	}
}

func TestWASMStdError(t *testing.T) {
	if code := runWasm(t, stdErrorSrc); code != 50 {
		t.Errorf("wasm exit = %d, want 50", code)
	}
}
