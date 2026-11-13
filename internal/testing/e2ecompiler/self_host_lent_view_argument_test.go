package e2ecompiler

import "testing"

// A view that a call lends to a `string` parameter is copied only when the
// callee can hand that parameter back in its result (#15719). `through`
// hands it back inside a value that a second callee builds.
const lentViewArgumentProgram = `import "std/string";
struct Work { tk: i32[] }
struct Named { name: string, n: i32 }
@noinline function pass_work(s: string, w: Work): Work { return w; }
@noinline function pass_named(s: string, w: Named): Named { return w; }
@noinline function keep(s: string, w: Named): Named { return Named { name: s, n: w.n + 1 }; }
struct Part { text: string }
@noinline function with_part(n: Named, p: Part): Named { return Named { name: p.text, n: n.n }; }
@noinline function through(s: string, n: Named): Named { return with_part(n, Part { text: s }); }
@noinline function longer(a: string, b: string): string {
  if (a.len() > b.len()) { return a; }
  return b;
}
@noinline function build(n: i32): string { return "x".repeat(n); }
function main(): i32 {
  let src = build(4096);
  let w = Work { tk: [1] };
  let before: i64 = __heap_alloc_count();
  for k in 0..10 {
    w = pass_work(slice_unchecked(src, 0, 2000 + k), w);
  }
  if (__heap_alloc_count() != before) { return 11; }
  if (w.tk.len() != 1) { return 12; }
  let named = Named { name: build(40), n: 0 };
  before = __heap_alloc_count();
  for k in 0..10 {
    named = pass_named(slice_unchecked(src, 0, 2000 + k), named);
  }
  if (__heap_alloc_count() != before) { return 13; }
  if (named.name.len() != 40) { return 14; }
  for k in 0..10 {
    named = keep(slice_unchecked(src, 0, 100 + k), named);
  }
  let best = build(3);
  for k in 0..4 {
    best = longer(slice_unchecked(src, 0, 50 + k), best);
  }
  let far = Named { name: build(2), n: 7 };
  far = through(slice_unchecked(src, 0, 77), far);
  src = build(8);
  let fill = build(4096);
  if (fill.len() != 4096 || far.n != 7 || far.name.len() != 77 || far.name[76] != b'x') { return 19; }
  if (named.n != 10 || named.name.len() != 109 || named.name[108] != b'x') { return 15; }
  if (best.len() != 53 || best[52] != b'x') { return 16; }
  if (src.len() != 8) { return 17; }
  if (__rc_underflow_count() != 0) { return 18; }
  return 0;
}
`

func TestSelfHostLentViewArgument(t *testing.T) {
	cli := buildRuntimeCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, lentViewArgumentProgram, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("lent view argument: exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
