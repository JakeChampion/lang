package e2eselfhost

import (
	"testing"
)

// TestSelfHostTryBoxReclaimIRArm64 is the arm64 port of the #4355
// `?`-consumed source-box reclaim (x86 sibling:
// TestSelfHostTryBoxReclaimIRX86_64). Same irlower-level frees; the box dec
// and string sweep route through the arm64 runtime helpers. Lighter churn
// under qemu; the growth-ratio assertion isolates the `?` edge from the
// pre-existing outer-box leak exactly as on x86.
func TestSelfHostTryBoxReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = box not reclaimed; 99 = over-release; 97 = value corrupted; 88 = aliased payload freed)", name, code, want)
		}
	}

	// SCALAR Result payload ratio — try churn leaks at most half baseline.
	run(t, `function mk(pre: string): Result[i32, i32] { return Ok(pre.len()); }
function innerT(pre: string): Result[i32, i32] { var v: i32 = mk(pre)?; return Ok(v + 1); }
function innerB(pre: string): Result[i32, i32] { var t: i32 = 0; match (mk(pre)) { Ok(v) => { t = v + 1; }, Err(e) => { t = e; }, } return Ok(t); }
function churnT(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerT(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function churnB(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerB(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function main(): i32 {
    var b0: i32 = __heap_bump_bytes();
    var w: i32 = churnB(1500);
    var b1: i32 = __heap_bump_bytes();
    var x: i32 = churnT(1500);
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    var gb: i32 = b1 - b0;
    var gt: i32 = b2 - b1;
    if (gt + gt > gb + 256) { return 98; }
    return 0;
}`, "try-box-scalar-ratio-arm64", 0)

	// STRING payload ratio — box + moved payload both recycle.
	run(t, `function mk(pre: string): Result[string, i32] { return Ok(pre + "abc"); }
function innerT(pre: string): Result[i32, i32] { var s: string = mk(pre)?; return Ok(s.len()); }
function innerB(pre: string): Result[i32, i32] { var t: i32 = 0; match (mk(pre)) { Ok(s) => { t = s.len(); }, Err(e) => { t = e; }, } return Ok(t); }
function churnT(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerT(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function churnB(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerB(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function main(): i32 {
    var b0: i32 = __heap_bump_bytes();
    var w: i32 = churnB(1500);
    var b1: i32 = __heap_bump_bytes();
    var x: i32 = churnT(1500);
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    var gb: i32 = b1 - b0;
    var gt: i32 = b2 - b1;
    if (gt + gt > gb + 256) { return 98; }
    return 0;
}`, "try-box-string-ratio-arm64", 0)

	// ALIASED payload excluded — keep stays readable, detector 0.
	run(t, `function mk(pre: string): Result[string, i32] { return Ok(pre); }
function inner(pre: string): Result[i32, i32] { var s: string = mk(pre)?; return Ok(s.len()); }
function go(pre: string): i32 { var r: i32 = 0; match (inner(pre)) { Ok(k) => { r = k; }, Err(e) => { r = e; }, } return r; }
function main(): i32 { var keep: string = "abc" + "def"; var bad: i32 = 0; var i: i32 = 0; while (i < 500) { if (go(keep) != 6) { bad = 1; } i = i + 1; } if (keep.len() != 6) { return 88; } if (__rc_underflow() != 0) { return 99; } return bad; }`,
		"try-aliased-payload-excluded-arm64", 0)
}
