package e2e

import (
	"strings"
	"testing"
)

// Two string-construction sites compute a byte count in i32 and hand the
// WRAPPED result to the allocator, then copy with the unwrapped one (#8457).
// The interpreter has rejected a negative `__alloc_u8` length since it was
// written and wasm traps; the natives silently continued:
//
//	                        repeat past ceiling      concat past ceiling
//	interp                  error, exit 1            —
//	wasm32-wasi             trap, exit 134           —
//	x86-64-linux            SIGSEGV                  SIGSEGV
//	arm64-linux             exit 0, len -1894967296  SIGSEGV
//
// arm64's `repeat` case is the one to keep in mind: it did not crash. It
// handed the program a string reporting a negative length and exited 0.
//
// Both are now a named abort — `fern: allocation size out of range`, exit
// 134 — which is what every other size failure in the runtime does.
//
// Neither case needs the multi-GB allocation the report assumed. `repeat`
// trips on the wrapped length before anything is copied, and the concat case
// reaches 2 GiB by DOUBLING (each step a memcpy, not a byte loop), so both
// run in seconds.
func TestAllocSizeOverflowAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping allocation-size e2e in -short mode")
	}
	t.Run("repeat_product_wraps_negative", func(t *testing.T) {
		// 8 x 300000000 = 2.4e9, which wraps to -1894967296 in i32.
		assertAbortsWithCause(t, `import "std/string";
function main(): i32 {
  var s: string = "abcdefgh";
  var r: string = s.repeat(300000000);
  return r.len();
}
`, "allocation size out of range")
	})
	t.Run("concat_combined_length_wraps_negative", func(t *testing.T) {
		// 2^30 + 2^30 = 2^31, which wraps to i32 MIN. A negative total is
		// not greater than the inline cap, so the pre-fix code took the
		// INLINE path and memcpy'd a gigabyte into an 8-byte stack buffer.
		assertAbortsWithCause(t, `import "std/string";
function main(): i32 {
  var a: string = "x";
  var i: i32 = 0;
  while (i < 30) { a = a + a; i = i + 1; }
  var b: string = a + a;
  return b.len();
}
`, "allocation size out of range")
	})
}

// A size that wraps to a small non-negative value is NOT caught here — the
// allocation succeeds at the wrapped size and the copy is then stopped by the
// element bounds check instead. Pinned so the two outcomes stay distinct: this
// one is safe, and its message names the check that actually caught it.
func TestAllocSizeWrappingToZeroStillAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping allocation-size e2e in -short mode")
	}
	// 4 x 2^30 = 2^32, which wraps to exactly 0.
	assertAbortsWithCause(t, `import "std/string";
function main(): i32 {
  var s: string = "abcd";
  var r: string = s.repeat(1073741824);
  return r.len();
}
`, "array index out of range")
}

// A large-but-representable allocation still succeeds — the guard rejects an
// overflowed size, not a big one.
func TestLargeButRepresentableAllocationStillWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping allocation-size e2e in -short mode")
	}
	src := `import "std/string";
function main(): i32 {
  var a: string = "x";
  var i: i32 = 0;
  while (i < 20) { a = a + a; i = i + 1; }
  if (a.len() != 1048576) { return 7; }
  return 0;
}
`
	t.Run("x86_64", func(t *testing.T) {
		if out, code := compileAndRunX86_64(t, src); code != 0 {
			t.Errorf("x86_64 exited %d, want 0; out=%q", code, out)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if out, code := compileAndRunArm64(t, src); code != 0 {
			t.Errorf("arm64 exited %d, want 0; out=%q", code, out)
		}
	})
}

// assertAbortsWithCause is assertAborts plus the cause line: an abort with the
// wrong message means a different check fired, which for a memory-safety guard
// is the difference between catching the overflow and tripping over its
// consequences later.
func assertAbortsWithCause(t *testing.T, src, cause string) {
	t.Helper()
	t.Run("x86_64", func(t *testing.T) {
		out, code := compileAndRunX86_64(t, src)
		checkAbort(t, "x86_64", out, code, cause, src)
	})
	t.Run("arm64-linux", func(t *testing.T) {
		out, code := compileAndRunArm64(t, src)
		checkAbort(t, "arm64", out, code, cause, src)
	})
}

func checkAbort(t *testing.T, backend, out string, code int, cause, src string) {
	t.Helper()
	if code != 134 {
		t.Errorf("%s exited %d, want 134; out=%q\nsrc:\n%s", backend, code, out, src)
	}
	if !strings.Contains(out, cause) {
		t.Errorf("%s aborted without the %q cause line; out=%q\nsrc:\n%s", backend, cause, out, src)
	}
	if trimOut(withoutAbortDiag(out)) != "" {
		t.Errorf("%s printed %q before aborting\nsrc:\n%s", backend, out, src)
	}
}
