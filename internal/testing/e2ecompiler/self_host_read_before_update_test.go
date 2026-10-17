package e2ecompiler

import "testing"

// readBeforeUpdateProgram reads an array after the update that consumes it:
// `chain` reads the element beside the one the first `with` wrote, `ends`
// reads the length and an element, and `after` reads the updated array,
// which has to stay behind the update. Moved ahead of the update (semhoist),
// the reads leave the array unique, so 100 calls of each of the first two
// allocate nothing.
const readBeforeUpdateProgram = `import "std/i64";

@noinline function chain(own acc: i32[], at: i32): i32[] {
  return acc.with(at, acc[at] + 1).with(at + 1, acc[at + 1] + 1);
}

@noinline function ends(own acc: i32[], v: i32): i32[] {
  return acc.with(0, v).with(acc.len() - 1, v + acc[1]);
}

@noinline function after(own acc: i32[], at: i32): i32[] {
  let next: i32[] = acc.with(at, 7);
  return next.with(at + 1, next[at] + acc[at]);
}

function main(): i32 {
  let a: i32[] = [];
  let i: i32 = 0;
  while (i < 256) {
    a = a.append(i);
    i = i + 1;
  }
  let before: i64 = __heap_alloc_count();
  i = 0;
  while (i < 100) {
    a = chain(a, 2 * i);
    i = i + 1;
  }
  let c: i64 = __heap_alloc_count() - before;
  before = __heap_alloc_count();
  i = 0;
  while (i < 100) {
    a = ends(a, i);
    i = i + 1;
  }
  let e: i64 = __heap_alloc_count() - before;
  i = 0;
  while (i < 100) {
    a = after(a, 2 * i + 3);
    i = i + 1;
  }
  print((a[0] as i64).to_string() + " " + (a[1] as i64).to_string() + " " + (a[2] as i64).to_string() + " " + (a[4] as i64).to_string() + " " + (a[5] as i64).to_string() + " " + (a[255] as i64).to_string() + " " + c.to_string() + " " + e.to_string() + "\n");
  return 0;
}
`

func TestSelfHostReadBeforeUpdate(t *testing.T) {
	want := "99 2 3 11 7 101 0 0\n"
	runSemanticProgram(t, "readbeforeupdate", readBeforeUpdateProgram, []string{"chain", "ends", "after"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}
