package ir

import (
	"strings"
	"testing"
)

// A cell over a composite element co-owns it the way a cell over a string
// does: `get` retains what it reads, `set` releases the value it replaces
// with that value's own deep drop, and the cell's last reference drops the
// slot through the deep drop an array of the element takes, since a cell is
// a one-element array box. The flat helpers a scalar cell uses would free the
// box and strand everything the element holds.
func TestCompositeCellOwnsItsElement(t *testing.T) {
	cases := []struct {
		name, src   string
		release     string // the old value's drop in set
		cellDropPfx string // the cell's own drop
	}{
		{
			"struct",
			`struct S { n: i32, name: string }
function f(): i32 {
  let c: Cell[S] = cell_new(S { n: 1, name: "a" });
  c.set(S { n: c.get().n + 1, name: "b" });
  return c.get().n;
}`,
			"__drop_struct_S", "__drop_arr_struct_S",
		},
		{
			"string-array",
			`function f(): i32 {
  let c: Cell[string[]] = cell_new([]);
  c.set(c.get().append("x"));
  return c.get().len();
}`,
			"__fern_drop_arr_str", "__drop_arr_arr_str",
		},
		{
			"enum",
			`enum T { Leaf(string), Node(T[]) }
function f(): i32 {
  let c: Cell[T] = cell_new(Leaf("a"));
  c.set(Node([c.get()]));
  return 0;
}`,
			"__drop_enum_T", "__drop_arr_",
		},
	}
	for _, tc := range cases {
		for _, ptrW := range []int{4, 8} {
			p := lowerSourceWith(t, tc.src, ptrW)
			ops := funcOpsOf(p, "f")
			retain, release, cellDrop := -1, -1, -1
			for i, op := range ops {
				switch {
				case op.Kind == OpRcInc && retain < 0:
					retain = i
				case op.Str == tc.release && release < 0:
					release = i
				case strings.HasPrefix(op.Str, tc.cellDropPfx) && op.Str != tc.release:
					cellDrop = i
				}
			}
			if retain < 0 || release < 0 || cellDrop < 0 {
				t.Errorf("%s ptrW=%d: retain=%d release(%s)=%d cell drop(%s*)=%d\n%s",
					tc.name, ptrW, retain, tc.release, release, tc.cellDropPfx, cellDrop, p)
			}
		}
	}
}
