package e2e

import (
	"testing"
)

// The drop-guided reuse shape: the donor is dropped INSIDE a dominated arm,
// so a reuse pair crosses the arm boundary. It must be value-correct on both
// the taken and not-taken paths, with no rc over-release.

// dgArmShapeRuntimeSrc exercises the drop-guided-only pairing at runtime:
// a's last use sits inside the if arm before b's construction (taken
// path reuses a's box; not-taken path leaves a to the exit sweep — the
// adversarial double-free alternation), with a pointer field so the old
// array is deep-freed on the reuse branch.
const dgArmShapeRuntimeSrc = `struct Holder { id: i32, items: i32[] }
function run(go_: boolean): i32 {
    let a: Holder = Holder { id: 1, items: [7, 8] };
    let acc: i32 = 0;
    if (go_) {
        let s: i32 = a.id + a.items[0] + a.items[1];
        let b: Holder = Holder { id: s, items: [3, 4] };
        acc = b.id + b.items[0] + b.items[1];
    }
    return acc;
}
function main(): i32 {
    let t: i32 = run(true);    // s=16; b={16,[3,4]} -> 23
    let f: i32 = run(false);   // a exit-swept
    if (t != 23) { return 1; }
    if (f != 0) { return 2; }
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let m: Holder = Holder { id: i, items: [i, i + 1] };
        if (i % 2 == 0) {
            let s: i32 = m.id + m.items[0] + m.items[1];
            let b: Holder = Holder { id: s, items: [i + 2, i + 3] };
            acc = acc + b.id + b.items[0] + b.items[1];
        }
        i = i + 1;
    }
    // even i: s = 3i+1; b.id + b.items = (3i+1) + (2i+5) = 5i+6
    // i=2j, j=0..99: 10j+6; sum = 49500 + 600 = 50100
    if (acc != 50100) { return 3; }
    return __rc_underflow_count();
}`

func TestX86_64DropGuidedArmShapeRuntime(t *testing.T) {
	if out, code := compileAndRunX86_64FreeOn(t, dgArmShapeRuntimeSrc); code != 0 {
		t.Errorf("arm-shape runtime: exit %d, want 0 (out %q)", code, out)
	}
}

func TestArm64DropGuidedArmShapeRuntime(t *testing.T) {
	if out, code := compileAndRunArm64FreeOn(t, dgArmShapeRuntimeSrc); code != 0 {
		t.Errorf("arm-shape runtime: exit %d, want 0 (out %q)", code, out)
	}
}

func TestWASMDropGuidedArmShapeRuntime(t *testing.T) {
	if got := runWasm(t, dgArmShapeRuntimeSrc); got != 0 {
		t.Errorf("arm-shape runtime (wasm): got %d, want 0", got)
	}
}
