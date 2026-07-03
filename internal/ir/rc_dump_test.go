package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// Pins the rcPlan dump format — the contract of the #4482 differential
// harness. The self-host dump driver must emit exactly this rendering, so
// any change here is a cross-compiler format change and must be mirrored.
// The program exercises the tables: a consumed-threaded param (thread), a
// moved local + move site (mover), and a precise drop (dropper).
func TestRcPlanDumpFormat(t *testing.T) {
	dumps := map[string]string{}
	ir.RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { ir.RcPlanHook = nil }()

	lowerForTest(t, `struct Ctx { name: string, n: i32 }
function thread(c: Ctx): i32 {
	c = Ctx { name: "x", n: c.n + 1 };
	return c.n;
}
function mover(): i32 {
	var a: i32[] = [1, 2, 3];
	var b: i32[] = a;
	return b[0];
}
function dropper(): i32 {
	var big: i32[] = [1, 2, 3, 4];
	var s: i32 = big[0];
	return s + 1;
}
function main(): i32 { return thread(Ctx { name: "a", n: 1 }) + mover() + dropper(); }`)

	check := func(fn string, wantLines ...string) {
		t.Helper()
		got := dumps[fn]
		for _, w := range wantLines {
			if !strings.Contains(got, w) {
				t.Errorf("%s dump missing %q; got:\n%s", fn, w, got)
			}
		}
	}
	// thread: the string-bearing struct param is promoted consumed-threaded
	// (and thereby freeEligible).
	check("thread", "consumedParams: c", "freeEligible: c")
	// mover: `var b = a` is a's last use — a moves into b.
	check("mover", "movedLocals: a", "moveSites: ")
	// dropper: big's last use is `big[0]` at top-level statement 1 — the
	// precise drop lands right after it.
	check("dropper", "freeEligible: big", "preciseDrops: 1=big")

	if len(dumps) == 0 {
		t.Fatal("RcPlanHook never fired")
	}
}
