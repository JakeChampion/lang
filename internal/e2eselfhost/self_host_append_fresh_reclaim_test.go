package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// The self-host IR sibling of internal/e2e/append_fresh_reclaim_test.go
// (#5608): a local array rebuilt by `xs = xs.append(v)` and returned must not
// orphan a buffer per copy-grow in the caller. The native fix was in the
// freshness oracle (computeFreshLocals' COW self-reassign carve-out did not
// list `__method_Array_push`); this pins that the self-hosted compiler's IR
// path is bounded on the same shape, so the two reclaim stories stay in step.
//
// Probe is `__heap_bump_bytes()` — the bump high-water, which lowers on the
// self-host IR path (#3534). Each phase warms up, samples, churns 10x more and
// re-samples; equal samples mean every buffer the extra calls allocated was
// reclaimed. `noAppend` is the control that must be bounded either way.
//
// The callees take a parameter deliberately: a constant argument gets them
// inlined, which hides the leak.
const selfHostAppendFreshReclaimMain = `function noAppend(k: i32): i32[] { var xs: i32[] = [1]; return xs; }
function oneGrow(k: i32): i32[] { var xs: i32[] = [1]; xs = xs.append(k); return xs; }
function loopGrows(k: i32): i32[] {
    var xs: i32[] = [1];
    var j: i32 = 0;
    while (j < 12) { xs = xs.append(k); j = j + 1; }
    return xs;
}
function churnNone(n: i32): i32 { var k: i32 = n & 3; var i: i32 = 0; var a: i32 = 0; while (i < n) { var z: i32[] = noAppend(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function churnOne(n: i32): i32 { var k: i32 = n & 3; var i: i32 = 0; var a: i32 = 0; while (i < n) { var z: i32[] = oneGrow(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function churnLoop(n: i32): i32 { var k: i32 = n & 3; var i: i32 = 0; var a: i32 = 0; while (i < n) { var z: i32[] = loopGrows(k); a = (a + z.len()) % 251; i = i + 1; } return a; }
function main(): i32 {
    var t: i32 = 0;
    t = t + churnNone(3); var a1: i32 = (__heap_bump_bytes() as i32); t = t + churnNone(10); var a2: i32 = (__heap_bump_bytes() as i32);
    if (a2 != a1) { return 11; }
    t = t + churnOne(3); var b1: i32 = (__heap_bump_bytes() as i32); t = t + churnOne(10); var b2: i32 = (__heap_bump_bytes() as i32);
    if (b2 != b1) { return 12; }
    t = t + churnLoop(3); var c1: i32 = (__heap_bump_bytes() as i32); t = t + churnLoop(10); var c2: i32 = (__heap_bump_bytes() as i32);
    if (c2 != c1) { return 13; }
    var v: i32[] = loopGrows(7);
    if (v.len() != 13) { return 20; }
    if (v[12] != 7) { return 21; }
    if (t < 0) { return 22; }
    return 42;
}
`

// TestSelfHostAppendFreshReclaimIRX86_64 runs the shape through the self-hosted
// x86-64 IR driver, pinning both the routing (must be "ir", not the AST
// fallback) and the bounded-high-water result.
func TestSelfHostAppendFreshReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	src := []byte(selfHostAppendFreshReclaimMain)

	// Native cross-check: the Go x86-64 backend is the oracle for the exit code.
	if _, code := compileAndRunX86_64(t, selfHostAppendFreshReclaimMain); code != 42 {
		t.Fatalf("native exited %d, want 42", code)
	}
	if path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src))); path != "ir" {
		t.Fatalf("routed through %q path, want \"ir\"", path)
	}
	asm := runCapture(t, gcc, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "append_fresh_reclaim", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}
	cmd.Stdin = bytes.NewReader(nil)
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("self-host-compiled program did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("self-host IR append-fresh reclaim exited %d, want 42", code)
	}
}
