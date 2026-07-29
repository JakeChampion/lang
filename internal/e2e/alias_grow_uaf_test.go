package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
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
// FIXED by routing the self-append through `__fern_arr_push_grow_move_ptr` /
// `_move_str`, which retain the copied elements exactly when the incoming
// rc != 1 — precisely "the old buffer survives this grow". With that in place
// the receiver-only arm is sound and IS applied, so this probe now exercises a
// REACHABLE shape: reverting either the helper routing (`emitArrayPush`) or the
// helper bodies makes it exit 1.
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
	t.Run("wasm", func(t *testing.T) {
		// A regression guard, NOT proof the _move_str retain fires: this leg
		// also passes with that retain disabled, because the shape is inert on
		// wasm today — a two-word `string[]` self-append never reclaims its
		// old buffer there (the pre-existing gap recorded in
		// docs/SELFHOST-AST-RETIREMENT.md), so there is no second walk-drop to
		// over-release. The x86-64 leg is the one that flips.
		prev := ast.RcFreeEnabled
		ast.RcFreeEnabled = true
		defer func() { ast.RcFreeEnabled = prev }()
		if code := runWasm(t, src); code != 0 {
			t.Errorf("exit %d, want 0: a name was reclaimed under its owner (shared-buffer grow copied its elements without a retain)", code)
		}
	})
}
