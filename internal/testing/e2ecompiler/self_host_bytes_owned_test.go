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
  print(t.to_string() + " " + (__heap_alloc_count() - before).to_string());
  return 0;
}
`

func TestSelfHostBytesOwned(t *testing.T) {
	want := "5 8869 18 721642 0 10 6 3\n800 202\n"
	runSemanticProgram(t, "bytesowned", bytesOwnedProgram, []string{"owned"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}
