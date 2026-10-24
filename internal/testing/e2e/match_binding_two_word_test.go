package e2e

import "testing"

const matchBindingTwoWordSrc = `
struct SN { text: string }
struct ST { elements: i32[] }
type E = SN | ST;

function eval(e: E): i32 {
    match (e) {
        SN(n) => {
            let t: string = n.text;
            return t.len();
        },
        ST(t) => {
            let s: i32 = 0;
            let i: i32 = 0;
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
    let e: E = ST { elements: [40, 2] };
    return eval(e);
}`

func TestMatchBindingCrossShapeVarCollisionArm64(t *testing.T) {
	_, code := compileAndRunArm64(t, matchBindingTwoWordSrc)
	if code != 42 {
		t.Errorf("exit code = %d, want 42 (134 = the two-word slot-reuse bounds trap)", code)
	}
}

func TestMatchBindingCrossShapeVarCollisionX86_64(t *testing.T) {
	_, code := compileAndRunX86_64(t, matchBindingTwoWordSrc)
	if code != 42 {
		t.Errorf("exit code = %d, want 42", code)
	}
}
