package e2ecompiler

import "testing"

// retire takes the fields of a record that outlives the run, because an
// element of its list is read after it. lock ends a run where its donor
// dies and becomes the reuse token of the next construction. main calls
// each with a unique argument and with a shared one.
const testedTakeSrc = `
struct St { id: i64, closed: boolean, buf: u8[] }

struct Tab { list: St[], closed: i64[], limits: i64[], dirty: boolean[], name: string, retired: i32 }

@noinline
function retire(s: Tab, i: i32): Tab {
  let st: St = s.list[i];
  if (st.closed) {
    return s;
  }
  let cur: Tab = Tab { ...s, retired: s.retired + 1 };
  if ((st.id & 1) == 0) {
    let t: i32 = (st.id & 3) as i32;
    cur = Tab { ...cur, closed: cur.closed.with(t, cur.closed[t] + 1) };
    if (cur.closed[t] - cur.limits[t] >= 2) {
      cur = Tab { ...cur, limits: cur.limits.with(t, cur.closed[t]), dirty: cur.dirty.with(t, true) };
    }
  }
  return Tab { ...cur, list: cur.list.with(i, St { ...cur.list[i], closed: true, buf: [] }) };
}

struct Idx { names: string[], head: i32[], next: i32[] }

struct Sig { name: string, arity: i32 }

struct Sigs { sigs: Sig[], index: Idx, raw: Sig[], raw_allowed: boolean }

struct Env { sigs: Sigs, structs: string[], unions: string[], methods: Sig[], imports: string[], impls: i64[], reqs: string[] }

@noinline
function made(p: string): Env {
  let s: Sigs = Sigs { sigs: [Sig { name: p, arity: 1 }], index: Idx { names: [p], head: [0], next: [0 - 1] }, raw: [], raw_allowed: true };
  return Env { sigs: s, structs: [p], unions: [], methods: [], imports: ["i"], impls: [1 as i64], reqs: [] };
}

@noinline
function lock(e: Env, raw: boolean): Env {
  if (raw) {
    return e;
  }
  return Env { ...e, sigs: Sigs { ...e.sigs, raw_allowed: false } };
}

function table(): Tab {
  return Tab { list: [], closed: [0, 0, 0, 0], limits: [0, 0, 0, 0], dirty: [false, false, false, false], name: "t", retired: 0 };
}

function main(): i32 {
  let bad: i32 = 0;
  let t: Tab = table();
  for k in 0..200 {
    t = Tab { ...t, list: t.list.append(St { id: k as i64, closed: false, buf: [k as u8] }) };
    t = retire(t, k);
  }
  if (t.retired != 200 || t.closed[0] != 50 || t.closed[2] != 50 || !t.dirty[2] || t.list[7].buf.len() != 0) {
    bad = bad + 1;
  }
  let kept: Tab = Tab { ...table(), list: [St { id: 4 as i64, closed: false, buf: [9 as u8] }] };
  let after: Tab = retire(kept, 0);
  if (kept.retired != 0 || kept.list[0].closed || kept.list[0].buf.len() != 1 || after.retired != 1 || !after.list[0].closed || after.closed[0] != 1) {
    bad = bad + 2;
  }
  let held: Env = made("x");
  let locked: Env = lock(held, false);
  if (locked.sigs.raw_allowed || !held.sigs.raw_allowed || locked.sigs.index.names[0] != "x" || held.structs[0] != "x" || locked.sigs.sigs[0].arity != 1) {
    bad = bad + 4;
  }
  let fresh: Env = lock(made("y"), false);
  if (fresh.sigs.raw_allowed || fresh.sigs.index.names[0] != "y" || fresh.imports[0] != "i") {
    bad = bad + 8;
  }
  return bad;
}
`

// A run of takes from one box reads the box's uniqueness once, not once a take.
func TestSelfHostTestedTakeShapeX86_64(t *testing.T) {
	emit, run := x86ShapeHarness(t)
	asm := emit(t, testedTakeSrc)
	for fn, want := range map[string]int{"retire": 10, "lock": 4} {
		if got := rcIsUniqueSites(fnText(t, asm, fn)); got != want {
			t.Errorf("%s: %d uniqueness tests, want %d", fn, got, want)
		}
	}
	if got := run(t, "tested", asm); got != 0 {
		t.Errorf("exit = %d, want 0 (bit 1: unique table, 2: shared table, 4: shared env, 8: fresh env)", got)
	}
	cli := newStrictCLI(t)
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", testedTakeSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostTestedTakeArm64(t *testing.T) {
	emit, run := arm64ShapeHarness(t)
	if got := run(t, "tested", emit(t, testedTakeSrc)); got != 0 {
		t.Errorf("exit = %d, want 0 (bit 1: unique table, 2: shared table, 4: shared env, 8: fresh env)", got)
	}
}

func TestSelfHostTestedTakeWasm(t *testing.T) {
	cli := newStrictCLI(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", testedTakeSrc)); code != 0 {
		t.Fatalf("wasm: exit %d, stdout %q; want 0", code, out)
	}
}
