package e2e

import (
	"testing"
)

func selfReassignFieldBumpSrc(n string) string {
	return `struct Blk { insts: i32[] }
struct St { cur: Blk }
function (s: St) emit(x: i32): St { return St { cur: Blk { insts: s.cur.insts.append(x) } }; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let s: St = St { cur: Blk { insts: [] } };
    let i: i32 = 0;
    while (i < ` + n + `) { s = s.emit(i); i = i + 1; }
    if (s.cur.insts.len() != ` + n + `) { return 999; }
    return ((__heap_bump_bytes() as i32) - before) / 64;
}`
}

const selfReassignFieldSoundSrc = `struct Blk { insts: i32[] }
struct St { cur: Blk }
function (s: St) emit(x: i32): St { return St { cur: Blk { insts: s.cur.insts.append(x) } }; }
function main(): i32 {
    let s: St = St { cur: Blk { insts: [] } };
    let i: i32 = 0;
    while (i < 200) { s = s.emit(i * 2); i = i + 1; }
    if (s.cur.insts.len() != 200) { return 100; }
    if (s.cur.insts[199] != 398) { return 101; }
    return __rc_underflow_count();
}`

const selfReassignStringFieldSoundSrc = `struct Acc { tag: string, xs: i32[] }
function (a: Acc) step(v: i32): Acc { return Acc { tag: a.tag, xs: a.xs.append(v) }; }
function tagfor(i: i32): string { if (i % 2 == 0) { return "even-ish-longer"; } return "odd-ish-longer"; }
function main(): i32 {
    let a: Acc = Acc { tag: "seed-" + tagfor(0), xs: [] };
    let i: i32 = 0;
    while (i < 200) { a = a.step(i); i = i + 1; }
    if (a.xs.len() != 200) { return 100; }
    if (a.tag.len() < 5) { return 101; }
    return __rc_underflow_count();
}`

func selfReassignStringFieldBumpSrc(n string) string {
	return `struct Acc { tag: string, xs: i32[] }
function (a: Acc) step(v: i32): Acc { return Acc { tag: a.tag, xs: a.xs.append(v) }; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let a: Acc = Acc { tag: "seed-" + "tag", xs: [] };
    let i: i32 = 0;
    while (i < ` + n + `) { a = a.step(i); i = i + 1; }
    if (a.xs.len() != ` + n + `) { return 999; }
    if (a.tag.len() != 8) { return 998; }
    return ((__heap_bump_bytes() as i32) - before) / 64;
}`
}

const ssaAccumSoundSrc = `struct Bld { name: string, insts: i32[] }
function (s: Bld) emit(x: i32): Bld { return Bld { name: s.name, insts: s.insts.append(x) }; }
function build(s: Bld, n: i32): Bld {
    let i: i32 = 0;
    while (i < n) { s = s.emit(i * 2); i = i + 1; }
    return s;
}
function main(): i32 {
    let s: Bld = build(Bld { name: "seed", insts: [] }, 200);
    if (s.insts.len() != 200) { return 100; }
    if (s.insts[199] != 398) { return 101; }
    return __rc_underflow_count();
}`

func assertSubQuadratic(t *testing.T, backend string, n1, n2 int) {
	t.Helper()
	if n1 <= 0 {
		t.Errorf("%s: expected non-zero bump, got %d", backend, n1)
		return
	}
	if n2 > n1*3 {
		t.Errorf("%s: bump grew %dx (N -> 2N: %d -> %d); want ~2x (O(N)), not quadratic", backend, n2/n1, n1, n2)
	}
}

func TestX86_64SelfReassignFieldSound(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, selfReassignFieldSoundSrc); code != 0 {
		t.Errorf("string-free accumulator: got %d, want 0 (100/101=value, >0=over-release)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, selfReassignStringFieldSoundSrc); code != 0 {
		t.Errorf("string-field accumulator: got %d, want 0 (UAF regression guard)", code)
	}
}

