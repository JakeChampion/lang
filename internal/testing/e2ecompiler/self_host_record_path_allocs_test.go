package e2ecompiler

import (
	"fmt"
	"testing"
)

const byteViewSliceProgram = `@noinline function viewlen(v: [u8]): i32 { return v.len(); }
@noinline function mk(n: i32): u8[] {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < n) { out = out.append((i & 255) as u8); i = i + 1; }
  return out;
}
function main(): i32 {
  let pt: u8[] = mk(100);
  let acc: i32 = 0;
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 1000) { acc = acc + viewlen(pt[1:90]); i = i + 1; }
  if (__heap_alloc_count() - before != %di64) { return 1; }
  before = __heap_alloc_count();
  i = 0;
  while (i < 1000) { acc = acc + viewlen(pt); i = i + 1; }
  if (__heap_alloc_count() - before != 0i64) { return 2; }
  if (acc != 189000) { return 3; }
  return 0;
}
`

const enumPayloadTakeProgram = `struct P { bytes: u8[], n: i32 }
struct C { p: P, k: i32 }
struct S { c: C, id: i32 }
enum T { Plain, Secured(S) }
@noinline function mk(id: i32): S {
  let b: u8[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12];
  return S { c: C { p: P { bytes: b, n: 0 }, k: 0 }, id: id };
}
@noinline function steps(s: S): S {
  let p: P = P { ...s.c.p, n: s.c.p.n + 1 };
  let c: C = C { ...s.c, p: p, k: s.c.k + 1 };
  return S { ...s, c: c };
}
@noinline function match_slot(n: i32): i64 {
  let slots: T[] = [Secured(mk(0)), Secured(mk(1)), Secured(mk(2))];
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < n) {
    match (slots[1]) {
      Secured(sess) => { slots = slots.with(1, Plain); slots = slots.with(1, Secured(steps(sess))); },
      Plain => {}
    }
    i = i + 1;
  }
  let d: i64 = __heap_alloc_count() - before;
  match (slots[1]) { Secured(s) => { if (s.c.k != n) { return 0 - 1; } }, Plain => { return 0 - 2; } }
  return d;
}
@noinline function match_local(n: i32): i64 {
  let slots: T[] = [Secured(mk(0)), Secured(mk(1)), Secured(mk(2))];
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < n) {
    let t: T = slots[1];
    slots = slots.with(1, Plain);
    match (t) {
      Secured(sess) => { slots = slots.with(1, Secured(steps(sess))); },
      Plain => {}
    }
    i = i + 1;
  }
  let d: i64 = __heap_alloc_count() - before;
  match (slots[1]) { Secured(s) => { if (s.c.k != n) { return 0 - 1; } }, Plain => { return 0 - 2; } }
  return d;
}
@noinline function option_local(n: i32): i64 {
  let opts: Option[S][] = [Some(mk(0)), Some(mk(1)), Some(mk(2))];
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < n) {
    let o: Option[S] = opts[1];
    opts = opts.with(1, None);
    if let Some(sess) = o { opts = opts.with(1, Some(steps(sess))); }
    i = i + 1;
  }
  let d: i64 = __heap_alloc_count() - before;
  if let Some(s) = opts[1] { if (s.c.k != n) { return 0 - 1; } } else { return 0 - 2; }
  return d;
}
function main(): i32 {
  if (match_slot(1000) > 2i64) { return 1; }
  if (match_local(1000) > 2i64) { return 2; }
  if (option_local(1000) > 1002i64) { return 3; }
  return 0;
}
`

func TestSelfHostRecordPathAllocs(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		sliceAllocs := 0
		if target == "wasm32-wasi" {
			sliceAllocs = 1000
		}
		for _, probe := range []struct {
			name, program string
		}{
			{"byte-view-slice", fmt.Sprintf(byteViewSliceProgram, sliceAllocs)},
			{"enum-payload-take", enumPayloadTakeProgram},
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
