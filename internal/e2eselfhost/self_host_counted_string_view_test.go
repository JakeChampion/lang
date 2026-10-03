package e2eselfhost

import "testing"

// The descriptor has its own reference count; releasing a retained alias
// must preserve both the descriptor and the borrowed source bytes.
const countedStringViewProgram = `@noinline function join(a: string, b: string): string { return a + b; }
@noinline function middle(s: string): str {
  return slice_unchecked(s, 1, s.len() - 1);
}
@noinline function check(v: str): i32 {
  let p: usize = v as usize;
  let before: i32 = __load_i32(p - 8);
  if (before < 1) { return 11; }
  let allocations: i64 = __heap_alloc_count();
  __fern_rc_inc(p);
  if (__load_i32(p - 8) != before + 1) { return 12; }
  __fern_str_free(p);
  if (__load_i32(p - 8) != before) { return 13; }
  if (__heap_alloc_count() != allocations) { return 14; }
  if (v.len() != 8 || v[0] != b'b' || v[7] != b'i') { return 15; }
  return 0;
}
@noinline function round(s: string): i32 {
  let v: str = middle(s);
  let result: i32 = check(v);
  if (result != 0) { return result; }
  if (s != "abcdefghij" || v != "bcdefghi") { return 16; }
  return 0;
}
function main(): i32 {
  let input: string = join("abcde", "fghij");
  let i: i32 = 0;
  while (i < 20) {
    let result: i32 = round(input);
    if (result != 0) { return result; }
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 17; }
  return 0;
}
`

func TestSelfHostCountedStringView(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, countedStringViewProgram, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("counted string view: exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
