package e2ecompiler

import "testing"

// A function returns a `str` view of a field of a record parameter (#15796).
// Each caller holds the only count of its record, and its last mention of
// the record is the call. The caller must release the record after the last
// read of the view. The churn reuses any block that is released early. The
// view of a field must cost the allocations that a view of a `string`
// parameter costs, so the callee does not copy it.
const paramFieldViewProgram = `import "std/string";
struct Inner { text: string, k: i32 }
struct Outer { inner: Inner, tag: string }
@noinline function head(t: Inner, k: i32): str {
  return slice_unchecked(t.text, 0, k);
}
@noinline function deep(o: Outer, k: i32): str {
  return slice_unchecked(o.inner.text, 1, k);
}
@noinline function via_copy(o: Outer, k: i32): str {
  let u = o;
  let w = u.inner;
  return slice_unchecked(w.text, 2, k);
}
@noinline function head_of(s: string, k: i32): str {
  return slice_unchecked(s, 0, k);
}
@noinline function fresh(unit: string): string { return unit.repeat(12); }
@noinline function churn(): i32 {
  let total = 0;
  for i in 0..256 {
    let junk = fresh("zq");
    total += junk.len();
  }
  return total;
}
@noinline function from_temporary(): i32 {
  let v = head(Inner { text: fresh("ab"), k: 1 }, 6);
  if (churn() != 6144) { return 20; }
  if (v != "ababab") { return 21; }
  return 0;
}
@noinline function from_local(): i32 {
  let t = Inner { text: fresh("cd"), k: 2 };
  let v = head(t, 4);
  if (churn() != 6144) { return 20; }
  if (v != "cdcd") { return 22; }
  return 0;
}
@noinline function from_nested(): i32 {
  let o = Outer { inner: Inner { text: fresh("ef"), k: 3 }, tag: fresh("x") };
  let v = deep(o, 5);
  if (churn() != 6144) { return 20; }
  if (v != "fefe") { return 23; }
  return 0;
}
@noinline function from_copy(): i32 {
  let v = via_copy(Outer { inner: Inner { text: fresh("gh"), k: 4 }, tag: fresh("y") }, 8);
  if (churn() != 6144) { return 20; }
  if (v != "ghghgh") { return 24; }
  return 0;
}
@noinline function no_copy(): i32 {
  let t = Inner { text: fresh("ij"), k: 5 };
  let s = fresh("ij");
  let before: i64 = __heap_alloc_count();
  let n = 0;
  for i in 0..10 {
    n += head(t, 2 + i).len();
  }
  let field: i64 = __heap_alloc_count() - before;
  before = __heap_alloc_count();
  for i in 0..10 {
    n += head_of(s, 2 + i).len();
  }
  let param: i64 = __heap_alloc_count() - before;
  if (field != param) { return 25; }
  if (n != 130) { return 26; }
  return 0;
}
function main(): i32 {
  let r = from_temporary() + from_local() + from_nested() + from_copy() + no_copy();
  if (r != 0) { return r; }
  if (__rc_underflow_count() != 0) { return 27; }
  print("field views survive");
  return 0;
}
`

func TestSelfHostParamFieldViewTargets(t *testing.T) {
	cli := buildRuntimeCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				if got, _ := runLentView(t, cli, paramFieldViewProgram, target, sanitize); got != "field views survive\n" {
					t.Fatalf("sanitize=%v: got %q", sanitize, got)
				}
			}
		})
	}
}
