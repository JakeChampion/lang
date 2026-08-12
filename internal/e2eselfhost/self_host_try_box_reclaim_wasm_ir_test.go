package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostTryBoxReclaimWasmIR is the wasm port of the #4355 `?`-consumed
// source-box reclaim (x86 sibling: TestSelfHostTryBoxReclaimIRX86_64). The
// self-host IR path has no pair-form ABI — op_opt_make boxes everywhere — so
// the same irlower-level frees apply unchanged; the box dec routes through
// wasm's __fern_rc_dec and the string payload through the string sweep. The
// baseline-vs-try growth-ratio assertion isolates the `?` edge from the
// pre-existing outer-box leak exactly as on x86.
func TestSelfHostTryBoxReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host try-box reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// SCALAR Result payload ratio — try churn leaks at most half baseline.
		{"try-box-scalar-ratio-wasm", `function mk(pre: string): Result[i32, i32] { return Ok(pre.len()); }
function innerT(pre: string): Result[i32, i32] { var v: i32 = mk(pre)?; return Ok(v + 1); }
function innerB(pre: string): Result[i32, i32] { var t: i32 = 0; match (mk(pre)) { Ok(v) => { t = v + 1; }, Err(e) => { t = e; }, } return Ok(t); }
function churnT(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerT(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function churnB(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerB(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var w: i32 = churnB(2000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churnT(2000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    var gb: i32 = b1 - b0;
    var gt: i32 = b2 - b1;
    if (gt + gt > gb + 256) { return 98; }
    return 0;
}`, 0},
		// STRING payload ratio — box + moved payload both recycle.
		{"try-box-string-ratio-wasm", `function mk(pre: string): Result[string, i32] { return Ok(pre + "abc"); }
function innerT(pre: string): Result[i32, i32] { var s: string = mk(pre)?; return Ok(s.len()); }
function innerB(pre: string): Result[i32, i32] { var t: i32 = 0; match (mk(pre)) { Ok(s) => { t = s.len(); }, Err(e) => { t = e; }, } return Ok(t); }
function churnT(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerT(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function churnB(n: i32): i32 { var pre: string = "ab"; var acc: i32 = 0; var i: i32 = 0; while (i < n) { var r = innerB(pre); match (r) { Ok(k) => { acc = (acc + k) % 251; }, Err(e) => { acc = e; }, } i = i + 1; } return acc; }
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var w: i32 = churnB(2000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churnT(2000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    var gb: i32 = b1 - b0;
    var gt: i32 = b2 - b1;
    if (gt + gt > gb + 256) { return 98; }
    return 0;
}`, 0},
		// ALIASED payload excluded — keep stays readable, detector 0.
		{"try-aliased-payload-excluded-wasm", `function mk(pre: string): Result[string, i32] { return Ok(pre); }
function inner(pre: string): Result[i32, i32] { var s: string = mk(pre)?; return Ok(s.len()); }
function go(pre: string): i32 { var r: i32 = 0; match (inner(pre)) { Ok(k) => { r = k; }, Err(e) => { r = e; }, } return r; }
function main(): i32 { var keep: string = "abc" + "def"; var bad: i32 = 0; var i: i32 = 0; while (i < 500) { if (go(keep) != 6) { bad = 1; } i = i + 1; } if (keep.len() != 6) { return 88; } if (__rc_underflow() != 0) { return 99; } return bad; }`, 0},
		// NON-CTOR return excluded — the forwarded live box is never freed.
		{"try-nonfresh-callee-excluded-wasm", `function pass(r: Result[string, i32]): Result[string, i32] { return r; }
function inner(b: Result[string, i32]): Result[i32, i32] { var s: string = pass(b)?; return Ok(s.len()); }
function main(): i32 {
    var b: Result[string, i32] = Ok("hi" + "!");
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        match (inner(b)) { Ok(k) => { if (k != 3) { bad = 1; } }, Err(e) => { bad = e; }, }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return bad;
}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("try-box wasm IR %q = %d, want %d (98 = box not reclaimed; 99 = double-free; 97 = value corrupted; 88 = aliased payload freed)", tc.name, got, tc.expected)
			}
		})
	}
}
