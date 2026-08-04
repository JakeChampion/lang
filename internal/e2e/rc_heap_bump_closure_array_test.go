package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// closure[] element reclamation (RC-Perceus). A `(() => i32)[]` array holds
// closure-PAIR pointers whose env blocks (the captures) leaked: an array
// element typed `(() => i32)` can't name WHICH closure it holds (distinct
// closures share a signature but have distinct capture layouts +
// per-closure __closure_drop_<name> thunks), so an array drop could only
// call the generic __fern_closure_drop per element — which by design freed
// only the closure PAIR block and leaked the env. The fix (option B from
// docs/RC-PERCEUS-PLAN.md) makes the closure box carry a drop-fn POINTER:
// the pair grows to {fn_ptr, env_ptr, drop_fn, env_ptr} and a generic
// __drop_arr_closure loop derefs the embedded drop-fn (via OpCallIndirect on
// the duplicated {drop_fn, env_ptr} sub-pair) to free each element's env
// generically, then frees the pair block + buffer.
//
// The wasm e2e is the over-release arbiter (natives' bump probe is
// insensitive to small-block reclaim — their segregated freelist arena
// isn't measured by __heap_bump_bytes, so the leak never showed there; the
// plan's "natives elide these closures" note was wrong, the IR is identical
// on all backends — see the plan's closure[] bullet). On wasm the leak was
// unbounded (3264 -> 320064 across 100x N); after the fix the high-water
// plateaus (freelist warmup, like rc string concat) and is flat across 10x
// N. The adversarial aliased / shared-closure cases pin 0 over-releases on
// every backend.

// scalarCapClosureArrSrc: a `(() => i32)[]` of scalar-capture closures.
func scalarCapClosureArrSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var a: i32 = i;
        var fs: (() => i32)[] = [function (): i32 { return a + 1; }, function (): i32 { return a + 2; }];
        acc = acc + fs[0]() + fs[1]();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// ptrCapClosureArrSrc: a `(() => i32)[]` whose closures each capture a
// POINTER (an i32[]) — the env's deep-drop (its __closure_drop_<name> thunk
// reclaims the captured array) is exercised through the drop-fn pointer.
func ptrCapClosureArrSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var xs: i32[] = [i, i + 1, i + 2];
        var fs: (() => i32)[] = [function (): i32 { return xs[0]; }, function (): i32 { return xs[2]; }];
        acc = acc + fs[0]() + fs[1]();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// aliasClosureArrSrc: the closure array is aliased (`var gs = fs` → rc>1), so
// the per-element env free must NOT fire while another holder is live.
// Returns 0 iff value-correct AND 0 over-releases.
const aliasClosureArrSrc = `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var a: i32 = i;
        var fs: (() => i32)[] = [function (): i32 { return a + 1; }, function (): i32 { return a + 2; }];
        var gs: (() => i32)[] = fs;
        acc = acc + fs[0]() + gs[1]();
        i = i + 1;
    }
    // each iter: (a+1)+(a+2) = 2i+3; sum i=0..199 = 2*19900 + 600 = 40400
    if (acc != 40400) { return 999; }
    return __rc_underflow_count();
}`

// sharedElemClosureArrSrc: the SAME closure value appears twice in the array
// (`[f, f]` → the pair's rc is 2) AND is still live as `f` after. The
// per-element is_unique gate must skip the env free until the genuine last
// reference. Returns 0 iff value-correct AND 0 over-releases.
const sharedElemClosureArrSrc = `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var xs: i32[] = [i, i + 1, i + 2];
        var f: (() => i32) = function (): i32 { return xs[1]; };
        var fs: (() => i32)[] = [f, f];
        acc = acc + fs[0]() + fs[1]() + f();
        i = i + 1;
    }
    // each iter: 3*(i+1); sum i=0..199 of 3i+3 = 3*19900 + 600 = 60300
    if (acc != 60300) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64ClosureArrayReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, scalarCapClosureArrSrc("5000"))
	large := mustRunX86_64FreeOn(t, scalarCapClosureArrSrc("50000"))
	if small != large {
		t.Errorf("closure[] bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	ps := mustRunX86_64FreeOn(t, ptrCapClosureArrSrc("5000"))
	pl := mustRunX86_64FreeOn(t, ptrCapClosureArrSrc("50000"))
	if ps != pl {
		t.Errorf("ptr-capture closure[] bump should be bounded: N=5000 -> %d, N=50000 -> %d", ps, pl)
	}
	if _, code := compileAndRunX86_64FreeOn(t, aliasClosureArrSrc); code != 0 {
		t.Errorf("aliased closure[]: code=%d (999=value/UAF, >0=over-release)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, sharedElemClosureArrSrc); code != 0 {
		t.Errorf("shared-element closure[]: code=%d", code)
	}
}

func TestArm64ClosureArrayReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, scalarCapClosureArrSrc("5000"))
	large := mustRunArm64FreeOn(t, scalarCapClosureArrSrc("50000"))
	if small != large {
		t.Errorf("closure[] bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	ps := mustRunArm64FreeOn(t, ptrCapClosureArrSrc("5000"))
	pl := mustRunArm64FreeOn(t, ptrCapClosureArrSrc("50000"))
	if ps != pl {
		t.Errorf("ptr-capture closure[] bump should be bounded: N=5000 -> %d, N=50000 -> %d", ps, pl)
	}
	if _, code := compileAndRunArm64FreeOn(t, aliasClosureArrSrc); code != 0 {
		t.Errorf("aliased closure[]: code=%d", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, sharedElemClosureArrSrc); code != 0 {
		t.Errorf("shared-element closure[]: code=%d", code)
	}
}

func TestWASMClosureArrayReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	// wasm is the over-release arbiter: it heap-allocates the pairs + envs
	// and __heap_bump_bytes measures the leak directly. Pre-fix this loop
	// ramped unbounded (320064 at N=5000); post-fix it plateaus (freelist
	// warmup) and is flat across 10x N.
	small := runWasm(t, scalarCapClosureArrSrc("5000"))
	large := runWasm(t, scalarCapClosureArrSrc("50000"))
	if small != large {
		t.Errorf("closure[] bump should be bounded (env reclaim): N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates closure pairs; expected a non-zero bounded high-water, got 0")
	}
	ps := runWasm(t, ptrCapClosureArrSrc("5000"))
	pl := runWasm(t, ptrCapClosureArrSrc("50000"))
	if ps != pl {
		t.Errorf("ptr-capture closure[] bump should be bounded: N=5000 -> %d, N=50000 -> %d", ps, pl)
	}
	if got := runWasm(t, aliasClosureArrSrc); got != 0 {
		t.Errorf("aliased closure[]: got %d (999=value/UAF, >0=over-release)", got)
	}
	if got := runWasm(t, sharedElemClosureArrSrc); got != 0 {
		t.Errorf("shared-element closure[]: got %d", got)
	}
}
