package e2e

import (
	"testing"
)

func reassignReplacedFieldSrc(n string) string {
	return `struct Box { data: i32[], tag: i32 }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    let sum: i32 = 0;
    let b: Box = Box { data: [0], tag: 0 };
    while (i < ` + n + `) {
        b = Box { data: [i, i + 1, i + 2], tag: i };
        sum = sum + b.data[0] + b.tag;
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const reassignCarryOverSrc = `struct Box { data: i32[], tag: i32 }
function main(): i32 {
    let b: Box = Box { data: [10, 20, 30], tag: 0 };
    let i: i32 = 0;
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
