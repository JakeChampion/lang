package e2e

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The array-push element move used to be disabled by ANY `return` / `break` /
// `continue` in the loop body, so a guard clause on a provably dead branch made
// every pushed element a copy: measured on main, 1280 B/round on arm64-darwin
// and 960 B/round on wasm at 200 vs 400 rounds, exactly 2.0x per doubling.
// Parsers and tokenisers are built out of that shape, so this covered most of
// the accumulate-into-an-array code in the language (#6533).
//
// The probe runs the same churn TWICE and subtracts the bump high-water across
// the second one, so the figure is what the FIRST churn failed to give back —
// a leak, not a working set. n=2 keeps the pre-fix figure inside an exit code
// (128 B on both natives, 96 B on wasm) so the number itself is pinned, not
// just its sign; anything past 200 saturates rather than wrapping into a small
// value.
//
// The answer is checked in-process on both churns: a move that fired where it
// must not is a use-after-free, and a corrupted element would show up as the
// wrong total rather than as a byte count.

const pushEarlyExitN = 2

// pushEarlyExitSrc places an early exit before the element's declaration
// (`pre`), between the declaration and the push (`mid`), or after it (`post`).
func pushEarlyExitSrc(pre, mid, post string) string {
	return fmt.Sprintf(`struct Val { kind: i32, kids: i32[] }
function churn(n: i32): i32 {
    var vals: Val[] = [];
    var total: i32 = 0;
    for i in 0..n {
%s        var v = Val { kind: i, kids: [i] };
%s        vals = vals.append(v);
%s        total = total + vals.len();
    }
    return total;
}
function main(): i32 {
    var w1: i32 = churn(%d);
    var b0: i64 = __heap_bump_bytes();
    var w2: i32 = churn(%d);
    var b1: i64 = __heap_bump_bytes();
    if (w1 != %d || w2 != %d) { return 201; }
    var d: i64 = b1 - b0;
    if (d <= 0) { return 0; }
    if (d > 200) { return 200; }
    return (d as i32);
}`, pre, mid, post, pushEarlyExitN, pushEarlyExitN,
		pushEarlyExitN*(pushEarlyExitN+1)/2, pushEarlyExitN*(pushEarlyExitN+1)/2)
}

var pushEarlyExitCases = []struct {
	name           string
	pre, mid, post string
}{
	{"no early exit", "", "", ""},
	{"guard-clause return before the declaration", "        if (i == 9999) { return 12345; }\n", "", ""},
	{"guard-clause continue before the declaration", "        if (i == 9999) { continue; }\n", "", ""},
	{"return after the push", "", "", "        if (i == 9999) { return 12345; }\n"},
	{"continue after the push", "", "", "        if (i == 9999) { continue; }\n"},
	{"break after the push", "", "", "        if (i == 9999) { break; }\n"},
}

func runPushEarlyExitChecks(t *testing.T, run func(*testing.T, string) int) {
	t.Helper()
	for _, c := range pushEarlyExitCases {
		t.Run(c.name, func(t *testing.T) {
			got := run(t, pushEarlyExitSrc(c.pre, c.mid, c.post))
			if got == 201 {
				t.Fatalf("churn returned the wrong total — the probe is not measuring " +
					"the work it thinks it is")
			}
			if got != 0 {
				t.Errorf("the second churn handed out %d fresh bytes the first did not "+
					"give back: the pushed element is being copied and leaked once per "+
					"iteration. An early exit outside the declaration-to-push interval "+
					"cannot make the push conditional", got)
			}
		})
	}
}

func TestX86_64ArrayPushEarlyExitReclaim(t *testing.T) {
	ast.RcFreeEnabled = true
	runPushEarlyExitChecks(t, mustRunX86_64FreeOn)
}

func TestArm64ArrayPushEarlyExitReclaim(t *testing.T) {
	ast.RcFreeEnabled = true
	runPushEarlyExitChecks(t, mustRunArm64FreeOn)
}

func TestWASMArrayPushEarlyExitReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	runPushEarlyExitChecks(t, func(t *testing.T, src string) int {
		return runWasm(t, src)
	})
}
