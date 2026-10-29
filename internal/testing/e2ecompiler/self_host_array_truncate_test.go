package e2ecompiler

import "testing"

const truncateProlog = `struct P { name: string, n: i32 }
struct Holder { ps: P[], tag: string }
function digits(i: i32): string {
  return "d" + int.int_to_string(i);
}
function people(n: i32): P[] {
  let ps: P[] = [];
  let i: i32 = 0;
  while (i < n) {
    ps = ps.append(P { name: digits(i), n: i });
    i = i + 1;
  }
  return ps;
}
function names(n: i32): string[] {
  let xs: string[] = [];
  let i: i32 = 0;
  while (i < n) {
    xs = xs.append(digits(i));
    i = i + 1;
  }
  return xs;
}
`

func truncateSrc(body string) string {
	return truncateProlog + "function main(): i32 {\n" + body +
		"\n  if (__rc_underflow_count() != 0) { return 99; }\n  return r;\n}\n"
}

var truncateCases = []struct{ name, body string }{
	{"unique-strings", `  let xs: string[] = names(6);
  xs = xs.truncate(3);
  let r: i32 = xs.len() * 10 + xs[2].len();`},
	{"shared-receiver", `  let xs: string[] = names(5);
  let ys: string[] = xs.truncate(2);
  ys = ys.append("tail");
  let r: i32 = xs.len() * 10 + ys.len();
  if (xs[2] != "d2" || ys[2] != "tail") { return 98; }`},
	{"borrowed-param", `  let ps: P[] = people(4);
  let two: P[] = first_two(ps);
  let r: i32 = ps.len() * 10 + two.len();
  if (two[1].n != 1 || ps[3].n != 3) { return 98; }`},
	{"swap-remove", `  let ps: P[] = people(5);
  let last: i32 = ps.len() - 1;
  ps = ps.with(1, ps[last]).truncate(last);
  let r: i32 = ps.len() * 10 + ps[1].n;
  if (ps[1].name != "d4" || ps[3].n != 3) { return 98; }`},
	{"field-update", `  let h: Holder = Holder { ps: people(4), tag: "h" };
  h = Holder { ...h, ps: h.ps.truncate(1) };
  let r: i32 = h.ps.len() * 10 + h.tag.len();`},
	{"clamped", `  let xs: i32[] = [1, 2, 3];
  let big: i32[] = xs.truncate(10);
  let none: i32[] = xs.truncate(0 - 4);
  let r: i32 = big.len() * 10 + none.len() + xs.len();`},
	{"scalars-in-place", `  let xs: i32[] = [];
  let i: i32 = 0;
  while (i < 40) { xs = xs.append(i); i = i + 1; }
  xs = xs.truncate(7);
  xs = xs.append(70);
  let r: i32 = xs.len() + xs[7] + xs[6];`},
	{"nested-arrays", `  let rows: string[][] = [names(2), names(3), names(4)];
  rows = rows.truncate(2);
  let r: i32 = rows.len() * 10 + rows[1].len();`},
	{"pop-loop", `  let r: i32 = pop_all(names(50000)) % 97;`},
	{"borrowed-swap-remove", `  let ps: P[] = people(6);
  let k: i32 = 0;
  while (ps.len() > 1) { ps = remove_at(ps, k % ps.len()); k = k + 3; }
  let r: i32 = ps[0].n;`},
	{"held-element-removal", `  let ps: P[] = people(3000);
  let before: i64 = __heap_alloc_count();
  let sum: i32 = 0;
  while (ps.len() > 0) {
    let w: (P[], i32) = wake(ps, 0);
    ps = w.0;
    sum = sum + w.1;
  }
  if (__heap_alloc_count() - before > 6100i64) { return 97; }
  let r: i32 = sum % 101;`},
	{"held-element-removal-inlined", `  let ps: P[] = people(3000);
  let before: i64 = __heap_alloc_count();
  let sum: i32 = 0;
  while (ps.len() > 0) {
    let p: P = ps[0];
    ps = ps.with(0, ps[ps.len() - 1]).truncate(ps.len() - 1);
    sum = sum + p.n + p.name.len();
  }
  if (__heap_alloc_count() - before > 100i64) { return 97; }
  let r: i32 = sum % 101;`},
	{"held-element-join", `  let ps: P[] = people(3000);
  let before: i64 = __heap_alloc_count();
  let sum: i32 = 0;
  while (ps.len() > 0) {
    let p: P = ps[ps.len() - 1];
    ps = remove_at(ps, ps.len() - 1);
    sum = sum + p.n + p.name.len();
  }
  if (__heap_alloc_count() - before > 100i64) { return 97; }
  let r: i32 = sum % 101;`},
	{"held-element-spliced-swap", `  let ps: P[] = people(3000);
  let before: i64 = __heap_alloc_count();
  let sum: i32 = 0;
  let k: i32 = 0;
  while (ps.len() > 0) {
    let at: i32 = k % ps.len();
    let p: P = ps[at];
    ps = remove_at(ps, at);
    sum = sum + p.n + p.name.len();
    k = k + 7;
  }
  if (__heap_alloc_count() - before > 100i64) { return 97; }
  let r: i32 = sum % 101;`},
	{"borrowed-swap-remove-kept", `  let ps: P[] = people(6);
  let qs: P[] = remove_at(ps, 1);
  let r: i32 = ps.len() * 10 + qs.len();
  if (ps[1].n != 1 || qs[1].n != 5) { return 98; }`},
	{"shared-to-empty", `  let xs: string[] = names(3);
  let before: i64 = __heap_alloc_count();
  let ys: string[] = xs.truncate(0);
  if (__heap_alloc_count() - before != 0i64) { return 97; }
  ys = ys.append("tail");
  let r: i32 = xs.len() * 10 + ys.len();
  if (xs[2] != "d2" || ys[0] != "tail") { return 98; }`},
	{"shared-bytes-to-empty", `  let xs: u8[] = [1, 2, 3];
  let before: i64 = __heap_alloc_count();
  let ys: u8[] = xs.truncate(0 - 1);
  if (__heap_alloc_count() - before != 0i64) { return 97; }
  ys = ys.append(9);
  let r: i32 = xs.len() * 10 + ys.len() + (ys[0] as i32);`},
}

