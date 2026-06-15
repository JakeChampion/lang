package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Error-converting `?` (#3234): a `Result[_, E]` propagated through a
// function returning `Result[_, dyn Trait]` boxes the concrete error `E`
// into `dyn Trait` when E implements Trait (the `Box<dyn Error>` + `?`
// idiom). handler(true) → Ok(43); handler(false) → Err(dyn Error) whose
// .message() is "missing" (len 7). 43 + 7 = 50.
const tryDynErrorSrc = `trait Error { function message(self: Self): string; }
struct NotFound { what: string }
impl Error for NotFound { function message(self: Self): string { return self.what; } }
function find(ok: boolean): Result[i32, NotFound] {
    if (ok) { return Ok(42); }
    return Err(NotFound { what: "missing" });
}
function handler(ok: boolean): Result[i32, dyn Error] {
    var v: i32 = find(ok)?;
    return Ok(v + 1);
}
function main(): i32 {
    var a: i32 = match (handler(true)) { Ok(v) => v, Err(e) => 0 };
    var b: i32 = match (handler(false)) { Ok(v) => 0, Err(e) => e.message().len() };
    return a + b;
}
`

func TestInterpTryDynError(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(tryDynErrorSrc), 0o644); err != nil {
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

func TestX86_64TryDynError(t *testing.T) {
	if out, code := compileAndRunX86_64(t, tryDynErrorSrc); code != 50 {
		t.Errorf("exit = %d, want 50\n%s", code, out)
	}
}

func TestArm64TryDynError(t *testing.T) {
	if out, code := compileAndRunArm64(t, tryDynErrorSrc); code != 50 {
		t.Errorf("exit = %d, want 50\n%s", code, out)
	}
}

func TestWASMTryDynError(t *testing.T) {
	if code := runWasm(t, tryDynErrorSrc); code != 50 {
		t.Errorf("wasm exit = %d, want 50", code)
	}
}
