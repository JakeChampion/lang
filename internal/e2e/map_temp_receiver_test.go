package e2e

// A Map that a call returns and nothing binds (#11246): a receiver
// (`mk().len()`, `mk().get(k)`, `mk().keys()`), an argument to a borrowing
// parameter, a mutator's receiver, and a `for` iterand. Each one leaked the
// whole table on the native backends; the census leg below is the native one.
// The interpreter and the self-host compiler were right throughout.

import (
	"strings"
	"testing"
)

const mapTempReceiverDefs = `
import "core/map";
import "std/i64";
import "std/i32";

@noinline function mk(): Map[i64, i64] { let m: Map[i64, i64] = Map {}; return m.insert(1, 10).insert(2, 20); }
@noinline function mka(): Map[i32, i32[]] { let m: Map[i32, i32[]] = Map {}; return m.insert(1, [2, 3]); }
@noinline function mks(): Map[string, string] { let m: Map[string, string] = Map {}; return m.insert("k" + "1", "v" + "1"); }
@noinline function look(m: Map[i64, i64]): i32 { return m.len(); }

function check(): i32 {
    let i: i32 = 0;
    let t: i32 = 0;
    while (i < 20) {
        t = t + mk().len() + look(mk());
        if (mk().has(2)) { t = t + 1; }
        match (mk().get(1)) { Some(v) => { t = t + (v as i32); }, None => {} }
        t = t + (mk().get_or(9, 5) as i32);
        t = t + (mk().keys().len() as i32) + (mk().values().len() as i32);
        t = t + mka().get_or(1, [0]).len() + (mks().keys().len() as i32);
        t = t + mk().insert(3, 30).len();
        for (k, v) in mk() { t = t + (v as i32); }
        i = i + 1;
    }
    // Per round: 2 + 2 + 1 + 10 + 5 + 2 + 2 + 2 + 1 + 3 + 30 = 60.
    if (t != 1200) { return 1; }
    return 0;
}
`

const mapTempReceiverProg = mapTempReceiverDefs + `
function main(): i32 {
    let r: i32 = check();
    if (r != 0) { return r; }
    return 42;
}
`

// 42 when clean; every rc underflow the run recorded adds to it.
const mapTempReceiverRcProg = mapTempReceiverDefs + `
function main(): i32 {
    let r: i32 = check();
    if (r != 0) { return r; }
    return 42 + __rc_underflow_count();
}
`

func TestMapTempReceiverInterp(t *testing.T) {
	if got := runInterpExit(t, mapTempReceiverProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

// The native wasm build under the leak census: the leg the fix is for, since
// native x86-64 and arm64 builds no longer run here (#4451).
func TestMapTempReceiverNativeWasmCensus(t *testing.T) {
	_, stderr, code := runLeakCheckWasm(t, mapTempReceiverProg, false)
	if code != 42 && code != 0 {
		t.Fatalf("exit=%d, want the program's own 42 (or wasm's 0)\n%s", code, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations — the program is not running")
	}
	if allocs != frees || live != 0 {
		t.Errorf("an unbound map temp leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

// The self-host, under the sanitizer: a leak is a verdict line, a double
// release or a read of a freed handle is fatal.
func TestMapTempReceiverSelfHostX86_64(t *testing.T) {
	_, stderr, code := runSanitizeX86_64(t, mapTempReceiverProg)
	checkMapTempReceiver(t, stderr, code)
}

func TestMapTempReceiverSelfHostArm64(t *testing.T) {
	_, stderr, code := runSanitizeArm64(t, mapTempReceiverProg)
	checkMapTempReceiver(t, stderr, code)
}

func checkMapTempReceiver(t *testing.T, stderr string, code int) {
	t.Helper()
	if code != 42 || strings.Contains(stderr, "fern-sanitizer:") {
		t.Fatalf("got exit %d, want 42 with no sanitizer finding\nstderr: %s", code, stderr)
	}
}

func TestMapTempReceiverSelfHostWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapTempReceiverRcProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}
