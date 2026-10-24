package e2e

import (
	"fmt"
	"strings"
	"testing"
)

func ordmapReuseSrc(rounds int, shared bool) string {
	keep := ""
	check := "false"
	if shared {
		keep = "let old: ordmap.OrdMap[i32, i32] = m;"
		check = "old.get_or(7, 0 - 1) != r"
	}
	return fmt.Sprintf(`import "std/ordmap";

function main(): i32 {
    let m: ordmap.OrdMap[i32, i32] = ordmap.ordmap_new();
    let i: i32 = 0;
    while (i < 500) {
        m = m.insert(i * 7, 0);
        i = i + 1;
    }
    let r: i32 = 0;
    while (r < %d) {
        %s
        let j: i32 = 0;
        while (j < 500) {
            m = m.insert(j * 7, r + 1);
            j = j + 1;
        }
        if (%s || m.get_or(7, 0 - 1) != r + 1 || m.len() != 500) { return 1; }
        r = r + 1;
    }
    return __rc_underflow_count();
}`, rounds, keep, check)
}

func checkOrdmapReuse(t *testing.T, target string, run censusRun) {
	t.Helper()
	census := func(rounds int, shared bool) int64 {
		stdout, stderr, exit := run(t, ordmapReuseSrc(rounds, shared))
		if exit != 0 || strings.TrimSpace(stdout) != "" && strings.TrimSpace(stdout) != "0" {
			t.Fatalf("%s rounds=%d shared=%v: exit %d, stdout %q, want 0; stderr: %s", target, rounds, shared, exit, stdout, stderr)
		}
		a, f, live := parseLeakCheckLine(t, stderr)
		if a != f || live != 0 {
			t.Fatalf("%s rounds=%d shared=%v: allocs=%d frees=%d live_bytes=%d, want balanced", target, rounds, shared, a, f, live)
		}
		t.Logf("%s rounds=%d shared=%v: allocs=%d", target, rounds, shared, a)
		return a
	}
	if lo, hi := census(2, false), census(8, false); lo != hi {
		t.Errorf("%s unique: %d allocations at 2 rounds and %d at 8, want the same: a re-insert allocated", target, lo, hi)
	}
	if lo, hi := census(2, true), census(8, true); hi-lo < 6*500 {
		t.Errorf("%s shared: %d allocations at 2 rounds and %d at 8, want at least %d more: a kept map must be copied", target, lo, hi, 6*500)
	}
}

func TestInterpOrdmapReuseValues(t *testing.T) {
	for _, shared := range []bool{false, true} {
		if got := runInterpExit(t, ordmapReuseSrc(3, shared)); got != 0 {
			t.Errorf("shared=%v: got %d, want 0", shared, got)
		}
	}
}

func TestX86_64OrdmapReuse(t *testing.T) {
	checkOrdmapReuse(t, "x86-64", runLeakCheckX86_64)
}

func TestArm64OrdmapReuse(t *testing.T) {
	checkOrdmapReuse(t, "arm64", runLeakCheckArm64)
}

func TestWASMOrdmapReuse(t *testing.T) {
	checkOrdmapReuse(t, "wasm", func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
