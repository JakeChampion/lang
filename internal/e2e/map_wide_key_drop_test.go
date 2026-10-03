package e2e

// A map whose key boxes into a cell (an i64 key on wasm32) freed its value
// cells, buffer and handle at drop but not its key cells, one per entry, on
// the native wasm backend (#11295). Each drop site is here: a local, a struct
// field, and a Map[] element.

import "testing"

const mapWideKeyDropProg = `
import "core/map";

struct Holder { m: Map[i64, i64] }

function fill(n: i32): Map[i64, i64] {
    let m: Map[i64, i64] = Map {};
    let i: i32 = 0;
    while (i < n) { m = m.insert(i as i64, 1); i = i + 1; }
    return m;
}

function main(): i32 {
    let m: Map[i64, i64] = fill(30);
    m = m.without(3).0;
    let h: Holder = Holder { m: fill(5) };
    let ms: Map[i64, i64][] = [fill(4), fill(6)];
    let u: Map[u64, string] = Map {};
    u = u.insert(7, "a" + "b");
    if (m.len() != 29 || h.m.len() != 5 || ms[1].len() != 6 || u.len() != 1) { return 1; }
    return 42;
}
`

func TestMapWideKeyDropInterp(t *testing.T) {
	if got := runInterpExit(t, mapWideKeyDropProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapWideKeyDropNativeWasmCensus(t *testing.T) {
	_, stderr, code := runLeakCheckWasm(t, mapWideKeyDropProg, false)
	if code != 42 && code != 0 {
		t.Fatalf("exit=%d, want the program's own 42 (or wasm's 0)\n%s", code, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("no allocations — the program is not running")
	}
	if allocs != frees || live != 0 {
		t.Errorf("wide map keys leak at drop: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestMapWideKeyDropSelfHostX86_64(t *testing.T) {
	_, stderr, code := runSanitizeX86_64(t, mapWideKeyDropProg)
	checkMapTempReceiver(t, stderr, code)
}

func TestMapWideKeyDropSelfHostArm64(t *testing.T) {
	_, stderr, code := runSanitizeArm64(t, mapWideKeyDropProg)
	checkMapTempReceiver(t, stderr, code)
}
