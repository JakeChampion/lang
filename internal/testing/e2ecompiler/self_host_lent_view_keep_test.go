package e2ecompiler

import "testing"

// A str view handed to a string parameter is copied only when the callee can
// keep that parameter. Exit 10 to 12: a callee that returns a different
// parameter copied the view (#15719). Exit 20 to 23: a callee that keeps the
// view, in its result, through a callee, in a cell or in a closure, got the
// view and not a copy, so the kept text reads the reused block of its source.
const lentViewKeepSrc = `import "std/string";
struct Work { tk: i32[], name: string }
@noinline function probe(s: string, w: Work): i32 { return s.len() + w.tk.len(); }
@noinline function pass(s: string, w: Work): Work { return w; }
@noinline function pass_on(s: string, w: Work): Work { return pass(s, w); }
function (w: Work) passed(s: string): Work { return w; }
@noinline function keep(s: string, w: Work): Work { return Work { ...w, name: s }; }
@noinline function keep_on(s: string, w: Work): Work {
  let v = keep(s, w);
  return v;
}
@noinline function store(s: string, c: Cell[string]): i32 {
  c.set(s);
  return 0;
}
@noinline function capture(s: string): () => string {
  return (): string => s;
}
function text(n: i32, unit: string): string { return unit.repeat(n); }
@noinline function kept_by_result(w: Work): Work {
  let src = text(12, "ab");
  return keep(slice_unchecked(src, 2, 10), w);
}
@noinline function kept_through_callee(w: Work): Work {
  let src = text(12, "cd");
  return keep_on(slice_unchecked(src, 2, 10), w);
}
@noinline function kept_in_cell(c: Cell[string]): i32 {
  let src = text(12, "ef");
  return store(slice_unchecked(src, 2, 10), c);
}
@noinline function kept_by_closure(): () => string {
  let src = text(12, "gh");
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
  let src = "x".repeat(4000);
  let w = Work { tk: [1], name: "" };
  let total = 0;
  let mark = __heap_alloc_count();
  for k in 0..10 {
    total += probe(slice_unchecked(src, 0, 3000 + k), w);
  }
  let control = __heap_alloc_count() - mark;
  mark = __heap_alloc_count();
  for k in 0..10 {
    w = pass(slice_unchecked(src, 0, 3000 + k), w);
  }
  if (__heap_alloc_count() - mark != control) { return 10; }
  mark = __heap_alloc_count();
  for k in 0..10 {
    w = pass_on(slice_unchecked(src, 0, 3000 + k), w);
  }
  if (__heap_alloc_count() - mark != control) { return 11; }
  mark = __heap_alloc_count();
  for k in 0..10 {
    w = w.passed(slice_unchecked(src, 0, 3000 + k));
  }
  if (__heap_alloc_count() - mark != control) { return 12; }
  let a = kept_by_result(w);
  let b = kept_through_callee(w);
  let c = cell_new("");
  let r = kept_in_cell(c);
  let f = kept_by_closure();
  let t = churn();
  if (a.name != "abababab") { return 20; }
  if (b.name != "cdcdcdcd") { return 21; }
  if (c.get() != "efefefef") { return 22; }
  if (f() != "ghghghgh") { return 23; }
  if (total <= 0 || r != 0 || t != 1536) { return 24; }
  return 0;
}
`

// The sanitizer never reuses a freed block, so the kept rows read their
// source only on the plain build. The sanitized build gives the census.
func TestSelfHostLentViewKeptPerParameter(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, lentViewKeepSrc, target); code != 0 {
				t.Fatalf("plain build: exit %d, want 0\n%s", code, stderr)
			}
			stderr, code := cli.exitOf(t, lentViewKeepSrc, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("sanitized build: exit %d, want 0\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
