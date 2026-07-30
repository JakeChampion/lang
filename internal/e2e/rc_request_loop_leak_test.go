package e2e

import "testing"

// #4417 request-loop leak guard. The Perceus model has no cycle collector by
// design (cycles are meant to be unconstructible under value-immutability), and
// the rc-correctness corpus catches over-RELEASES (__rc_underflow_count) but
// explicitly NOT leaks. This is the complementary gate the issue asks for: a
// simulated edge-handler request loop that fails if per-request heap growth
// stops being bounded — the signature of a regression that constructs a cycle,
// compounds a leak across requests, or disables reclamation.
//
// WHY IT'S COARSE (and deliberately so). The native backends currently carry
// pervasive DOCUMENTED safe-leaks — `int_to_string` / slicing leak their buffer
// (docs/RC-STRINGS-PLAN.md), and every incremental build (`s = s + x`,
// `xs = xs.append(v)` in a loop) and struct-with-string-fields threads leak a
// bounded amount per call. Measured baselines for the handler below: 176 B/req
// on x86-64 + wasm, 352 B/req on arm64 (arm64's two-word strings leak more) —
// all perfectly LINEAR (constant per request, `__rc_underflow_count() == 0`).
// So a *flat* leak assertion is impossible until that safe-leak cleanup lands;
// this guard therefore checks the three things that ARE backend-robust today:
//
//  1. Value correctness — both loop halves compute the same result (a drop
//     that corrupted data would diverge).
//  2. No rc under-release — __rc_underflow_count() stays 0 (the corpus's gate,
//     folded in here too since a request loop is a good exerciser).
//  3. BOUNDED, LINEAR growth — the second batch of N requests must not grow
//     meaningfully more than the first (catches a compounding leak or a
//     cross-request cycle, which accelerate), and per-request growth must sit
//     under an absolute ceiling of 700 B (catches a gross per-request leak or
//     reclamation broken for a class — e.g. an injected 300-element
//     append-churn handler measures 1632 B/req and trips it, while the ~176 /
//     352 B/req safe-leak baseline clears it with 2-4x margin). It TOLERATES a
//     documented linear safe-leak below ~700 B/req; catching those is the
//     RC-STRINGS-PLAN cleanup's job, and when that lands this ceiling should
//     drop toward zero.
//
// Runs on the three native backends that share the bump-allocator + freelist
// memory model; the interp backend uses Go GC (it reported 0 growth for the
// same program) so it is not a meaningful participant here.
const rcRequestLoopLeakGuard = `
import "core/int";
import "std/string";
struct Request { method: string, path: string, body: string }
struct Response { status: i32, body: string }
function handle(req: Request): Response {
    var segs: string[] = req.path.split("/");
    var line: string = req.method + " " + req.path + " (" + req.body + ")";
    var n: i32 = segs.len();
    return Response { status: 200 + (n - n), body: line };
}
function serve(iters: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < iters) {
        var r: Response = handle(Request { method: "GET", path: "/api/v1/users/42", body: "ping" });
        if (r.status != 200) { return 0 - 1; }
        acc = acc + r.body.len();
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    // Warm the freelist to steady state, then measure two equal batches.
    var warm: i32 = serve(5000);
    var b1: i32 = __heap_bump_bytes();
    var a: i32 = serve(50000);
    var g1: i32 = __heap_bump_bytes() - b1;
    var b2: i32 = __heap_bump_bytes();
    var c: i32 = serve(50000);
    var g2: i32 = __heap_bump_bytes() - b2;
    if (a != c) { return 5; }                               // value correctness
    if (__rc_underflow_count() != 0) { return 4; }          // no over-release
    if (g1 / 50000 > 700) { return 2; }                     // absolute ceiling (gross leak / reclaim broken)
    if (g2 > g1 + g1 / 4 + 65536) { return 3; }             // linear, not compounding (cycle / growing leak)
    return 0;
}
`

func TestX86_64RcRequestLoopLeakGuard(t *testing.T) {
	if _, code := compileAndRunX86_64(t, rcRequestLoopLeakGuard); code != 0 {
		t.Errorf("request-loop leak guard exit %d, want 0 (2=per-req ceiling, 3=compounding growth, 4=rc underflow, 5=value mismatch)", code)
	}
}

func TestArm64RcRequestLoopLeakGuard(t *testing.T) {
	if _, code := compileAndRunArm64(t, rcRequestLoopLeakGuard); code != 0 {
		t.Errorf("request-loop leak guard exit %d, want 0 (2=per-req ceiling, 3=compounding growth, 4=rc underflow, 5=value mismatch)", code)
	}
}

func TestWASMRcRequestLoopLeakGuard(t *testing.T) {
	if got := runWasm(t, rcRequestLoopLeakGuard); got != 0 {
		t.Errorf("request-loop leak guard exit %d, want 0 (2=per-req ceiling, 3=compounding growth, 4=rc underflow, 5=value mismatch)", got)
	}
}
