package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// uniformEnumDropLoads' verdict is what lets the enum drop / reuse paths
// release a box's payloads with NO runtime tag switch. Its loads carry a
// concrete `typ`, and callers release through a TYPE-SPECIFIC helper — the
// generated `__drop_struct_<T>`, the per-element array walk — so the type has
// to hold for every box of the enum, not just the variant it was read from.
//
// The union shape is where that bites: `E { VA(A), VB(B) }` has one droppable
// pointer at one shared offset in both variants, so an offset-and-kind
// signature calls it uniform and hands out A's type for a box that may hold a
// B. It measured as one leaked buffer per replacement on the enum-reuse
// overwrite path (`__drop_struct_A` walking a B), and as a wrong-width read
// wherever the two structs' droppable fields do not line up.
func TestUniformEnumDropLoadsNeedsOneTypePerOffset(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{
			// Different concrete payload structs at the same offset: not
			// uniform, whatever their drop kinds.
			name: "union_of_distinct_structs",
			src: `struct A { s: i32[], n: i32 }
struct B { x: i32[], y: i32[] }
enum E { VA(A), VB(B) }
function main(): i32 { var e: E = VA(A { s: [1], n: 2 }); match (e) { VA(a) => { return a.n; }, VB(b) => { return b.y[0]; } } }`,
			want: false,
		},
		{
			// Same payload type in every payload-carrying variant: uniform,
			// and the load's type describes every box.
			name: "same_payload_type",
			src: `struct A { s: i32[], n: i32 }
enum E { VA(A), VB(A) }
function main(): i32 { var e: E = VA(A { s: [1], n: 2 }); match (e) { VA(a) => { return a.n; }, VB(b) => { return b.n; } } }`,
			want: true,
		},
		{
			// Arrays whose ELEMENT types differ walk different per-element
			// drops, so they are not uniform either.
			name: "arrays_of_different_elements",
			src: `struct A { n: i32 }
enum E { VA(A[]), VB(i32[][]) }
function main(): i32 { var e: E = VB([[1]]); match (e) { VA(a) => { return a[0].n; }, VB(b) => { return b[0][0]; } } }`,
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			ed, ok := info.Enums["E"]
			if !ok {
				t.Fatalf("enum E not in checker info")
			}
			_, got := uniformEnumDropLoads(ed, 8)
			if got != tc.want {
				t.Errorf("uniformEnumDropLoads = %v, want %v — a load's type is applied to every box of the enum, so one type per offset is the requirement", got, tc.want)
			}
		})
	}
}
