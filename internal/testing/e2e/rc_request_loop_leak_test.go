package e2e

import "testing"

const rcRequestLoopLeakGuard = `
import "core/int";
import "std/string";
struct Request { method: string, path: string, body: string }
struct Response { status: i32, body: string }
function handle(req: Request): Response {
    let segs: string[] = req.path.split("/");
    let line: string = req.method + " " + req.path + " (" + req.body + ")";
    let n: i32 = segs.len();
    return Response { status: 200 + (n - n), body: line };
}
function serve(iters: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < iters) {
        let r: Response = handle(Request { method: "GET", path: "/api/v1/users/42", body: "ping" });
        if (r.status != 200) { return 0 - 1; }
        acc = acc + r.body.len();
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    // Warm the freelist to steady state, then measure two equal batches.
    let warm: i32 = serve(5000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let a: i32 = serve(50000);
    let g1: i32 = (__heap_bump_bytes() as i32) - b1;
    let b2: i32 = (__heap_bump_bytes() as i32);
    let c: i32 = serve(50000);
    let g2: i32 = (__heap_bump_bytes() as i32) - b2;
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
