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

const storedViewLifetimeProgram = `import "std/string";
struct Saved { text: string, n: i32 }
@noinline function store_text(s: string, c: Cell[string]): i32 {
  c.set(s);
  return 0;
}
@noinline function store_through(s: string, c: Cell[string]): i32 {
  return store_text(s, c);
}
@noinline function capture_text(s: string): () => string {
  return (): string => s;
}
@noinline function capture_through(s: string): () => string {
  return capture_text(s);
}
@noinline function save_result(s: string): Saved {
  return Saved { text: s, n: 17 };
}
@noinline function fresh(unit: string): string { return unit.repeat(12); }
@noinline function fill_cells(a: Cell[string], b: Cell[string]): i32 {
  let src = fresh("ef");
  return store_text(slice_unchecked(src, 2, 10), a)
    + store_through(slice_unchecked(src, 4, 12), b);
}
@noinline function make_closure(): () => string {
  let src = fresh("gh");
  return capture_text(slice_unchecked(src, 2, 10));
}
@noinline function make_indirect_closure(): () => string {
  let src = fresh("ij");
  return capture_through(slice_unchecked(src, 2, 10));
}
@noinline function make_result(): Saved {
  let src = fresh("kl");
  return save_result(slice_unchecked(src, 2, 10));
}
@noinline function churn(): i32 {
  let total = 0;
  for i in 0..256 {
    let junk = fresh("zq");
    total += junk.len();
  }
  return total;
}
function main(): i32 {
  let a = cell_new("");
  let b = cell_new("");
  if (fill_cells(a, b) != 0) { return 21; }
  if (churn() != 6144) { return 20; }
  if (a.get() != "efefefef") { return 22; }
  if (b.get() != "efefefef") { return 23; }
  let f = make_closure();
  if (churn() != 6144) { return 20; }
  if (f() != "ghghghgh") { return 24; }
  let g = make_indirect_closure();
  if (churn() != 6144) { return 20; }
  if (g() != "ijijijij") { return 25; }
  let saved = make_result();
  if (churn() != 6144) { return 20; }
  if (saved.text != "klklklkl" || saved.n != 17) { return 26; }
  if (__rc_underflow_count() != 0) { return 27; }
  print("stored views survive");
  return 0;
}
`

func TestSelfHostStoredViewLifetimeTargets(t *testing.T) {
	cli := buildRuntimeCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				got, stderr := runLentView(t, cli, storedViewLifetimeProgram, target, sanitize)
				if got != "stored views survive\n" {
					t.Fatalf("sanitize=%v: got %q", sanitize, got)
				}
				if sanitize {
					counts := lentViewLeak.FindStringSubmatch(stderr)
					if counts[1] == "0" {
						t.Fatalf("no allocations in the heap-churn control: %s", stderr)
					}
					t.Logf("allocs=%s frees=%s live_bytes=0", counts[1], counts[2])
				}
			}
		})
	}
}
