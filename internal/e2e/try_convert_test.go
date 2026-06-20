package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// std/convert TryFrom / TryInto (#2710): the fallible siblings of From / Into.
// `TryFrom[T]` supplies a `try_from(T): Result[Self, string]` associated
// function (a checked constructor); `TryInto[T]` a `try_into(): Result[T,
// string]` method. They're ordinary traits (plain assoc-fn / method dispatch,
// no compiler magic), so a user impl + call resolves like any other.
//
// Small.try_from(42) => Ok(Small{42}); Small.try_from(300) => Err("too big")
// (len 7); Celsius{100}.try_into() delegates to Small.try_from => Ok(Small{100}).
// 42 + 7 + 100 = 149.
const tryConvertSrc = `import "std/convert" as convert;
struct Small { v: i32 }
impl convert.TryFrom[i32] for Small {
    function try_from(v: i32): Result[Small, string] {
        if (v < 0) { return Err("negative"); }
        if (v > 255) { return Err("too big"); }
        return Ok(Small { v: v });
    }
}
struct Celsius { deg: i32 }
impl convert.TryInto[Small] for Celsius {
    function try_into(self: Self): Result[Small, string] {
        return Small.try_from(self.deg);
    }
}
function main(): i32 {
    var a: i32 = match (Small.try_from(42)) { Ok(s) => s.v, Err(m) => 0 };
    var b: i32 = match (Small.try_from(300)) { Ok(s) => 0, Err(m) => m.len() };
    var c: Celsius = Celsius { deg: 100 };
    var d: i32 = match (c.try_into()) { Ok(s) => s.v, Err(m) => 0 };
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
