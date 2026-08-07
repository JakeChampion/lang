package ir_test

import (
	"testing"
)

// `t.1.a` — a field access whose TARGET is a tuple element selector — resolved
// its owning struct through the struct-field path, which has no entry for a
// numeric selector. The owner came back as the empty name and lowering aborted
// with `field access on unresolved struct ""`, on code the checker accepts and
// the interpreter runs correctly.
//
// The conformance case (tuple_element_field_access) covers the values on every
// backend. These cover the resolution itself: each nesting the owner lookup
// recurses through, so a fix that handled one level would be caught.

func lowerOK(t *testing.T, src string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("lowering panicked: %v", r)
		}
	}()
	lowerForTest(t, src)
}

const tupleFieldDecls = `struct Point { x: i32, y: i32 }
struct Segment { from: Point, to: Point }
function origin(): (i32, Point) { return (1, Point { x: 7, y: 9 }); }
function span(): (i32, Segment) {
    return (2, Segment { from: Point { x: 0, y: 0 }, to: Point { x: 3, y: 4 } });
}
`

func TestTupleElementFieldAccessLowers(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"one level", `var t: (i32, Point) = origin(); return t.1.x;`},
		{"two levels", `var s: (i32, Segment) = span(); return s.1.to.x;`},
		{"off the call result", `return origin().1.y;`},
		{"two levels off the call result", `return span().1.from.y;`},
		{"through an array element", `var ps: (i32, Point)[] = [origin()]; return ps[0].1.x;`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lowerOK(t, tupleFieldDecls+"\nfunction main(): i32 { "+tc.body+" }\n")
		})
	}
}

// The new tuple branch answers for EVERY tuple-element target, including the
// ones that are not structs, so it has to decline them rather than claim an
// owner. An array element reached through a tuple already worked and must keep
// working — it is the path the declining branch now sits in front of.
func TestTupleElementNonStructTargetStillLowers(t *testing.T) {
	lowerOK(t, `
function mk(): (i32, i32[]) { return (1, [4, 5, 6]); }

function main(): i32 {
    var t: (i32, i32[]) = mk();
    return t.1.len() + t.1[2];
}
`)
}
