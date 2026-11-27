package e2ecompiler

import "testing"

// A record built again on each pass appends to one field and then reads the
// length of that field for another. The two reads of the field merge into one
// value, and the length read then moves above the append, so the append
// keeps the array unique. Before, each append copied the array, and the scan
// of the accesses in compiler/ssabounds.fern took time in the square of their
// count.
const hoistMergedProg = `struct Seen { arr: i32[], head: i32[], n: i32 }

@noinline function grow(n: i32): Seen {
  let s = Seen { arr: [], head: [0, 0, 0, 0], n: 0 };
  for i in 0..n {
    s = Seen { arr: s.arr.append(i * 3), head: s.head.with(i % 4, s.arr.len()), n: s.n + 1 };
  }
  return s;
}

function main(): i32 {
  let before = __heap_alloc_count();
  let s = grow(1000);
  let n = __heap_alloc_count() - before;
  if (s.arr.len() != 1000 || s.arr[999] != 2997 || s.head[3] != 999 || s.head[0] != 996 || s.n != 1000) {
    return 1;
  }
  if (__rc_underflow_count() != 0) {
    return 99;
  }
  // The growth of the array and the first write of head, not one copy for each append.
  if (n > 40 as i64) {
    return 2;
  }
  return 0;
}
`

func TestSelfHostMergedReadMovesAboveItsAppend(t *testing.T) {
	runRereadProgram(t, "hoist_merged.fern", hoistMergedProg)
}
