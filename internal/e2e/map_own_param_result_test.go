package e2e

// A Map threaded through `own` parameters and mutator chains on the native
// backends (#11168).
//
//   - A call with a cast argument, `m = ins(m, i as i64, v)`, read the cast as
//     a borrowed input, so the binding lost its typed drop and the map leaked
//     at exit; bound through `let m2 = …; m = m2;` each COW copy leaked too.
//   - `return m.without(k).0` retained a projection that the read out of the
//     temporary tuple had already retained, so the returned map leaked.
//   - A mutator chain, `m.insert(a, 1).insert(b, 2)`, hands back the
//     receiver's own handle like a single mutator does, but neither a rebind
//     nor a return recognised it: the rebind released the handle it stored
//     back, and the return handed the caller a handle the exit sweep freed.
//
// The interpreter and the self-host compiler were right throughout.

import (
	"strings"
	"testing"
)

const mapOwnParamResultDefs = `
import "core/map";
import "std/i64";
import "std/i32";

@noinline function ins(own m: Map[i64, i64], k: i64, v: i64): Map[i64, i64] { return m.insert(k, v); }
@noinline function del(own m: Map[i64, i64]): Map[i64, i64] { return m.without(1).0; }
@noinline function del_pair(own m: Map[i64, i64]): (Map[i64, i64], i32) { return (m.without(1).0, 4); }
@noinline function chain(own m: Map[i64, i64]): Map[i64, i64] { return m.insert(1, 2).insert(3, 4); }
@noinline function chain_borrowed(m: Map[i64, i64]): i32 { m = m.insert(1, 2).cleared().insert(5, 6); return m.len() as i32; }

function fill(n: i32): Map[i64, i64] {
    let m: Map[i64, i64] = Map {};
    let i: i32 = 0;
    while (i < n) { m = m.insert(i as i64, i as i64); i = i + 1; }
    return m;
}

function check(): i32 {
    let m: Map[i64, i64] = fill(15);
    let i: i32 = 0;
    while (i < 10) { m = ins(m, (i % 15) as i64, (i + 100) as i64); i = i + 1; }
    if (m.len() != 15 || m.get_or(3, 0) != 103) { return 1; }

    let n: Map[i64, i64] = fill(15);
    i = 0;
    while (i < 10) { let n2: Map[i64, i64] = ins(n, i as i64, 7); n = n2; i = i + 1; }
    if (n.len() != 15 || n.get_or(9, 0) != 7) { return 2; }

    let e: Map[i64, i64] = del(fill(3));
    if (e.len() != 2) { return 3; }
    let (d, four) = del_pair(fill(3));
    if (d.len() != 2 || four != 4) { return 4; }

    let f: Map[i64, i64] = chain(fill(1));
    if (f.len() != 3) { return 5; }
    let c: Map[i64, i64] = fill(1);
    i = 0;
    while (i < 4) { c = c.insert(i as i64 + 10, 1).insert(i as i64 + 20, 2); i = i + 1; }
    if (c.len() != 9) { return 6; }
    let b: Map[i64, i64] = fill(2);
    if (chain_borrowed(b) != 1 || b.len() != 2) { return 7; }
    return 0;
}
`

const mapOwnParamResultProg = mapOwnParamResultDefs + `
function main(): i32 {
    let r: i32 = check();
    if (r != 0) { return r; }
    return 42;
}
`

// 42 when clean; every rc underflow the run recorded adds to it.
const mapOwnParamResultRcProg = mapOwnParamResultDefs + `
function main(): i32 {
    let r: i32 = check();
    if (r != 0) { return r; }
    return 42 + __rc_underflow_count();
}
`

func TestMapOwnParamResultInterp(t *testing.T) {
	if got := runInterpExit(t, mapOwnParamResultProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

// Under the sanitizer: a leak is a verdict line, a double release or a read
// of a freed handle is fatal.
func TestMapOwnParamResultSelfHostX86_64(t *testing.T) {
	_, stderr, code := runSanitizeX86_64(t, mapOwnParamResultProg)
	checkMapOwnParamResult(t, stderr, code)
}

func TestMapOwnParamResultSelfHostArm64(t *testing.T) {
	_, stderr, code := runSanitizeArm64(t, mapOwnParamResultProg)
	checkMapOwnParamResult(t, stderr, code)
}

func checkMapOwnParamResult(t *testing.T, stderr string, code int) {
	t.Helper()
	if code != 42 || strings.Contains(stderr, "fern-sanitizer:") {
		t.Fatalf("got exit %d, want 42 with no sanitizer finding\nstderr: %s", code, stderr)
	}
}

func TestMapOwnParamResultWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapOwnParamResultRcProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}
