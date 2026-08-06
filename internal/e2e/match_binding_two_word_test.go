package e2e

import "testing"

// Cross-shape name collision between a `var` and a sibling match-arm
// binding, under the two-word string ABI (arm64's TwoWordOverride;
// wasm shares the lowering at ptrW==4).
//
// `eval` declares `var t: string` in one arm while a sibling arm binds
// its pointer-shaped payload as `t`. ir.bindingSlotScoped's shape guard used
// to read the var slot's type from scratchType (never stamped for
// info.Locals slots → nil → "single-word"), so the binding REUSED the
// string's two-word slot: the backend fanned every load/store of the
// binding into two words while the IR balanced the operand stack for
// one, desynchronising the stack. Observed as the self-host interp's
// `parser.ExprTuple(t)` arm trapping its own bounds check (exit 134)
// on arm64 after #4497 added `var t: string` to a sibling arm
// (TestSelfHostInterpArm64). The binding must take a fresh slot; the
// loop below then reads a sane length and the program returns 42.
//
// The unit-level pin lives at
// ir.TestMatchBindingCrossShapeVarCollisionGetsFreshSlot; this is the
// end-to-end backend guard.
const matchBindingTwoWordSrc = `
struct SN { text: string }
struct ST { elements: i32[] }
type E = SN | ST;

function eval(e: E): i32 {
    match (e) {
        SN(n) => {
            var t: string = n.text;
            return t.len();
        },
        ST(t) => {
            var s: i32 = 0;
            var i: i32 = 0;
            while (i < t.elements.len()) {
                s = s + t.elements[i];
                i = i + 1;
            }
            return s;
        }
    }
    return 0 - 1;
}
function main(): i32 {
    var e: E = ST { elements: [40, 2] };
    return eval(e);
}`

func TestMatchBindingCrossShapeVarCollisionArm64(t *testing.T) {
	_, code := compileAndRunArm64(t, matchBindingTwoWordSrc)
	if code != 42 {
		t.Errorf("exit code = %d, want 42 (134 = the two-word slot-reuse bounds trap)", code)
	}
}

func TestMatchBindingCrossShapeVarCollisionX86_64(t *testing.T) {
	// Single-word strings on x86-64 — the collision is same-shape there
	// and the slot is legitimately shared; this pins the baseline.
	_, code := compileAndRunX86_64(t, matchBindingTwoWordSrc)
	if code != 42 {
		t.Errorf("exit code = %d, want 42", code)
	}
}
