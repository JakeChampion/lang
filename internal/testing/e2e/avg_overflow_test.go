package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// avgI32OverflowProgram exercises the std/array `i32[].avg()` reduction
// (issue #2687). The three elements sum to 2.4e9, which overflows i32
// (> 2^31-1): the old `Some(sum() / n)` carried the running sum in i32, so it
// wrapped to a NEGATIVE value BEFORE the divide and `avg()` returned a wrong
// (negative) mean even though the true truncated mean (800000000) fits in i32.
// avg() now accumulates in i64, so the mean is exact. main returns 0 iff the
// mean is correct, so exit 0 means the pre-division overflow is fixed on every
// backend.
const avgI32OverflowProgram = `
import "std/array";
function main(): i32 {
    match ([800000000, 800000000, 800000000].avg()) {
        Some(m) => { if (m == 800000000) { return 0; } return 1; },
        None => { return 2; }
    }
}
`

func TestInterpAvgI32Overflow(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(avgI32OverflowProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (avg() overflowed before the divide)\nstderr: %s", code, errb.String())
	}
}

func TestX86_64AvgI32Overflow(t *testing.T) {
	if _, code := compileAndRunX86_64(t, avgI32OverflowProgram); code != 0 {
		t.Errorf("x86-64 i32[].avg() i64 accumulation: exit = %d, want 0", code)
	}
}

func TestArm64AvgI32Overflow(t *testing.T) {
	if _, code := compileAndRunArm64(t, avgI32OverflowProgram); code != 0 {
		t.Errorf("arm64 i32[].avg() i64 accumulation: exit = %d, want 0", code)
	}
}

func TestWASMAvgI32Overflow(t *testing.T) {
	if code := runWasm(t, avgI32OverflowProgram); code != 0 {
		t.Errorf("wasm i32[].avg() i64 accumulation: exit = %d, want 0", code)
	}
}
