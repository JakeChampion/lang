package e2ecompiler

import "testing"

// A callee keeps a `string` parameter also when it stores it in a cell or
// in a closure, directly or through a second callee. A view that a call
// lends to such a parameter is copied, whatever the result type (#15741).
// `count_only` reads its parameter, so its view is lent with no copy.
const storedViewArgumentProgram = `import "std/string";
@noinline function store(s: string, c: Cell[string]): i32 {
  c.set(s);
  return 0;
}
@noinline function store_via(s: string, c: Cell[string]): i32 {
  return store(s, c) + 1;
}
@noinline function capture(s: string): () => string {
  return (): string => s;
}
@noinline function count_only(s: string, c: Cell[string]): i32 {
  return s.len() + c.get().len();
}
function text(n: i32, unit: string): string { return unit.repeat(n); }
@noinline function kept_in_cell(c: Cell[string]): i32 {
  let src = text(12, "ef");
  return store(slice_unchecked(src, 2, 10), c);
}
@noinline function kept_through(c: Cell[string]): i32 {
  let src = text(12, "gh");
  return store_via(slice_unchecked(src, 2, 10), c);
}
@noinline function kept_in_closure(): () => string {
  let src = text(12, "ij");
  return capture(slice_unchecked(src, 2, 10));
}
@noinline function churn(): i32 {
  let total = 0;
  for i in 0..64 {
    let junk = text(12, "zq");
    total += junk.len();
  }
  return total;
}
function main(): i32 {
  let c = cell_new("");
  let r = kept_in_cell(c);
  let t = churn();
  if (c.get() != "efefefef") { return 22; }
  let d = cell_new("");
  let r2 = kept_through(d);
  t += churn();
  if (d.get() != "ghghghgh") { return 23; }
  let f = kept_in_closure();
  t += churn();
  if (f() != "ijijijij") { return 24; }
  let src = text(4096, "x");
  let before: i64 = __heap_alloc_count();
  let n = 0;
  for k in 0..10 {
    n += count_only(slice_unchecked(src, 0, 2000 + k), c);
  }
  if (__heap_alloc_count() != before) { return 25; }
  return r + r2 - 1 + t - 4608 + n - 20125;
}
`

func TestSelfHostStoredViewArgument(t *testing.T) {
	cli := buildRuntimeCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, storedViewArgumentProgram, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("stored view argument: exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
