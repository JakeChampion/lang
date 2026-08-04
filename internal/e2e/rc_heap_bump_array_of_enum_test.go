package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Array-of-enum (`E[]`) element reclamation (RC-Perceus). Arrays of
// variant values (e.g. `Value[]`, pervasive in the self-host compiler)
// flat-rc_dec'd each element under __fern_drop_arr_ptr, freeing the enum
// box but leaking its rc-tracked payloads (profiling probe: 4864 B →
// 480064 B). arrElemStructDropName now routes a CONCRETE droppable enum
// element through a generated __drop_arr_enum_<Name> loop whose per-element
// call is the tag-dispatched __drop_enum_<Name> (reclaims box + payloads,
// is_unique-gated), then frees the buffer.
//
// Safety vs. the deferred enum-reuse hazard: enum construction doesn't inc
// payloads, so a payload SHARED with a local would be a double-free risk —
// but a payload built from a local taints that local (it escapes via the
// variant constructor), so the local's OWN drop is skipped (no
// double-free), and the array's deep-drop only fires at loop-reinit /
// function-exit, after the shared local's last use. The
// reassignment-overwrite path keeps the flat arr_dec (no deep drop), so no
// deep-drop point precedes a use. The shared-payload test + the
// self-host VM suite (heavy Value[] user) are the backstops.

func arrOfEnumBumpSrc(n string) string {
	return `enum E { Arr(i32[]), Empty }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var xs: E[] = [Arr([i, i + 1, i + 2]), Arr([i + 3])];
        match (xs[0]) {
            Arr(a) => { acc = acc + a[0]; },
            Empty => {},
        }
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// arrOfEnumUnderflowSrc shares a payload with a live local `a` (the
// adversarial double-free / UAF shape) and uses it after building the
// array — returns 0 iff value-correct AND no over-release.
const arrOfEnumUnderflowSrc = `enum E { Arr(i32[]), Empty }
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var a: i32[] = [i, i + 1, i + 2];
        var xs: E[] = [Arr(a), Empty];
        acc = acc + a[0] + a[2];
        i = i + 1;
    }
    // sum over i of (i + (i+2)) = 2i+2, i=0..199 => 2*19900 + 400 = 40200
    if (acc != 40200) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64ArrayOfEnumReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, arrOfEnumBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, arrOfEnumBumpSrc("5000"))
	if small != large {
		t.Errorf("E[] bump should be bounded (element reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, arrOfEnumUnderflowSrc); code != 0 {
		t.Errorf("E[] reclaim: code=%d (999=value/UAF, >0=over-release)", code)
	}
}

func TestArm64ArrayOfEnumReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, arrOfEnumBumpSrc("50"))
	large := mustRunArm64FreeOn(t, arrOfEnumBumpSrc("5000"))
	if small != large {
		t.Errorf("E[] bump should be bounded (element reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, arrOfEnumUnderflowSrc); code != 0 {
		t.Errorf("E[] reclaim: code=%d", code)
	}
}

func TestWASMArrayOfEnumReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, arrOfEnumBumpSrc("50"))
	large := runWasm(t, arrOfEnumBumpSrc("5000"))
	if small != large {
		t.Errorf("E[] bump should be bounded (element reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, arrOfEnumUnderflowSrc); got != 0 {
		t.Errorf("E[] reclaim: got %d", got)
	}
}
