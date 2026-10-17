package e2ecompiler

import "testing"

// bytesOwnedProgram copies byte views into owned arrays with
// `__bytes_owned`: a slice of a packed u8[], a string's bytes (a view tagged
// as string-backed), an empty slice, a whole array lent as a view, and a copy
// grown afterwards, which must not touch the source. The last line counts
// what 100 copies of an eight-byte slice allocate: the slice's borrowed view
// box and the copy, two each.
const bytesOwnedProgram = `import "std/i32";
import "std/i64";

@noinline function owned(b: [u8]): u8[] {
  return __bytes_owned(b);
}

@noinline function sum(xs: u8[]): i32 {
  let t: i32 = 0;
  for x in xs {
    t = t * 7 + x as i32;
    t = t % 1000003;
  }
  return t;
}

function main(): i32 {
  let arr: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
  let a: u8[] = owned(arr[2:7]);
  let s: string = "hello, owned bytes";
  let b: u8[] = owned(s.as_bytes());
  let c: u8[] = owned(arr[3:3]);
  let d: u8[] = owned(arr);
  let e: u8[] = a.append(99);
  print(a.len().to_string() + " " + sum(a).to_string() + " " + b.len().to_string() + " " + sum(b).to_string() + " " + c.len().to_string() + " " + d.len().to_string() + " " + e.len().to_string() + " " + (arr[2] as i32).to_string());
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  let t: i32 = 0;
  while (i < 100) {
    t = t + owned(arr[1:9]).len();
    i = i + 1;
  }
  let n: i64 = __heap_alloc_count() - before;
  print(t.to_string() + " " + n.to_string());
  return 0;
}
`

func TestSelfHostBytesOwned(t *testing.T) {
	want := "5 8869 18 721642 0 10 6 3\n800 200\n"
	runSemanticProgram(t, "bytesowned", bytesOwnedProgram, []string{"owned"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}

// bytesRangeProgram copies windows with `__bytes_range`: of a packed u8[], of
// a string's bytes, and an empty one. A window costs one box: no view of it
// is built first, so 100 copies of five bytes allocate 100 times, and an
// empty window is the static empty array, so 200 of them allocate nothing.
const bytesRangeProgram = `import "std/i64";

@noinline function ranged(b: [u8], from: i32, to: i32): u8[] {
  return __bytes_range(b, from, to);
}

function main(): i32 {
  let arr: u8[] = [10, 20, 30, 40, 50, 60];
  let s: string = "abcdefgh";
  let a: u8[] = ranged(arr, 1, 4);
  let b: u8[] = ranged(s.as_bytes(), 2, 5);
  let c: u8[] = ranged(arr, 3, 3);
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  let t: i32 = 0;
  while (i < 100) {
    t = t + ranged(arr, 0, 5).len();
    i = i + 1;
  }
  let n: i64 = __heap_alloc_count() - before;
  before = __heap_alloc_count();
  i = 0;
  while (i < 100) {
    t = t + ranged(arr, 2, 2).len() + ranged(s.as_bytes(), 8, 8).len();
    i = i + 1;
  }
  let e: i64 = __heap_alloc_count() - before;
  print((a.len() as i64).to_string() + " " + (a[0] as i64).to_string() + " " + (a[2] as i64).to_string() + " " + (b.len() as i64).to_string() + " " + (b[0] as i64).to_string() + " " + (c.len() as i64).to_string() + " " + (t as i64).to_string() + " " + n.to_string() + " " + e.to_string());
  return 0;
}
`

func TestSelfHostBytesRange(t *testing.T) {
	want := "3 20 40 3 99 0 500 100 0\n"
	runSemanticProgram(t, "bytesrange", bytesRangeProgram, []string{"ranged"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}
