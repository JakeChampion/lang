package e2ecompiler

import (
	"fmt"
	"testing"
)

const byteViewAllocationProgram = `@noinline function join(a: string, b: string): string { return a + b; }
@noinline function bytes(s: string): [u8] { return s.as_bytes(); }
@noinline function borrowed_bytes(s: str): [u8] { return s.as_bytes(); }
@noinline function check(s: string): i32 {
  let before: i64 = __heap_alloc_count();
  let v: [u8] = bytes(s);
  if (__heap_alloc_count() != before) { return 11; }
  if (v.len() != s.len()) { return 12; }
  let i: i32 = 0;
  while (i < v.len()) {
    if (v[i] != s[i]) { return 13; }
    i = i + 1;
  }
  return 0;
}
@noinline function check_slice(s: string): i32 {
  let text: str = slice_unchecked(s, 1, s.len() - 1);
  let before: i64 = __heap_alloc_count();
  let v: [u8] = borrowed_bytes(text);
  if (__heap_alloc_count() != before) { return 14; }
  if (v.len() != text.len() || v[0] != text[0]) { return 15; }
  return 0;
}
function main(): i32 {
  let heap: string = join("abcde", "fghij");
  let result: i32 = check(heap);
  if (result != 0) { return result; }
  result = check("a\0é界𐐀");
  if (result != 0) { return result; }
  result = check("");
  if (result != 0) { return result; }
  result = check_slice(heap);
  if (result != 0) { return result; }
  if (__rc_underflow_count() != 0) { return 16; }
  return 0;
}
`

const byteViewAliasesProgram = `struct Holder { bytes: [u8], n: i32 }
@noinline function join(a: string, b: string): string { return a + b; }
@noinline function keep(v: [u8]): [u8] { return v; }
@noinline function first(v: [u8]): i32 { return v[0] as i32; }
@noinline function exercise(s: string, a: u8[]): i32 {
  let v: [u8] = s.as_bytes();
  let av: [u8] = keep(a);
  let h: Holder = Holder { bytes: v, n: 1 };
  let h2: Holder = Holder { ...h, n: 2 };
  let pair: ([u8], [u8]) = (keep(v), av);
  let values: [u8][] = [v, av];
  let alias: [u8][] = values;
  let expanded: [u8][] = values.append(v);
  let window: [u8] = v[1:4];
  let array_window: [u8] = av[1:4];
  let empty: [u8] = v[v.len():v.len()];
  if (first(h.bytes) != 97 || first(h2.bytes) != 97 || first(pair.0) != 97 || first(pair.1) != 97) { return 21; }
  if (alias.len() != 2 || expanded.len() != 3 || first(alias[0]) != 97 || first(expanded[2]) != 97) { return 22; }
  if (window.len() != 3 || array_window.len() != 3 || window[0] != 98 as u8 || array_window[2] != 100 as u8) { return 23; }
  if (empty.len() != 0 || s.len() != 10 || a.len() != 5 || v[9] != 106 as u8) { return 24; }
  return 0;
}
function main(): i32 {
  let s: string = join("abcde", "fghij");
  let a: u8[] = [97, 98, 99, 100, 101];
  let i: i32 = 0;
  while (i < 20) {
    let result: i32 = exercise(s, a);
    if (result != 0) { return result; }
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 25; }
  return 0;
}
`

const byteViewContainersProgram = `import "core/map";
@noinline function join(a: string, b: string): string { return a + b; }
@noinline function choose(v: [u8]): Option[[u8]] { return Some(v); }
@noinline function exercise(s: string): i32 {
  let v: [u8] = s.as_bytes();
  let m: Map[i32, [u8]] = map_new(2);
  m = m.insert(1, v);
  let held = m;
  m = m.insert(2, v);
  let got = m.get_or(1, v);
  m = m.insert(1, "z".as_bytes());
  if (got.len() != 10 || got[0] != 97 as u8 || held.get_or(1, v)[0] != 97 as u8) { return 31; }
  let total: i32 = 0;
  for (_, bytes) in m { total = total + bytes.len(); }
  if (total != 11) { return 32; }
  match (m.get(2)) { Some(bytes) => { if (bytes[9] != 106 as u8) { return 33; } }, None => { return 34; } }
  if (v[9] != 106 as u8) { return 41; }
  let o = choose(v);
  let alias = o;
  match (o) { Some(bytes) => { if (bytes[0] != 97 as u8) { return 51; } }, None => { return 52; } }
  match (alias) { Some(bytes) => { if (bytes[9] != 106 as u8) { return 53; } }, None => { return 54; } }
  return 0;
}
function main(): i32 {
  let s: string = join("abcde", "fghij");
  let i: i32 = 0;
  while (i < 20) {
    let r = exercise(s);
    if (r != 0) { return r; }
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 55; }
  return 0;
}
`

const byteViewLoopProgram = `@noinline function join(a: string, b: string): string { return a + b; }
@noinline function keep(v: [u8]): [u8] { return v; }
@noinline function sum(v: [u8]): i32 {
  let total: i32 = 0;
  for byte in v { total = total + (byte as i32); }
  return total;
}
@noinline function exercise(s: string, a: u8[]): i32 {
  let left: [u8] = s.as_bytes();
  let right: [u8] = keep(a);
  let text_sum: i32 = sum(left);
  let array_sum: i32 = sum(right);
  let i: i32 = 0;
  while (i < 20) {
    if ((i & 1) == 0) {
      if (left.len() != s.len() || right.len() != a.len()) { return 61; }
      if (sum(left) != text_sum || sum(right) != array_sum) { return 62; }
    } else {
      if (left.len() != a.len() || right.len() != s.len()) { return 63; }
      if (sum(left) != array_sum || sum(right) != text_sum) { return 64; }
    }
    if (left[0] != 97 as u8 || right[0] != 97 as u8) { return 65; }
    let old: [u8] = left;
    left = right;
    right = old;
    i = i + 1;
  }
  return 0;
}
function main(): i32 {
  let s: string = join("abcde", "fghij");
  let a: u8[] = [97, 98, 99];
  let result: i32 = exercise(s, a);
  if (result != 0) { return result; }
  if (__rc_underflow_count() != 0) { return 66; }
  return 0;
}
`

