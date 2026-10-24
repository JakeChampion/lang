package e2e

import (
	"strings"
	"testing"

	e2eharness "github.com/jakechampion/lang/internal/testing/e2eharness"
)

const repeatPastCeilingSrc = `import "std/string";
function main(): i32 {
	let s: string = "0123456789abcdef";
	let big: string = s.repeat(268435455);
	return big.len();
}`

const arrLenOverflowMsg = "fern: allocation size out of range"

func TestRepeatPastLengthCeilingAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs on three backends; not a -short test")
	}
	t.Run("x86_64", func(t *testing.T) {
		out, code := compileAndRunX86_64(t, repeatPastCeilingSrc)
		assertLenOverflowAbort(t, "x86_64", out, code, arrLenOverflowMsg)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		out, code := compileAndRunArm64(t, repeatPastCeilingSrc)
		assertLenOverflowAbort(t, "arm64", out, code, arrLenOverflowMsg)
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		comp := buildCLIComponent(t, repeatPastCeilingSrc)
		stdout, _, code := runWasmArtifact(t, comp, runOpts{})
		if code == 0 {
			t.Fatalf("wasm did not trap on a length past the i32 ceiling (exit 0, stdout=%q)", stdout)
		}
		if trimOut(stdout) != "" {
			t.Errorf("wasm printed %q before trapping", stdout)
		}
	})
}

func TestAllocU8NegativeLengthAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs on two backends; not a -short test")
	}
	src := `function main(): i32 {
	let width: i32 = 16;
	let count: i32 = 268435455;
	let bs: u8[] = __alloc_u8(width * count);
	return bs.len();
}`
	t.Run("x86_64", func(t *testing.T) {
		out, code := compileAndRunX86_64(t, src)
		assertLenOverflowAbort(t, "x86_64", out, code, arrLenOverflowMsg)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		out, code := compileAndRunArm64(t, src)
		assertLenOverflowAbort(t, "arm64", out, code, arrLenOverflowMsg)
	})
}

func assertLenOverflowAbort(t *testing.T, backend, out string, code int, wantMsg string) {
	t.Helper()
	if code != 134 {
		t.Errorf("%s exit = %d, want 134; output:\n%s", backend, code, out)
	}
	if !strings.Contains(out, wantMsg) {
		t.Errorf("%s did not name the cause (want %q); output:\n%s", backend, wantMsg, out)
	}
	if trimOut(withoutAbortDiag(out)) != "" {
		t.Errorf("%s printed %q before aborting", backend, out)
	}
}

const concatPastCeilingSrc = `function main(): i32 {
	let n: i32 = 1073741840;
	let bs: u8[] = __alloc_u8(n);
	let a: string = string_from_bytes_unchecked(bs);
	let b: string = a + a;
	return b.len();
}`

const strLenOverflowMsg = "fern: allocation size out of range"

func TestConcatPastLengthCeilingAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 2 GiB; not a -short test")
	}
	var out string
	var code int
	if err := e2eharness.WithBuildMemoryMB(2400, func() error {
		out, code = compileAndRunX86_64(t, concatPastCeilingSrc)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertLenOverflowAbort(t, "x86_64", out, code, strLenOverflowMsg)
}
