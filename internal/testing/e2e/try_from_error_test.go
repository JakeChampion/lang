package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const tryFromErrorSrc = `trait From[T] { function from(value: T): Self; }
struct IoErr { code: i32 }
struct AppErr { code: i32 }
impl From[IoErr] for AppErr {
    function from(value: IoErr): Self { return AppErr { code: value.code + 100 }; }
}
function read(ok: boolean): Result[i32, IoErr] {
    if (ok) { return Ok(8); }
    return Err(IoErr { code: 42 });
}
function run(ok: boolean): Result[i32, AppErr] {
    let v: i32 = read(ok)?;
    return Ok(v);
}
function main(): i32 {
    let a: i32 = match (run(true)) { Ok(v) => v, Err(e) => 0 };
    let b: i32 = match (run(false)) { Ok(v) => 0, Err(e) => e.code };
    return a + b;
}
`

func TestInterpTryFromError(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(tryFromErrorSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 150 {
		t.Errorf("exit = %d, want 150\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64TryFromError(t *testing.T) {
	if out, code := compileAndRunX86_64(t, tryFromErrorSrc); code != 150 {
		t.Errorf("exit = %d, want 150\n%s", code, out)
	}
}

func TestArm64TryFromError(t *testing.T) {
	if out, code := compileAndRunArm64(t, tryFromErrorSrc); code != 150 {
		t.Errorf("exit = %d, want 150\n%s", code, out)
	}
}

func TestWASMTryFromError(t *testing.T) {
	if code := runWasm(t, tryFromErrorSrc); code != 150 {
		t.Errorf("wasm exit = %d, want 150", code)
	}
}