// A sub-range of a string-backed view aliases the source's bytes on the
// register backends: it is read after the source's last use, through a call,
// a record, a nested sub-range and a phi, and must balance under the census.
const byteViewSubRangeProgram = `import "std/i32";
struct Hold { b: [u8], n: i32 }
@noinline function join(a: string, b: string): string { return a + b; }
@noinline function sum(v: [u8]): i32 {
    let t: i32 = 0;
    for x in v { t = t + (x as i32); }
    return t;
}
@noinline function pick(v: [u8], i: i32): [u8] { return v[i:i + 2]; }
@noinline function exercise(n: i32): i32 {
    let s: string = join("abcdefgh", n.to_string());
    let v: [u8] = s.as_bytes();
    let sub: [u8] = v[2:6];
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 20) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    if (sub.len() != 4 || sub[0] != 99 as u8 || sub[3] != 102 as u8) { return 11; }
    let h: Hold = Hold { b: sub, n: 1 };
    let inner: [u8] = h.b[1:3];
    if (inner[0] != 100 as u8 || inner.len() != 2) { return 12; }
    if (sum(sub) != 99 + 100 + 101 + 102) { return 13; }
    let p: [u8] = pick(v, 6);
    if (p[0] != 103 as u8 || p[1] != 104 as u8) { return 14; }
    let empty: [u8] = v[3:3];
    if (empty.len() != 0) { return 15; }
    let w: [u8] = sub;
    if (n > 1) { w = v[0:1]; }
    if (w[0] != 97 as u8 && w[0] != 99 as u8) { return 16; }
    return 0;
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 30) {
        let r: i32 = exercise(i);
        if (r != 0) { return r; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 25; }
    return 0;
}
`

// The sub-range's allocation does not grow with its length: 0 when the
// window allocated under 1 KiB, 7 when it copied. Every intermediate string
// stays alive, so no freed block can absorb a copy unseen.
const byteViewSubRangeCopyProgram = `@noinline function window(v: [u8]): [u8] { return v[1:60001]; }
function main(): i32 {
    let parts: string[] = ["abcdefghijklmnop"];
    let i: i32 = 0;
    while (i < 13) { parts = parts.append(parts[i] + parts[i]); i = i + 1; }
    let v: [u8] = parts[13].as_bytes();
    let before: i64 = __heap_bump_bytes();
    let w: [u8] = window(v);
    let after: i64 = __heap_bump_bytes();
    if (w.len() != 60000 || w[0] != 98 as u8 || w[59999] != 97 as u8) { return 3; }
    if (after - before < 1024i64) { return 0; }
    return 7;
}
`

const byteViewChecksumProgram = `import "std/hash";
import "std/string";
function main(): i32 {
  let input: string = "abcdefghijklmnop".repeat(256);
  let state = hash.crc32_new();
  let before: i64 = __heap_alloc_count();
  state = state.update(input);
  if (__heap_alloc_count() != before) { return 71; }
  if (state.finish() != 3631681148 || state.len() != 4096 as u64) { return 72; }
  if (__rc_underflow_count() != 0) { return 73; }
  return 0;
}
`

func TestSelfHostByteViewRuntime(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range []struct{ name, source string }{
			{"no-conversion-allocation", byteViewAllocationProgram},
			{"aliases-and-slices", byteViewAliasesProgram},
			{"maps-options", byteViewContainersProgram},
			{"loop-carried-backing-layout", byteViewLoopProgram},
			{"crc32-consumer-allocation", byteViewChecksumProgram},
			{"string-sub-range-aliases", byteViewSubRangeProgram},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				stderr, code := cli.exitOf(t, tc.source, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("byte view: exit %d\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
		// wasm's strings are inline blocks with no data pointer to share, so
		// there a sub-range copies, as a string slice does.
		t.Run(target+"/string-sub-range-zero-copy", func(t *testing.T) {
			want := 0
			if target == "wasm32-wasi" {
				want = 7
			}
			if stderr, code := cli.exitOf(t, byteViewSubRangeCopyProgram, target); code != want {
				t.Fatalf("sub-range: exit %d, want %d\n%s", code, want, stderr)
			}
		})
		for _, storage := range []struct{ name, value string }{
			{"string", `"abc".as_bytes()`},
			{"array", "a"},
		} {
			for _, tc := range []struct{ name, body, args string }{
				{"negative-index", "return v[i] as i32;", "0 - 1, 0"},
				{"past-end-index", "return v[i] as i32;", "3, 0"},
				{"negative-slice", "return v[i:j].len();", "0 - 1, 2"},
				{"reversed-slice", "return v[i:j].len();", "2, 1"},
				{"past-end-slice", "return v[i:j].len();", "0, 4"},
			} {
				t.Run(target+"/"+storage.name+"/"+tc.name, func(t *testing.T) {
					source := fmt.Sprintf(`@noinline function fault(v: [u8], i: i32, j: i32): i32 { %s }
function main(): i32 { let a: u8[] = [1, 2, 3]; return fault(%s, %s); }`, tc.body, storage.value, tc.args)
					stderr, code := cli.exitOf(t, source, target)
					if code != 134 {
						t.Fatalf("byte-view bounds: exit %d, want 134\n%s", code, stderr)
					}
				})
			}
		}
	}
}
