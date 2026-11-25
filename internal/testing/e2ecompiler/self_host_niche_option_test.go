package e2ecompiler

import "testing"

// An Option of a box is the payload pointer, and null is None. Some(x) makes
// no box, and each payload kind and a nested Option keep their values.
const nicheOptionProgram = `struct P { a: u8[], b: i32 }
struct R { n: i32, p: Option[P] }
enum E { Leaf(i32), Pair(string, i32), Empty }
@noinline function mk(ps: P[], i: i32): R { return R { n: i, p: Some(ps[i]) }; }
@noinline function rd(r: R): i32 { match (r.p) { Some(p) => { return p.b; }, None => { return 0; } } }
@noinline function pick_s(xs: string[], i: i32): Option[string] { if (i < xs.len()) { return Some(xs[i]); } return None; }
@noinline function pick_a(xs: i32[][], i: i32): Option[i32[]] { if (i < xs.len()) { return Some(xs[i]); } return None; }
@noinline function pick_e(xs: E[], i: i32): Option[E] { if (i < xs.len()) { return Some(xs[i]); } return None; }
@noinline function nest(p: P, k: i32): Option[Option[P]] {
  if (k == 0) { return None; }
  if (k == 1) { return Some(None); }
  return Some(Some(p));
}
@noinline function weight(e: Option[E]): i32 {
  match (e) {
    Some(Leaf(n)) => { return n; },
    Some(Pair(s, n)) => { return s.len() + n; },
    Some(Empty()) => { return 100; },
    None => { return 1000; },
  }
}
function main(): i32 {
  let ps: P[] = [P { a: [1], b: 2 }, P { a: [3], b: 4 }];
  let t = 0;
  let before = __heap_alloc_count();
  for i in 0..1000 { t = t + rd(mk(ps, i % 2)); }
  if (__heap_alloc_count() - before != 1000i64) { return 1; }
  if (t != 3000) { return 2; }
  let xs: string[] = ["ab", "cde"];
  let arrs: i32[][] = [[1, 2, 3], [4]];
  let es: E[] = [Leaf(5), Pair("xyz", 1), Empty];
  let sum = 0;
  before = __heap_alloc_count();
  for i in 0..1000 {
    match (pick_s(xs, i % 3)) { Some(s) => { sum = sum + s.len(); }, None => { sum = sum + 7; } }
    match (pick_a(arrs, i % 3)) { Some(a) => { sum = sum + a.len(); }, None => { sum = sum + 11; } }
    sum = sum + weight(pick_e(es, i % 4));
  }
  if (__heap_alloc_count() - before != 0i64) { return 3; }
  if (sum != 334 * 2 + 333 * 3 + 333 * 7 + 334 * 3 + 333 * 1 + 333 * 11 + 250 * (5 + 4 + 100 + 1000)) { return 4; }
  let held: Option[P][] = [None, Some(ps[0]), None];
  held = held.with(2, Some(ps[1]));
  held = held.with(1, None);
  let seen = 0;
  for h in held { match (h) { Some(p) => { seen = seen + p.b; }, None => { seen = seen + 10; } } }
  if (seen != 24) { return 5; }
  let deep = 0;
  for k in 0..3 {
    match (nest(ps[1], k)) {
      Some(Some(p)) => { deep = deep + p.b; },
      Some(None()) => { deep = deep + 20; },
      None => { deep = deep + 300; },
    }
  }
  if (deep != 324) { return 6; }
  return 0;
}
`

// A map read of such a value gives the value or null, with no Option box.
const nicheMapGetProgram = `import "core/map";
import "core/cmp";
struct Q { s: string[], n: i32 }
@derive(cmp.Eq, cmp.Hash)
struct K { a: i32, b: string }
function main(): i32 {
  let m: Map[string, Q] = map_new(4);
  m = m.insert("x", Q { s: ["a"], n: 1 });
  m = m.insert("y", Q { s: ["bb"], n: 2 });
  let t = 0;
  for k in ["x", "y", "z"] {
    match (m.get(k)) { Some(q) => { t = t + q.n + q.s[0].len(); }, None => { t = t + 100; } }
  }
  let ms: Map[string, string] = map_new(4);
  ms = ms.insert("a", "hello");
  match (ms.get("a")) { Some(s) => { t = t + s.len(); }, None => {} }
  match (ms.get("b")) { Some(_) => { t = t + 1000; }, None => { t = t + 7; } }
  let mk: Map[K, i32[]] = map_new(4);
  mk = mk.insert(K { a: 1, b: "p" }, [1, 2, 3]);
  match (mk.get(K { a: 1, b: "p" })) { Some(xs) => { t = t + xs.len(); }, None => { t = t + 10000; } }
  match (mk.get(K { a: 2, b: "p" })) { Some(_) => { t = t + 20000; }, None => { t = t + 9; } }
  if (t != 130) { return 1; }
  return 0;
}
`

func TestSelfHostNicheOption(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, probe := range []struct {
			name, program string
		}{
			{"payloads", nicheOptionProgram},
			{"map-get", nicheMapGetProgram},
		} {
			t.Run(target+"/"+probe.name, func(t *testing.T) {
				stderr, code := cli.exitOf(t, probe.program, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d, want 0\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
