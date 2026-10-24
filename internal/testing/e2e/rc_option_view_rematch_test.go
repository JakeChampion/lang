package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func optionViewRematchSrc(end int) string {
	return fmt.Sprintf(`import "std/i32";

function check(): i32 {
    let o: Option[str] = "hello"[1:%d];
    match (o) {
        Some(sl) => {
            if (sl != "ell" || sl.len() != 3) { return 16; }
        },
        None => { return 17; }
    }
    match (o) {
        Some(s2) => { if (s2.len() != 3) { return 18; } },
        None => { return 19; }
    }
    return 0;
}

function main(): i32 {
    print(check().to_string());
    return 0;
}`, end)
}

func checkOptionViewRematch(t *testing.T, target string, run censusRun) {
	t.Helper()
	for _, tc := range []struct{ end, want int }{{4, 0}, {3, 16}, {9, 17}} {
		stdout, stderr, exit := run(t, optionViewRematchSrc(tc.end))
		if exit != 0 || !strings.HasPrefix(stdout, strconv.Itoa(tc.want)+"\n") {
			t.Fatalf("%s end=%d: exit %d, stdout %q, want %d first; stderr: %s", target, tc.end, exit, stdout, tc.want, stderr)
		}
		if a, f, live := parseLeakCheckLine(t, stderr); a != f || live != 0 {
			t.Errorf("%s end=%d: allocs=%d frees=%d live_bytes=%d, want balanced", target, tc.end, a, f, live)
		}
	}
}

func TestX86_64OptionViewRematch(t *testing.T) {
	checkOptionViewRematch(t, "x86-64", runLeakCheckX86_64)
}

func TestArm64OptionViewRematch(t *testing.T) {
	checkOptionViewRematch(t, "arm64", runLeakCheckArm64)
}

func TestWASMOptionViewRematch(t *testing.T) {
	checkOptionViewRematch(t, "wasm", func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
