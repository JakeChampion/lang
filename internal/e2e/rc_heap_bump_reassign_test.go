package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Struct reassignment-overwrite deep reclamation (RC-Perceus). A
// self-overwrite of an owned struct local with a same-type struct
// literal (`b = Box{ data: [...], tag: i }`) reuses the box in place
// (tryStructReuseOverwrite), but before this slice it released the OLD
// rc-tracked fields with a flat __fern_rc_dec — which doesn't free an
// array field's buffer (rc_dec has no free path). So a REPLACED array
// field leaked its buffer every iteration. The fix routes the old-field
// release through a per-field deep drop (emitFieldDropOnStack →
// __fern_arr_dec for arrays, __drop_* for nested), freeing the replaced
// buffer at rc 0 while the helper's own is_unique gate keeps a
// CARRIED-OVER field (e.g. `data: b.data`, eval-inc'd to rc>1) alive.

func reassignReplacedFieldSrc(n string) string {
	return `struct Box { data: i32[], tag: i32 }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    var b: Box = Box { data: [0], tag: 0 };
    while (i < ` + n + `) {
        b = Box { data: [i, i + 1, i + 2], tag: i };
        sum = sum + b.data[0] + b.tag;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// reassignCarryOverSrc keeps the array field across iterations
// (`data: b.data`); the carried-over buffer must NOT be freed (its
// eval-inc makes it rc>1 at the old-field release, so is_unique is
// false → dec only). Returns 0 iff value-correct AND no over-release.
const reassignCarryOverSrc = `struct Box { data: i32[], tag: i32 }
function main(): i32 {
    var b: Box = Box { data: [10, 20, 30], tag: 0 };
    var i: i32 = 0;
    while (i < 200) {
        b = Box { data: b.data, tag: b.tag + 1 };
        i = i + 1;
    }
    if (b.data[0] + b.data[1] + b.data[2] != 60) { return 999; }
    if (b.tag != 200) { return 888; }
    return __rc_underflow_count();
}`

func TestX86_64StructReassignReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, reassignReplacedFieldSrc("50"))
	large := mustRunX86_64FreeOn(t, reassignReplacedFieldSrc("5000"))
	if small != large {
		t.Errorf("replaced-field reassignment should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunX86_64FreeOn(t, reassignCarryOverSrc); code != 0 {
		t.Errorf("carry-over reassignment: code=%d (999/888=value mismatch, >0=over-release)", code)
	}
}

func TestArm64StructReassignReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, reassignReplacedFieldSrc("50"))
	large := mustRunArm64FreeOn(t, reassignReplacedFieldSrc("5000"))
	if small != large {
		t.Errorf("replaced-field reassignment should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if _, code := compileAndRunArm64FreeOn(t, reassignCarryOverSrc); code != 0 {
		t.Errorf("carry-over reassignment: code=%d", code)
	}
}

func TestWASMStructReassignReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, reassignReplacedFieldSrc("50"))
	large := runWasm(t, reassignReplacedFieldSrc("5000"))
	if small != large {
		t.Errorf("replaced-field reassignment should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, reassignCarryOverSrc); got != 0 {
		t.Errorf("carry-over reassignment: got %d", got)
	}
}
