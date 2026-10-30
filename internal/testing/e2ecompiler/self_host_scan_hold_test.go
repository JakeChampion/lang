package e2ecompiler

import (
	"strings"
	"testing"
)

// scan reads each element of a list field of a record it rebuilds in the
// loop. An element that a match changes is read after the call that consumes
// the record, so the element is held. The list itself is read only to reach
// the element. scan_len reads the element again after the call, in the
// call's own block. main calls each with a unique table and with a shared one.
const scanHoldSrc = `
struct T { id: i64, state: i64, name: u8[] }

struct S { list: T[], fit: boolean, n: i64 }

@noinline
function (s: S) add(k: i64): S {
  return S { ...s, fit: k % 3 != 0, n: s.n + k };
}

@noinline
function (s: S) put(i: i32, t: T): S {
  return S { ...s, list: s.list.with(i, t) };
}

@noinline
function scan(s: S): S {
  let cur: S = s;
  let i: i32 = 0;
  while (i < cur.list.len()) {
    let st: T = cur.list[i];
    if (st.state == 1) {
      cur = cur.add(st.id);
      if (cur.fit) {
        cur = cur.put(i, T { ...st, state: 2 });
      }
    }
    i = i + 1;
  }
  return cur;
}

@noinline
function scan_len(s: S): S {
  let cur: S = s;
  for i in 0..cur.list.len() {
    let st: T = cur.list[i];
    if (st.state == 1) {
      cur = cur.add(st.id);
      cur = S { ...cur, n: cur.n + st.name.len() as i64 * 1000 };
    }
  }
  return cur;
}

function table(): S {
  let s: S = S { list: [], fit: true, n: 0 };
  for k in 0..40 {
    s = S { ...s, list: s.list.append(T { id: k as i64, state: (k % 2) as i64, name: [k as u8] }) };
  }
  return s;
}

function main(): i32 {
  let bad: i32 = 0;
  let r: S = scan(table());
  if (r.n != 400 || r.list[1].state != 2 || r.list[3].state != 1 || r.list[0].state != 0 || r.list[5].name[0] != 5) {
    bad = bad + 1;
  }
  let kept: S = table();
  let after: S = scan(kept);
  if (kept.list[1].state != 1 || kept.n != 0 || after.list[1].state != 2 || after.n != 400 || after.list[39].name[0] != 39) {
    bad = bad + 2;
  }
  let l: S = scan_len(table());
  if (l.n != 20400 || l.list[1].state != 1) {
    bad = bad + 4;
  }
  let lk: S = table();
  let la: S = scan_len(lk);
  if (la.n != 20400 || lk.n != 0 || la.list[39].name[0] != 39) {
    bad = bad + 8;
  }
  return bad;
}
`

// The loop holds only the element it reads, and only on the arm that
// consumes the record. It holds no count on the list, and so tests no
// uniqueness to take the element out of it. The one test left is the reuse
// of the element's box for the rebuilt element.
func TestSelfHostScanHoldShapeX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, scanHoldSrc)
	text := fnText(t, asm, "scan")
	if got := rcIsUniqueSites(text); got != 1 {
		t.Errorf("scan: %d uniqueness tests, want 1", got)
	}
	if got := rcIncSites(text); got != 2 {
		t.Errorf("scan: %d retains, want 2 (the element, and its name in the rebuilt element)", got)
	}
	state := strings.Index(text, "cmpq $1, %")
	if state < 0 {
		t.Fatalf("scan: no test of the element's state in:\n%s", text)
	}
	if got := rcIncSites(text[:state]); got != 0 {
		t.Errorf("scan: %d retains before the test of the element's state, want 0: only the arm that consumes the record holds the element", got)
	}
	if got := run(t, "scan", asm); got != 0 {
		t.Errorf("exit = %d, want 0 (bit 1: unique table, 2: shared table, 4 and 8: the same for scan_len)", got)
	}
	cli := newStrictCLI(t)
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", scanHoldSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostScanHoldArm64(t *testing.T) {
	emit, run := arm64ShapeHarness(t)
	if got := run(t, "scan", emit(t, scanHoldSrc)); got != 0 {
		t.Errorf("exit = %d, want 0 (bit 1: unique table, 2: shared table, 4 and 8: the same for scan_len)", got)
	}
}

func TestSelfHostScanHoldWasm(t *testing.T) {
	cli := newStrictCLI(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", scanHoldSrc)); code != 0 {
		t.Fatalf("wasm: exit %d, stdout %q; want 0", code, out)
	}
}
