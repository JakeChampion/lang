package e2e

import (
	"os"
	"testing"
)

// Two `string[]` locals that alias one buffer, both reclaimable, one appended
// to. The self-append MOVE form routes to `__fern_arr_push_grow`, whose copy
// path memcpy's the element pointers with NO retain — sound only when the old
// buffer dies right after (the assign's buffer-only `__fern_arr_dec`). When
// the copy is taken because the buffer is SHARED (rc != 1) the old buffer
// survives, the two buffers share every element under a single count, and the
// two walk-drops (`__fern_drop_arr_str`) dec each element twice. The caller's
// strings are freed while it still holds them.
//
// This is the mechanism behind the `rhsTainted` `__method_Array_push`
// receiver-only arm's self-host corruption (#3457): the arm makes both halves
// of exactly this pair — `gfns` / `lgfns` in `irlower.lift_lambdas_view` —
// reclaimable, and the monomorphised clone names (the only function names with
// no second owner) hit rc 0 and are recycled, so `emit_function` writes
// `__fn_` + garbage. See docs/SELFHOST-AST-RETIREMENT.md.
//
// On main the appended-to local is taint-excluded from freeEligible, so only
// one walk-drop runs and the probe is correct — this pins that it STAYS
// correct when the eligibility rules widen (the shape is unreachable today,
// which is why it takes a probe rather than a failing test to hold the line).
// Names must exceed the SSO inline-string threshold: a short name has no heap
// block and so no refcount to over-release.
func TestAliasGrowNoOverRelease(t *testing.T) {
	srcBytes, err := os.ReadFile(langSrcAbs(t, "examples/probes/alias_grow_uaf.fern"))
	if err != nil {
		t.Fatalf("read probe: %v", err)
	}
	src := string(srcBytes)

	interpBin := buildLangBinForInterp(t)
	if want := interpExit(t, interpBin, src); want != 0 {
		t.Fatalf("interpreter oracle exits %d, want 0 — fixture drift", want)
	}

	t.Run("x86_64", func(t *testing.T) {
		_, code := compileAndRunX86_64(t, src)
		if code != 0 {
			t.Errorf("exit %d, want 0: a name was reclaimed under its owner (shared-buffer grow copied its elements without a retain)", code)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		_, code := compileAndRunArm64(t, src)
		if code != 0 {
			t.Errorf("exit %d, want 0: a name was reclaimed under its owner (shared-buffer grow copied its elements without a retain)", code)
		}
	})
}
