package e2e

import (
	"fmt"
	"testing"
)

type cliffBytesCase struct {
	name      string
	g         string
	appends   int
	wantCount int
	wantBytes int
}

var cliffBytesCases = []cliffBytesCase{
	{"clean_self_append", `function g(a: i32[], v: i32): i32[] { a = a.append(v); return a; }`, 1, 0, 0},

	{"live_alias_append", `function g(b: i32[], v: i32): i32[] { let k: i32[] = b; let r: i32[] = b.append(v); if (k.len() + 1 != r.len()) { return k; } return r.append(v + 1); }`, 2, 4, 240},
}

func (c cliffBytesCase) src(probe string) string {
	wantLen := 7 * c.appends
	return fmt.Sprintf(`function f(b: i32[], v: i32): i32[] { return b.append(v); }
%s
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 7) { acc = g(acc, i); i = i + 1; }
    if (acc.len() != %d) { return 254; }
    return %s;
}`, c.g, wantLen, probe)
}

func (c cliffBytesCase) countSrc() string { return c.src("__arr_push_shared_count()") }
func (c cliffBytesCase) bytesSrc() string { return c.src("__arr_push_shared_bytes() as i32") }

func (c cliffBytesCase) check(t *testing.T, backend string, gotCount, gotBytes int) {
	t.Helper()
	if gotCount == 254 || gotBytes == 254 {
		t.Fatalf("%s %s: driver returned 254 — the accumulator computed the WRONG "+
			"contents; the readings below it are meaningless until that is fixed", backend, c.name)
	}
	if gotCount != c.wantCount {
		t.Errorf("%s %s: __arr_push_shared_count() = %d, want %d",
			backend, c.name, gotCount, c.wantCount)
	}
	if gotBytes != c.wantBytes {
		t.Errorf("%s %s: __arr_push_shared_bytes() = %d, want %d — the weight and the "+
			"count are emitted at the same site and must move together",
			backend, c.name, gotBytes, c.wantBytes)
	}
}

func TestX86_64ArrPushCliffBytes(t *testing.T) {
	for _, c := range cliffBytesCases {
		t.Run(c.name, func(t *testing.T) {
			_, count := compileAndRunX86_64FreeOn(t, c.countSrc())
			_, bytes := compileAndRunX86_64FreeOn(t, c.bytesSrc())
			c.check(t, "x86-64-linux", count, bytes)
		})
	}
}

func TestInterpArrPushCliffBytesZero(t *testing.T) {
	c := cliffBytesCases[1]
	if got := runInterpExit(t, c.countSrc()); got != 0 {
		t.Errorf("-interp __arr_push_shared_count() = %d, want 0", got)
	}
	if got := runInterpExit(t, c.bytesSrc()); got != 0 {
		t.Errorf("-interp __arr_push_shared_bytes() = %d, want 0", got)
	}
}
