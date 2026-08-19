package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// closureAliasIRCases pin #4557: `var d = c` where c is a closure local left
// d a plain scalar slot, so `d()` called the raw env-box pointer as a
// call-table index — SIGSEGV on the self-host IR path (all backends).
// The fix gives StmtVar's clo_init detection a bare-ident arm gated on
// is_closure_local: the alias is marked a closure local (env-first
// dispatch) and its env box is __fern_rc_inc'd, so the exit sweep's two
// shallow decs balance and the rc==1 gate hands the capture release to the
// last owner (the alias name carries no capture kinds, so captures keep
// the documented aliased-env leak).
//
// RC note: the hoisted call path's per-call capture over-dec these cases
// originally had to tolerate is FIXED (the ENVCAP borrow exclusion —
// see self_host_closure_capture_borrow_ir_test.go, which pins detector
// zero on these shapes); these cases keep pinning values + crash-freedom.
var closureAliasIRCases = []struct {
	name string
	src  string
	want int
}{
	// The filed repro: scalar-capture closure, aliased, called via the alias.
	{"bare-alias-call",
		`function main(): i32 {
    var k: i32 = 3;
    var c = () => k + 1;
    var d = c;
    return d();
}`, 4},
	// Array capture, called through BOTH names — same env box, same values.
	{"alias-both-names-array-capture",
		`function go(k: i32): i32 {
    var xs: i32[] = [k, k + 1];
    var c = () => xs[0] + xs[1];
    var d = c;
    var a: i32 = c();
    var b: i32 = d();
    if (a != b) { return 98; }
    return a;
}
function main(): i32 { return go(3); }`, 7},
	// fn-typed PARAM alias: params are closure locals too — `var g = f`
	// inside the callee segfaulted the same way pre-fix.
	{"param-alias",
		`function apply1(f: (i32) => i32): i32 { var g = f; return g(4); }
function main(): i32 { var k: i32 = 5; var c = (x: i32) => x * k; return apply1(c); }`, 20},
	// Chained aliases (e = d = c) and a REASSIGN alias in a branch: every
	// name dispatches env-first and yields the same closure.
	{"chained-and-branch-alias",
		`function main(): i32 {
    var k: i32 = 2;
    var c = () => k + 5;
    var d = c;
    var e = d;
    var r1: i32 = e();
    var pick: boolean = true;
    var f = c;
    if (pick) { f = d; }
    return r1 * 10 + f();
}`, 77},
}

// TestSelfHostClosureAliasIRX86_64 cross-checks native, pins the "ir"
// routing, then runs the self-host-compiled binary.
func TestSelfHostClosureAliasIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range closureAliasIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			if _, code := compileAndRunX86_64(t, tc.src+"\n"); code != tc.want {
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
			bin := buildBin(t, gcc, dir, "cla-"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally (the pre-#4557 alias call segfaults)", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s self-host IR exited %d, want %d (139 = raw env-box call, #4557)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostClosureAliasWasmIR runs the same cases through the wasm IR
// backend.
func TestSelfHostClosureAliasWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping closure-alias wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range closureAliasIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s wasm IR exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostClosureAliasIRArm64 runs the repro and the param-alias case
// under qemu via `asm_ir_run -ir -target arm64-linux`.
func TestSelfHostClosureAliasIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range closureAliasIRCases {
		if tc.name != "bare-alias-call" && tc.name != "param-alias" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-ir", "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "cla-"+tc.name+"-arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s arm64 IR exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
