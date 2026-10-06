package ir

import "testing"

func TestByteCellOverwriteRetainsBeforeRelease(t *testing.T) {
	const src = `function f(): i32 {
  let c: Cell[u8[]] = cell_new([255 as u8]);
  c.set(c.get());
  return c.get().len();
}`
	for _, width := range []int{4, 8} {
		p := lowerSourceWith(t, src, width)
		ops := funcOpsOf(p, "f")
		retain, release, deep := -1, -1, false
		for i, op := range ops {
			if op.Kind == OpRcInc && retain < 0 {
				retain = i
			}
			if op.Str == "__fern_arr_dec" && release < 0 {
				release = i
			}
			if op.Str == "__drop_arr_arr_1" {
				deep = true
			}
		}
		if retain < 0 || release <= retain || !deep {
			t.Fatalf("width %d: retain=%d release=%d deep=%v\n%s", width, retain, release, deep, p)
		}
	}
}
