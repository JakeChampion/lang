package e2e

import (
	"os"
	"testing"
)

// TestRCNestedArrayAliasingBug pins a known reference-counting codegen
// bug: an aliasing-built nested-array structure (a ChunkDef[] whose
// .body is a BodyLine[] of structs with string fields) is consumed by
// an expand-style function and its inner strings are freed too early —
// a use-after-free that aborts the native + wasm backends while the
// interpreter is correct.
//
// The reproducer (testdata/rc_nested_array_aliasing_bug.fern, with the
// full bisection write-up) tangles a one-line root chunk, so the
// correct exit code is len("fn main() {}") == 12. Today:
//
//	-interp        → 12   (correct)
//	-target x86-64 → 255  (abort)
//	-target arm64  → 137  (SIGKILL)
//	-target wasm   → trap
//
// The test is SKIPPED until the bug is fixed. It is written as a real
// gate: drop the t.Skip and it asserts native parity with the
// interpreter (exit 12) on both native backends. This is the deferred
// "recursive inner deep-drop / string-concat under-reclaim" case from
// PR #1771's remaining-work list — fixing it should make this pass.
//
// Until then, code that hits this shape must use a flat data model (see
// examples/self_host/literate.fern, which avoids the nested arrays for
// exactly this reason).
func TestRCNestedArrayAliasingBug(t *testing.T) {
	t.Skip("known RC use-after-free on aliasing-built nested string arrays (native/wasm abort; interp correct) — remove this Skip when fixed")

	src, err := os.ReadFile("testdata/rc_nested_array_aliasing_bug.fern")
	if err != nil {
		t.Fatalf("read reproducer: %v", err)
	}
	const want = 12 // len("fn main() {}")

	if _, code := compileAndRunX86_64(t, string(src)); code != want {
		t.Errorf("x86-64 exit = %d, want %d (interp result) — RC bug still present", code, want)
	}
	if _, code := compileAndRunArm64(t, string(src)); code != want {
		t.Errorf("arm64 exit = %d, want %d (interp result) — RC bug still present", code, want)
	}
}
