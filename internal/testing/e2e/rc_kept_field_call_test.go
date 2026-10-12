package e2e

import (
	"testing"
)

// #12135: a construction that rebuilds its own donor keeps a field in place,
// which stands in for the construction's retain of that field. The loop below
// reads `ctx.st` once, hands it to `wrap`, which keeps it and so takes a unit
// of it, and then rebuilds `ctx` around it. The keep must replace only the
// construction's retain: when `ctx` was unique, the call's retain was skipped
// too, so `wrap` took the slot's unit and the rebuilt `ctx` held a freed `St`.
const keptFieldCallSrc = `import "std/string";
import "std/i32";
import "std/array";

struct St { names: string[] }
struct Ctx { st: St, diags: string[] }
struct Wrapped { s: St, n: i32 }

@noinline
function wrap(s: St, n: i32): Wrapped {
    return Wrapped { s: s, n: n };
}

@noinline
function run(n: i32): Ctx {
    let ctx: Ctx = Ctx { st: St { names: [f"x{n}", "y"] }, diags: [] };
    let i: i32 = 0;
    while (i < n) {
        let st: St = ctx.st;
        let d: string[] = ctx.diags;
        let w: Wrapped = wrap(st, i);
        ctx = Ctx { st: st, diags: d.append(w.n.to_string()) };
        i = i + 1;
    }
    return ctx;
}

function main(): i32 {
    let ctx: Ctx = run(100);
    if (ctx.st.names[0] != "x100" || ctx.st.names.len() != 2) { return 1; }
    if (ctx.diags.len() != 100 || ctx.diags[99] != "99") { return 2; }
    return 0;
}`

func TestInterpKeptFieldCallValues(t *testing.T) {
	if got := runInterpExit(t, keptFieldCallSrc); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestX86_64KeptFieldCallSanitized(t *testing.T) {
	checkSanitizedBalanced(t, keptFieldCallSrc, 0, runSanitizeX86_64)
}

func TestArm64KeptFieldCallSanitized(t *testing.T) {
	checkSanitizedBalanced(t, keptFieldCallSrc, 0, runSanitizeArm64)
}

func TestWASMKeptFieldCallSanitized(t *testing.T) {
	checkSanitizedBalanced(t, keptFieldCallSrc, 0, func(t *testing.T, src string) (string, string, int) {
		return runLeakCheckWasm(t, src, true)
	})
}
