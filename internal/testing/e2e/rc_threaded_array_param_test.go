package e2e

import (
	"testing"
)

func threadedArrayParamSrc(n, twoN string) string {
	return `function le32(buf: i32[], v: i32): i32[] {
    buf = buf.append(v & 255);
    buf = buf.append((v >> 8) & 255);
    return buf;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < ` + n + `) { acc = le32(acc, i); i = i + 1; }
    if (acc.len() != ` + twoN + `) { return 254; }
    if (acc[0] != 0 || acc[2] != 1 || acc[6] != 3 || acc[7] != 0) { return 253; }
    if (__rc_underflow_count() != 0) { return 200; }
    if (__arr_push_shared_count() != 0) { return 201; }
    return ((__heap_bump_bytes() as i32) - before) / (acc.len() * 4);
}`
}

const threadedArrayParamOverheadCeiling = 8

func checkThreadedArrayParam(t *testing.T, backend string, n, got int) {
	t.Helper()
	switch got {
	case 254:
		t.Fatalf("%s: N=%d wrong accumulator length — the threading itself is broken", backend, n)
	case 253:
		t.Fatalf("%s: N=%d wrong accumulator CONTENTS — an append wrote to a buffer the caller no longer holds", backend, n)
	case 200:
		t.Fatalf("%s: N=%d __rc_underflow_count() != 0 — the overwrite dec released the caller's "+
			"reference (the #6021 count-steal)", backend, n)
	case 201:
		t.Fatalf("%s: N=%d __arr_push_shared_count() != 0 — an append copied a buffer that had room, "+
			"so something upstream is holding an extra reference across it", backend, n)
	}
	if got > threadedArrayParamOverheadCeiling {
		t.Errorf("%s: N=%d allocated %d arena bytes per live byte, over the %d ceiling — the threaded "+
			"array param is copying the whole buffer per append instead of growing it in place",
			backend, n, got, threadedArrayParamOverheadCeiling)
	}
}

var threadedArrayParamSizes = []struct {
	n    int
	src  string
	twoN string
}{
	{200, "200", "400"},
	{800, "800", "1600"},
}

func TestX86_64ThreadedArrayParamBounded(t *testing.T) {
	for _, sz := range threadedArrayParamSizes {
		_, got := compileAndRunX86_64FreeOn(t, threadedArrayParamSrc(sz.src, sz.twoN))
		checkThreadedArrayParam(t, "x86-64-linux", sz.n, got)
	}
}

func TestWASMThreadedArrayParamBounded(t *testing.T) {
	for _, sz := range threadedArrayParamSizes {
		got := runWasm(t, threadedArrayParamSrc(sz.src, sz.twoN))
		checkThreadedArrayParam(t, "wasm32-wasi", sz.n, got)
	}
}
