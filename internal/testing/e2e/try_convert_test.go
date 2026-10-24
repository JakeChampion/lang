package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The conversion traits preserve a concrete error type with its original value.
// Success contributes 42 + 100. The rejected value contributes 7.
const tryConvertSrc = `import "std/convert" as convert;
import "std/error";
import "core/cmp";
struct RangeError { value: i32 }
impl error.Error for RangeError {}
impl cmp.Display for RangeError {}
function (e: RangeError) message(): string { return "out of range"; }
function (e: RangeError) to_string(): string { return e.message(); }
struct Small { v: i32 }
impl convert.TryFrom[i32, RangeError] for Small {
    function try_from(v: i32): Result[Small, RangeError] {
        if (v < 0) { return Err(RangeError { value: v }); }
        if (v > 255) { return Err(RangeError { value: v }); }
        return Ok(Small { v: v });
    }
}
struct Celsius { deg: i32 }
impl convert.TryInto[Small, RangeError] for Celsius {
    function try_into(self: Self): Result[Small, RangeError] {
        return Small.try_from(self.deg);
    }
}
function checked[T: convert.TryFrom[i32, RangeError]](witness: T, v: i32): Result[T, RangeError] {
    return T.try_from(v);
}
function error_text[E: error.Error + cmp.Display](e: E): string {
    if (e.message() != e.to_string()) { return "mismatch"; }
    return e.message();
}
function main(): i32 {
    let Err(problem) = Small.try_from(-7) else { return 0; };
    if (problem.value != -7 || error_text(problem) != "out of range") { return 0; }
    let a: i32 = match (checked(Small { v: 0 }, 42)) { Ok(s) => s.v, Err(m) => 0 };
    let b: i32 = match (Small.try_from(300)) { Ok(s) => 0, Err(e) => e.value - 293 };
    let c: Celsius = Celsius { deg: 100 };
    let d: i32 = match (c.try_into()) { Ok(s) => s.v, Err(m) => 0 };
    return a + b + d;
}
`

func TestInterpTryConvert(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(tryConvertSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 149 {
		t.Errorf("exit = %d, want 149\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64TryConvert(t *testing.T) {
	if out, code := compileAndRunX86_64(t, tryConvertSrc); code != 149 {
		t.Errorf("exit = %d, want 149\n%s", code, out)
	}
}

func TestArm64TryConvert(t *testing.T) {
	if out, code := compileAndRunArm64(t, tryConvertSrc); code != 149 {
		t.Errorf("exit = %d, want 149\n%s", code, out)
	}
}

func TestWASMTryConvert(t *testing.T) {
	if code := runWasm(t, tryConvertSrc); code != 149 {
		t.Errorf("wasm exit = %d, want 149", code)
	}
}
