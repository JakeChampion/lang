package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// An alias whose retain was cancelled must still see the original map after
// a mutator consumes the source's last syntactic use. Both assertion forms
// must preserve immutable values and reclaim every map.
func TestMapBorrowedAliasCensus(t *testing.T) {
	runMapBorrowedAliasCases(t, func(t *testing.T, program string) {
		stdout, stderr, code := runLeakCheckWasm(t, program, false)
		if code != 0 || strings.TrimSpace(stdout) != "0" {
			t.Fatalf("exit=%d stdout=%q, want 0\n%s", code, stdout, stderr)
		}
		allocs, frees, live := parseLeakCheckLine(t, stderr)
		if allocs == 0 || allocs != frees || live != 0 {
			t.Fatalf("map alias cleanup: allocs=%d frees=%d live=%d", allocs, frees, live)
		}
	})
}

func TestMapBorrowedAliasInterp(t *testing.T) {
	runMapBorrowedAliasCases(t, func(t *testing.T, program string) {
		program = strings.Replace(program, "return __rc_underflow_count();", "return 0;", 1)
		if got := runInterpExit(t, program); got != 0 {
			t.Fatalf("map alias changed: exit=%d", got)
		}
	})
}

func TestMapBorrowedAliasSelfHostX86_64(t *testing.T) {
	runMapBorrowedAliasCases(t, func(t *testing.T, program string) {
		checkSanitizedBalanced(t, program, 0, runSanitizeX86_64)
	})
}

func TestMapBorrowedAliasSelfHostArm64(t *testing.T) {
	runMapBorrowedAliasCases(t, func(t *testing.T, program string) {
		checkSanitizedBalanced(t, program, 0, runSanitizeArm64)
	})
}

func runMapBorrowedAliasCases(t *testing.T, run func(*testing.T, string)) {
	t.Helper()
	for _, keyType := range []string{"i32", "i64", "u64"} {
		t.Run(keyType, func(t *testing.T) {
			src := fmt.Sprintf(`import "core/map";
struct Holder { m: Map[%[1]s, i32] }
@noinline function make(n: i32): Map[%[1]s, i32] {
    let m: Map[%[1]s, i32] = Map {};
    return m.insert(n as %[1]s, n).insert((n + 1) as %[1]s, n + 1);
}
function check(n: i32): i32 {
    let m = make(n);
    let alias = m;
    let changed = m.insert((n + 2) as %[1]s, n + 2);
    let (removed, found) = changed.without(n as %[1]s);
    if (!found) { return 11; }
    if (removed.len() != 2) { return 20 + removed.len(); }
    if (alias.len() != 2) { return 30 + alias.len(); }
    if (alias.get_or(n as %[1]s, 0 - 1) != n) { return 2; }
    let empty = removed.cleared();
    if (empty.len() != 0 || alias.get_or((n + 1) as %[1]s, 0 - 1) != n + 1) { return 3; }
    let holder = Holder { m: make(n) };
    let rows: Map[%[1]s, i32][] = [make(n), make(n + 2)];
    if (holder.m.len() != 2 || rows[1].len() != 2) { return 4; }
    return 0;
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 20) {
        let result = check(i);
        if (result != 0) { return result; }
        i = i + 1;
    }
    return __rc_underflow_count();
}`, keyType)
			for _, combined := range []bool{false, true} {
				t.Run(fmt.Sprintf("combined=%t", combined), func(t *testing.T) {
					program := src
					if combined {
						program = strings.Replace(program, `if (!found) { return 11; }
    if (removed.len() != 2) { return 20 + removed.len(); }
    if (alias.len() != 2) { return 30 + alias.len(); }`, `if (!found || removed.len() != 2 || alias.len() != 2) { return 1; }`, 1)
					}
					run(t, program)
				})
			}
		})
	}
}
