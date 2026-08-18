package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Destructure-binding reclamation (RC-Perceus) — the follow-up to tuple
// reclamation (rc_heap_bump_tuple_test.go). A `var (a, b) = p` inside a
// loop reuses the synthetic destructure temp slot AND each binding slot
// across iterations. Before this slice neither got a per-iteration
// dec-on-reinit, so every iteration but the last leaked the tuple box
// and each rc-tracked element (the exit sweep only reclaims the final
// iteration). With the fix the destructure lowering routes the temp and
// each binding through emitVarReinitDropOld before the re-store, so the
// bump high-water stays FLAT regardless of iteration count.
//
// Two programs differing only in iteration count must report the SAME
// bump growth; a regression would make the larger run grow with N.

// destructureBumpGrowthSrc re-declares + destructures a (i32[], i32)
// tuple each iteration — exercises both the temp's deep-drop (frees the
// box + dec's the array element) and the array binding's per-iteration
// __fern_arr_dec.
func destructureBumpGrowthSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var p: (i32[], i32) = ([i, i + 1, i + 2], i);
        var (a, b) = p;
        sum = sum + a[0] + b;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// destructureUnderflowSrc destructures across many iterations and
// returns the over-release counter — must be 0 (no double-free of a
// box/element shared between the temp's deep-drop and a binding).
const destructureUnderflowSrc = `function main(): i32 {
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < 200) {
        var p: (i32[], i32) = ([i, i + 1, i + 2], i);
        var (a, b) = p;
        sum = sum + a[0] + b;
        i = i + 1;
    }
    return __rc_underflow_count();
}`

func TestX86_64DestructureHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, destructureBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, destructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leak would grow with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunX86_64FreeOn(t, destructureUnderflowSrc); code != 0 {
		t.Errorf("destructure over-releases = %d, want 0", code)
	}
}

func TestArm64DestructureHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, destructureBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, destructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunArm64FreeOn(t, destructureUnderflowSrc); code != 0 {
		t.Errorf("destructure over-releases = %d, want 0", code)
	}
}

func TestWASMDestructureHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, destructureBumpGrowthSrc("50"))
	large := runWasm(t, destructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, destructureUnderflowSrc); got != 0 {
		t.Errorf("destructure over-releases = %d, want 0", got)
	}
}

// nestedDestructureBumpGrowthSrc is the same shape one level deeper: the INNER
// tuple is its own box, so it needs its own temp, its own alias-inc and its own
// per-iteration reinit drop. A lowering that flattened the levels into extra
// offset hops off one temp would leak the inner box every iteration but the
// last, and the growth would scale with N.
func nestedDestructureBumpGrowthSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var p: (i32, (i32[], i32)) = (i, ([i, i + 1, i + 2], i));
        var (x, (a, b)) = p;
        sum = sum + x + a[0] + b;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// nestedDestructureUnderflowSrc must report 0 over-releases: the inner box is
// reachable from both the outer temp's deep-drop and the inner temp's, so a
// missing alias-inc on the inner level would double-free it.
const nestedDestructureUnderflowSrc = `function main(): i32 {
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < 200) {
        var p: (i32, (i32[], i32)) = (i, ([i, i + 1, i + 2], i));
        var (x, (a, b)) = p;
        sum = sum + x + a[0] + b;
        i = i + 1;
    }
    return __rc_underflow_count();
}`

func TestX86_64NestedDestructureHeapBumpBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, nestedDestructureBumpGrowthSrc("50"))
	large := mustRunX86_64FreeOn(t, nestedDestructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("nested destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d (a leaked inner box would grow with N)", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunX86_64FreeOn(t, nestedDestructureUnderflowSrc); code != 0 {
		t.Errorf("nested destructure over-releases = %d, want 0", code)
	}
}

func TestArm64NestedDestructureHeapBumpBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, nestedDestructureBumpGrowthSrc("50"))
	large := mustRunArm64FreeOn(t, nestedDestructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("nested destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunArm64FreeOn(t, nestedDestructureUnderflowSrc); code != 0 {
		t.Errorf("nested destructure over-releases = %d, want 0", code)
	}
}

func TestWASMNestedDestructureHeapBumpBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, nestedDestructureBumpGrowthSrc("50"))
	large := runWasm(t, nestedDestructureBumpGrowthSrc("5000"))
	if small != large {
		t.Errorf("nested destructure bump growth should be bounded (reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, nestedDestructureUnderflowSrc); got != 0 {
		t.Errorf("nested destructure over-releases = %d, want 0", got)
	}
}
