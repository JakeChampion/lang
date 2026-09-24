package e2e

import (
	"strings"
	"testing"
)

// A `.with` chain assigned back to its receiver writes one array in place
// (#9702). Its inner link used to copy, since the receiver looked live to the
// outer link, so `b = b.with(i, x).with(j, y)` bought a box per evaluation
// where the same writes as two statements bought none.
//
// An outer link that reads the receiver is the exception, for an array and
// for a map alike: it runs after the inner store, so it must see the old
// element, and the chain copies. `own b`
// assigned from a chain is the shape whose old-value release freed the buffer
// the chain had just written.
const withChainInPlaceSrc = `import "core/map";

function chain(own b: i32[], x: i32, y: i32): i32[] {
	var i: i32 = 0;
	while (i < 4) {
		b = b.with(i, x + i).with(i + 4, y + i);
		i = i + 1;
	}
	return b;
}

function reads_receiver(): i32 {
	var b: i32[] = [7, 8, 9, 10];
	b = b.with(0, 3).with(1, b[0]);
	return b[0] * 10 + b[1];
}

function map_reads_receiver(): i32 {
	var m: Map[i32, i32] = map_new(4);
	m = m.insert(1, 5);
	m = m.insert(1, 3).insert(2, m.get_or(1, 0));
	return m.get_or(1, 0) * 10 + m.get_or(2, 0);
}

function own_param(own b: i32[]): i32[] {
	b = b.with(0, 1).with(1, 2);
	return b;
}

function main(): i32 {
	var b: i32[] = [0, 0, 0, 0, 0, 0, 0, 0];
	var at: i64 = __heap_alloc_count();
	var n: i32 = 0;
	while (n < 50) { b = chain(b, n, 100); n = n + 1; }
	if (__heap_alloc_count() - at != (0 as i64)) { return 90; }
	if (b[0] != 49 || b[3] != 52 || b[4] != 100 || b[7] != 103) { return 91; }

	if (reads_receiver() != 37) { return 92; }
	if (map_reads_receiver() != 35) { return 94; }

	var c: i32[] = own_param([0, 0, 0]);
	if (c[0] != 1 || c[1] != 2 || c[2] != 0) { return 93; }

	return 42;
}
`

// 90 names a chain that still copies; 91-94 mean a result is wrong.
func TestWithChainWritesInPlace(t *testing.T) {
	check := func(t *testing.T, code int, out string) {
		t.Helper()
		if code != 42 {
			t.Errorf("exit %d, want 42 — see the source for what each code names\n%s", code, strings.TrimSpace(out))
		}
	}
	t.Run("x86-64", func(t *testing.T) {
		out, code := compileAndRunX86Native(t, withChainInPlaceSrc)
		check(t, code, out)
	})
	t.Run("x86-64/leakcheck", func(t *testing.T) {
		_, stderr, exit := runLeakCheckX86_64(t, withChainInPlaceSrc)
		checkMapReceiverCensus(t, 42, stderr, exit)
	})
	t.Run("arm64", func(t *testing.T) {
		out, code := compileAndRunArm64FreeOn(t, withChainInPlaceSrc)
		check(t, code, out)
	})
	t.Run("wasm", func(t *testing.T) {
		check(t, runWasm(t, withChainInPlaceSrc), "")
	})
}
