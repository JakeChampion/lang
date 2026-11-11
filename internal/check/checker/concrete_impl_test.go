package checker

import "testing"

// An impl for one instance of a generic struct compares the method with the
// instance, which the parser spells as an enum type.
func TestImplForOneInstanceOfAGenericStruct(t *testing.T) {
	if msg := checkSrc(t, `trait T { function a(self: Self): i32; }
struct B[X] { x: X }
impl T for B[i32] { function a(self: B[i32]): i32 { return 1; } }
function main(): i32 { return 0; }`); msg != "" {
		t.Fatalf("want no diagnostic, got:\n%s", msg)
	}
}