func TestX86_64SelfReassignStringFieldBounded(t *testing.T) {
	_, n1 := compileAndRunX86_64FreeOn(t, selfReassignStringFieldBumpSrc("200"))
	_, n2 := compileAndRunX86_64FreeOn(t, selfReassignStringFieldBumpSrc("400"))
	assertSubQuadratic(t, "x86-64-linux", n1, n2)
}

func TestArm64SelfReassignFieldSound(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, selfReassignFieldSoundSrc); code != 0 {
		t.Errorf("string-free accumulator: got %d, want 0", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, selfReassignStringFieldSoundSrc); code != 0 {
		t.Errorf("string-field accumulator: got %d, want 0", code)
	}
}

func TestArm64SelfReassignStringFieldBounded(t *testing.T) {
	_, n1 := compileAndRunArm64FreeOn(t, selfReassignStringFieldBumpSrc("200"))
	_, n2 := compileAndRunArm64FreeOn(t, selfReassignStringFieldBumpSrc("400"))
	assertSubQuadratic(t, "arm64-linux", n1, n2)
}

func TestWASMSelfReassignFieldBounded(t *testing.T) {
	n1 := runWasm(t, selfReassignFieldBumpSrc("200"))
	n2 := runWasm(t, selfReassignFieldBumpSrc("400"))
	assertSubQuadratic(t, "wasm32-wasi", n1, n2)
	if got := runWasm(t, selfReassignFieldSoundSrc); got != 0 {
		t.Errorf("string-free accumulator: got %d, want 0", got)
	}
	if got := runWasm(t, selfReassignStringFieldSoundSrc); got != 0 {
		t.Errorf("string-field accumulator: got %d, want 0 (UAF regression guard)", got)
	}
	sn1 := runWasm(t, selfReassignStringFieldBumpSrc("200"))
	sn2 := runWasm(t, selfReassignStringFieldBumpSrc("400"))
	assertSubQuadratic(t, "wasm string-field", sn1, sn2)
	if got := runWasm(t, ssaAccumSoundSrc); got != 0 {
		t.Errorf("threaded-param accumulator: got %d, want 0 (over-release)", got)
	}
}

const unionThreadedParamSrc = `struct Leaf { v: i32[] }
struct Wrap { operand: Ex, n: i32 }
type Ex = Leaf | Wrap;

function mkwrap(operand: Ex, n: i32): Ex { return Wrap { operand: operand, n: n }; }

function post(base: Ex): Ex {
    base = mkwrap(base, 1);
    return base;
}

function build(seed: i32): Ex {
    let l: Ex = Leaf { v: [seed, seed + 1, seed + 2] };
    return post(l);
}

function inner(x: Ex): i32 {
    match (x) {
        Leaf(lf) => { return lf.v[0]; },
        Wrap(w2) => { return -1; },
    }
}

function payload(e: Ex): i32 {
    match (e) {
        Wrap(w) => { return inner(w.operand); },
        Leaf(lf) => { return -2; },
    }
}

function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let e: Ex = build(i);
        let junk: i32[] = [9, 9, 9];
        acc = acc + payload(e);
        i = i + 1;
    }
    if (acc != 1225) { return 100; }
    return __rc_underflow_count();
}`

func TestX86_64UnionThreadedParam(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, unionThreadedParamSrc); code != 0 {
		t.Errorf("union threaded param: got %d, want 0 (100=corrupted payload, >0=over-release)", code)
	}
}

func TestArm64UnionThreadedParam(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, unionThreadedParamSrc); code != 0 {
		t.Errorf("union threaded param: got %d, want 0 (100=corrupted payload, >0=over-release)", code)
	}
}

func TestWASMUnionThreadedParam(t *testing.T) {
	if got := runWasm(t, unionThreadedParamSrc); got != 0 {
		t.Errorf("union threaded param: got %d, want 0 (100=corrupted payload, >0=over-release)", got)
	}
}