const truncateHelpers = `import "core/int";
function first_two(xs: P[]): P[] {
  return xs.truncate(2);
}
function remove_at(ps: P[], at: i32): P[] {
  let last: i32 = ps.len() - 1;
  if (at != last) {
    ps = ps.with(at, ps[last]);
  }
  return ps.truncate(last);
}
@noinline function wake(ps: P[], at: i32): (P[], i32) {
  let p: P = ps[at];
  ps = remove_at(ps, at);
  return (ps, p.n + p.name.len());
}
function pop_all(own xs: string[]): i32 {
  let popped: i32 = 0;
  while (xs.len() > 0) {
    xs = xs.truncate(xs.len() - 1);
    popped = popped + 1;
  }
  return popped;
}
`

// TestSelfHostArrayTruncate runs each case on every target against the
// interpreter, and requires a balanced census where the target reports one.
func TestSelfHostArrayTruncate(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range truncateCases {
		src := truncateHelpers + truncateSrc(tc.body)
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOf(t, src, target, "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
				if code != want {
					t.Fatalf("exited %d, want %d (interp oracle; 99 = rc underflow)\n%s", code, want, stderr)
				}
				if target == "wasm32-wasi" {
					return
				}
				summary := leakSummaryLine(stderr)
				var allocs, frees, live int64
				if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
					t.Fatalf("parse %q: %v", summary, err)
				}
				if allocs == 0 || allocs != frees || live != 0 {
					t.Errorf("unbalanced census: %s", summary)
				}
			})
		}
	}
}
