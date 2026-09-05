// Length-ceiling contract (#8457): a string byte count and an array element
// count both live in a 4-byte signed prefix, so a construction whose length
// does not fit is refused rather than wrapped. Before this the wrapped value
// reached the allocator: `"x".repeat(n)` computed `sl * n` in i32, handed the
// negative product to __alloc_u8, and the zero-fill then ran on the unwrapped
// count — an under-allocation followed by a ~4 GiB write, silent corruption on
// the natives' 16 GiB arena and an out-of-bounds trap on wasm32.
//
// The repro costs nothing to run: the wrap happens at the allocation, before
// any of the bytes it under-counted are touched.
package e2e

import (
	"strings"
	"testing"

	e2eharness "github.com/jakechampion/lang/internal/e2eharness"
)

// repeatPastCeilingSrc multiplies a 16-byte string by 268435455 copies:
// 16 * 268435455 == 4294967280, which is -16 as i32. `repeat` guards `n <= 0`
// and an empty receiver, so nothing upstream of the multiply catches it.
const repeatPastCeilingSrc = `import "std/string";
function main(): i32 {
	var s: string = "0123456789abcdef";
	var big: string = s.repeat(268435455);
	return big.len();
}`

// arrLenOverflowMsg is the cause line the natives write before exiting. One
// wording covers every backend, so a repeat/concat overflow and a bare
// oversized allocation are told apart by the program, not by the message.
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
		// wasm32 has a 32-bit address space, so there is no wider index type
		// to widen the product into: the backend checks the length instead and
		// traps. A trap carries no cause line, so the exit status is the whole
		// assertion here.
		comp := buildNumComponent(t, repeatPastCeilingSrc)
		stdout, _, code := runComponent(t, comp, runOpts{})
		if code == 0 {
			t.Fatalf("wasm did not trap on a length past the i32 ceiling (exit 0, stdout=%q)", stdout)
		}
		if trimOut(stdout) != "" {
			t.Errorf("wasm printed %q before trapping", stdout)
		}
	})
}

// TestAllocU8NegativeLengthAborts pins the guard at the allocator itself, so a
// caller other than `repeat` that overflows its own length arithmetic cannot
// reach the under-allocation either.
func TestAllocU8NegativeLengthAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs on two backends; not a -short test")
	}
	// 16 * 268435455 wraps to -16, the same product `repeat` produces, written
	// so the constant folder cannot see it as a literal.
	src := `function main(): i32 {
	var width: i32 = 16;
	var count: i32 = 268435455;
	var bs: u8[] = __alloc_u8(width * count);
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

// assertLenOverflowAbort checks the native abort contract: exit 134 (the status
// every other representational abort uses) with the cause named on stderr, and
// no program value printed first.
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

// TestLengthOverflowDiagnosticsAgreeAcrossNatives keeps the two backends' cause
// lines identical. They are duplicated constants (the codegen packages are
// deliberately independent), so nothing else would notice one drifting — the
// program still aborts, it just names its cause differently depending on which
// native built it.
// concatPastCeilingSrc builds one ~1.07 GiB string and concatenates it with
// itself: 2147483680 bytes total, 33 past the i32 ceiling. `repeat` cannot
// build the operand (it aborts on its own product, and its byte-at-a-time fill
// would take hours at this size), so the string comes from a zero-filled
// buffer.
const concatPastCeilingSrc = `function main(): i32 {
	var n: i32 = 1073741840;
	var bs: u8[] = __alloc_u8(n);
	var a: string = string_from_bytes_unchecked(bs);
	var b: string = a + a;
	return b.len();
}`

// strLenOverflowMsg is the cause line __fern_strcat writes. It is the same
// wording as an oversized allocation: the guard is the same refusal, and the
// program under test is what says which path reached it.
const strLenOverflowMsg = "fern: allocation size out of range"

// TestConcatPastLengthCeilingAborts is the second half of #8457: two operands
// whose byte counts sum past 2^31. The total used to be summed with a 32-bit
// `add`, wrapping negative, which fails the signed compare that picks the
// inline output path — so a 1 GiB memcpy ran into an 8-byte stack scratch
// buffer and the process died on SIGSEGV inside the copy.
//
// x86-64 only. The operand is really allocated and zero-filled, so the run
// peaks at 2 GiB of RSS for ~7s; the arm64 helper's zero-fill is a byte loop,
// which makes the same program hours under qemu-aarch64. Both natives' guards
// are pinned on the emitted text by internal/e2e/alloc_size_overflow_test.go.
func TestConcatPastLengthCeilingAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 2 GiB; not a -short test")
	}
	// The program's own RSS peak has to be budgeted against concurrent heavy
	// driver builds, or the two together can OOM the host.
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
