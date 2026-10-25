package e2e

import (
	"strings"
	"testing"
)

// ordmapDeepSrc builds maps of three and four levels, too large for the
// interpreted tests/stdlib/ordmap_test.fern.
const ordmapDeepSrc = `import "std/ordmap";
import "std/option";
import "std/i32";
import "core/cmp";

function lcg(x: i32): i32 {
  return (x * 1103515245 + 12345) & 2147483647;
}

function same(m: ordmap.OrdMap[i32, i32], present: i32[]): boolean {
  let at: i32 = 0;
  for e in m.iter() {
    while (at < present.len() && present[at] < 0) {
      at = at + 1;
    }
    if (at >= present.len() || e.0 != at || e.1 != present[at]) {
      return false;
    }
    at = at + 1;
  }
  while (at < present.len()) {
    if (present[at] >= 0) {
      return false;
    }
    at = at + 1;
  }
  return true;
}

function cut_ok(m: ordmap.OrdMap[i32, i32], present: i32[], k: i32): boolean {
  let below: i32 = 0;
  let j: i32 = 0;
  while (j < k) {
    if (present[j] >= 0) {
      below = below + 1;
    }
    j = j + 1;
  }
  let lo: ordmap.OrdMap[i32, i32] = m.split_lt(k);
  let hi: ordmap.OrdMap[i32, i32] = m.split_gt(k);
  let back: ordmap.OrdMap[i32, i32] = lo.merge(hi);
  let hit: i32 = 0;
  if (present[k] >= 0) {
    hit = 1;
    if (m.index_of(k).unwrap_or(-1) != below || m.key_at(below).unwrap_or(-1) != k) {
      return false;
    }
  }
  return lo.is_valid() && hi.is_valid() && back.is_valid() && lo.len() == below && back.len() == m.len() - hit;
}

function ints(): i32 {
  let range: i32 = 40000;
  let present: i32[] = [];
  let m: ordmap.OrdMap[i32, i32] = ordmap.ordmap_new();
  let i: i32 = 0;
  while (i < range) {
    present = present.append(i);
    m = m.insert(i, i);
    i = i + 1;
  }
  if (!m.is_valid() || !same(m, present)) {
    return 2;
  }
  let x: i32 = 3;
  let step: i32 = 0;
  while (step < 60000) {
    x = lcg(x);
    let k: i32 = x % range;
    let op: i32 = x / range % 10;
    if (op < 4) {
      m = m.insert(k, step);
      present = present.with(k, step);
    } else if (op < 9) {
      m = m.remove(k);
      present = present.with(k, -1);
    } else if (m.get_or(k, -1) != present[k]) {
      return 3;
    }
    if (step % 10000 == 9999) {
      if (!m.is_valid() || !same(m, present)) {
        return 4;
      }
      if (!cut_ok(m, present, k)) {
        return 5;
      }
    }
    step = step + 1;
  }
  i = 0;
  while (i < range) {
    m = m.remove(i);
    i = i + 1;
  }
  if (!m.is_empty() || !m.is_valid()) {
    return 6;
  }
  return 0;
}

function strs(): i32 {
  let m: ordmap.OrdMap[string, i32] = ordmap.ordmap_new();
  let all: string[] = [];
  let x: i32 = 5;
  let i: i32 = 0;
  while (i < 20000) {
    x = lcg(x);
    let k: string = "https://example.com/items/" + (x % 30000).to_string();
    if (!m.contains(k)) {
      all = all.append(k);
    }
    m = m.insert(k, i);
    i = i + 1;
  }
  let want: string[] = cmp.sort(all);
  let got: string[] = m.keys();
  if (!m.is_valid() || got.len() != want.len()) {
    return 7;
  }
  i = 0;
  while (i < want.len()) {
    if (got[i] != want[i]) {
      return 8;
    }
    if (i % 2 == 0) {
      m = m.remove(want[i]);
    }
    i = i + 1;
  }
  if (!m.is_valid() || m.len() != want.len() / 2 || m.contains(want[0]) || !m.contains(want[1])) {
    return 9;
  }
  return 0;
}

function main(): i32 {
  let r: i32 = ints();
  if (r != 0) {
    return r;
  }
  return strs();
}
`

func checkOrdmapDeep(t *testing.T, target string, run censusRun) {
	t.Helper()
	stdout, stderr, exit := run(t, ordmapDeepSrc)
	if exit != 0 || strings.TrimSpace(stdout) != "" {
		t.Fatalf("%s: exit %d, stdout %q, want 0; stderr: %s", target, exit, stdout, stderr)
	}
	if a, f, live := parseLeakCheckLine(t, stderr); a != f || live != 0 {
		t.Fatalf("%s: allocs=%d frees=%d live_bytes=%d, want balanced", target, a, f, live)
	}
}

func TestX86_64OrdmapDeep(t *testing.T) {
	checkOrdmapDeep(t, "x86-64", runLeakCheckX86_64)
}

func TestArm64OrdmapDeep(t *testing.T) {
	checkOrdmapDeep(t, "arm64", runLeakCheckArm64)
}

func TestWASMOrdmapDeep(t *testing.T) {
	checkOrdmapDeep(t, "wasm", func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
