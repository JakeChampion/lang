package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tryFailCleanupIRCases pin the RC dec-sweep on the self-host `?` (try) FAILURE
// path (#4334). A failure-path early return with no cleanup leaks every owned
// array / string / struct / map / tuple local live at a `?` when the `?`
// short-circuits — the only uncleaned exit on the IR
// path (StmtReturn already swept). The fix routes the failure return through the
// same emit_dec_sweep_except a normal return runs, mirroring native's
// emitRcDecLocalsAtExit at the TryOp failure edge.
//
// The heap probe ISOLATES the owned-local reclaim from the unrelated Option-box
// safe-leak (an enum box + payload are never swept; ~16 B/iter here on every
// backend, #2704). It runs two 20000-iteration loops over functions that differ
// ONLY by an owned local (array / string) declared live across a FAILING `?`,
// and compares the bump high-water growth: if the owned local is reclaimed the
// delta is ~0 (both loops leak only their Option boxes), and if it leaks the
// owned loop grows by 20000 * ~20 B ≈ 400000. Expectations are the native
// result (native reclaims — validated exit 7). Without the fix the self-host
// owned loop leaks and returns 1.
var tryFailCleanupIRCases = []struct {
	name string
	main string
	want int
}{
	// Owned i32[] live across a failing `?` — reclaimed => extra growth ~0.
	{"try-fail-array-reclaimed",
		`function fails(): Option[i32] { return None; }
function step_bare(): Option[i32] { var x: i32 = fails()?; return Some(x); }
function step_owned(): Option[i32] { var owned: i32[] = [1, 2, 3, 4, 5]; var x: i32 = fails()?; return Some(x + owned[0]); }
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 20000) { match (step_bare()) { Some(_) => {}, None => {} } i = i + 1; }
    var base: i32 = (__heap_bump_bytes() as i32) - b0;
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 20000) { match (step_owned()) { Some(_) => {}, None => {} } j = j + 1; }
    if ((__heap_bump_bytes() as i32) - b1 - base < 100000) { return 7; }
    return 1;
}`, 7},
	// Owned heap string live across a failing `?` — exercises __fern_str_free in
	// the sweep.
	{"try-fail-string-reclaimed",
		`function fails(): Option[i32] { return None; }
function step_bare(): Option[i32] { var x: i32 = fails()?; return Some(x); }
function step_owned(): Option[i32] { var owned: string = "abcdefghijklmnop" + "!"; var x: i32 = fails()?; return Some(x + owned.len()); }
function main(): i32 {
    var b0: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 20000) { match (step_bare()) { Some(_) => {}, None => {} } i = i + 1; }
    var base: i32 = (__heap_bump_bytes() as i32) - b0;
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 20000) { match (step_owned()) { Some(_) => {}, None => {} } j = j + 1; }
    if ((__heap_bump_bytes() as i32) - b1 - base < 100000) { return 7; }
    return 1;
}`, 7},
	// The SUCCESS path is unchanged by the failure-path edit: the `?` still
	// yields the payload and the value is correct (the sweep is emitted only in
	// the failure block, under the return value already on the operand stack).
	{"try-success-value",
		`function ok(): Option[i32] { return Some(41); }
function step(): Option[i32] { var owned: i32[] = [1]; var x: i32 = ok()?; return Some(x + owned[0]); }
function main(): i32 { match (step()) { Some(v) => { return v - 35; }, None => { return 1; } } }`, 7},
}

// TestSelfHostTryFailCleanupIRX86_64 routes each case through the self-hosted
// x86-64 IR driver (pinned to "ir"), cross-checks the native exit code, and runs
// the emitted binary.
func TestSelfHostTryFailCleanupIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range tryFailCleanupIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			// Native cross-check: the Go x86-64 backend (which cleans up at the
			// TryOp failure edge) must agree.
			if _, code := compileAndRunX86_64(t, tc.main+"\n"); code != tc.want {
				t.Fatalf("%s native exited %d, want %d", tc.name, code, tc.want)
			}
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d (1 = owned local leaked on the ? failure path)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostTryFailCleanupIRWasm runs the same cases through the wasm IR
// backend (the sweep uses __fern_rc_dec / __fern_str_free the same way).
func TestSelfHostTryFailCleanupIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host try-fail-cleanup wasm IR e2e")
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

	for _, tc := range tryFailCleanupIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "try_fail_cleanup_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("try-fail-cleanup wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
