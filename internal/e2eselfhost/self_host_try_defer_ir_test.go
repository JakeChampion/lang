package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tryDeferIRCases pin defer/errdefer firing on the `?` (try) FAILURE path on
// the self-host IR path (#4334 part 1 — part 2, the RC dec-sweep, landed in
// #4422). The self-host lowers defers at PARSE level by rewriting StmtReturn
// (parser.fern lower_defers_func), so the try operator's implicit early
// return skipped every registered defer — and errdefer never fired on the
// exact path it exists for. The fix: the parse pass embeds its two cleanup
// lists behind never-true `__dfa_tryall` / `__dfa_tryerr` guards at the body
// tail, and irlower's lower_try replays the guarded statements at the `?`
// failure edge (plain defers first, then errdefers, then the dec-sweep —
// native's TryOp order: emitDeferCleanup, emitErrDeferCleanup,
// emitRcDecLocalsAtExit).
//
// Expectations are native semantics: every case is cross-checked against the
// native x86-64 backend before asserting the self-host IR result. The legacy
// self-host AST emitters still skip defers at `?` (the markers are dead code
// there) — a documented gap, per the policy that legacy-AST-only gaps don't
// block IR-path features.
var tryDeferIRCases = []struct {
	name    string
	main    string
	wantOut string
	want    int
}{
	// Both defer and errdefer fire on a failing `?`, defer first — then the
	// caller sees the propagated Err.
	{"result-fail-defer-and-errdefer",
		`function step(x: i32): Result[i32, i32] { if (x < 0) { return Err(1); } return Ok(x); }
function f(x: i32): Result[i32, i32] {
    defer print("D");
    errdefer print("E");
    var v: i32 = step(x)?;
    return Ok(v);
}
function main(): i32 {
    match (f(0 - 1)) { Ok(v) => {}, Err(e) => { print("err"); } }
    return 0;
}`, "D\nE\nerr\n", 0},
	// Success path: the `?` yields the payload, the function leaves via its
	// normal return — defer fires there (the parse-level rewrite, untouched by
	// this fix), errdefer stays silent.
	{"success-defer-at-return-only",
		`function step(x: i32): Result[i32, i32] { if (x < 0) { return Err(1); } return Ok(x); }
function f(x: i32): Result[i32, i32] {
    defer print("D");
    errdefer print("E");
    var v: i32 = step(x)?;
    return Ok(v);
}
function main(): i32 {
    match (f(5)) { Ok(v) => { print("ok"); }, Err(e) => { print("err"); } }
    return 0;
}`, "D\nok\n", 0},
	// Two defers replay LIFO at the `?` edge, same as at an explicit return.
	{"lifo-order-at-try-edge",
		`function step(x: i32): Result[i32, i32] { if (x < 0) { return Err(1); } return Ok(x); }
function f(x: i32): Result[i32, i32] {
    defer print("A");
    defer print("B");
    var v: i32 = step(x)?;
    return Ok(v);
}
function main(): i32 { match (f(0 - 3)) { Ok(v) => {}, Err(e) => { print("err"); } } return 0; }`,
		"B\nA\nerr\n", 0},
	// Registration is dynamic: a conditionally-registered defer fires only when
	// its branch ran, and a defer AFTER the `?` never fires on the failure path
	// (its `__dfa` flag is still 0 when the replay runs).
	{"conditional-and-late-registration",
		`function step(x: i32): Result[i32, i32] { if (x < 0) { return Err(1); } return Ok(x); }
function f(reg: boolean, x: i32): Result[i32, i32] {
    if (reg) { defer print("C"); }
    var v: i32 = step(x)?;
    defer print("L");
    return Ok(v);
}
function main(): i32 {
    match (f(true, 0 - 1)) { Ok(v) => {}, Err(e) => { print("e1"); } }
    match (f(false, 0 - 1)) { Ok(v) => {}, Err(e) => { print("e2"); } }
    return 0;
}`, "C\ne1\ne2\n", 0},
	// Option `?`: None propagation is the error path too — both kinds fire.
	{"option-none-propagation",
		`function step(x: i32): Option[i32] { if (x < 0) { return None; } return Some(x); }
function f(x: i32): Option[i32] {
    defer print("D");
    errdefer print("E");
    var v: i32 = step(x)?;
    return Some(v + 1);
}
function main(): i32 { match (f(0 - 2)) { Some(v) => {}, None => { print("none"); } } return 0; }`,
		"D\nE\nnone\n", 0},
	// RC interplay: the cleanup replay runs BEFORE the failure-path dec-sweep,
	// and the sweep still reclaims an owned local live at the `?`. Same
	// bare-vs-owned isolation as the #4422 cases (the Option box itself is the
	// known safe-leak baseline both loops share); with a defer registered in
	// both, the owned loop's extra growth stays ~0 only if the sweep survives
	// the replay.
	{"rc-owned-array-reclaimed-with-defer",
		`function fails(): Option[i32] { return None; }
function step_bare(): Option[i32] { var acc: i32 = 0; defer acc = acc + 1; var x: i32 = fails()?; return Some(x); }
function step_owned(): Option[i32] { var acc: i32 = 0; defer acc = acc + 1; var owned: i32[] = [1, 2, 3, 4, 5]; var x: i32 = fails()?; return Some(x + owned[0]); }
function main(): i32 {
    var b0: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 20000) { match (step_bare()) { Some(_) => {}, None => {} } i = i + 1; }
    var base: i32 = __heap_bump_bytes() - b0;
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 20000) { match (step_owned()) { Some(_) => {}, None => {} } j = j + 1; }
    if (__heap_bump_bytes() - b1 - base < 100000) { return 7; }
    return 1;
}`, "", 7},
}

// TestSelfHostTryDeferIRX86_64 cross-checks each case against the native
// x86-64 backend, pins the "ir" routing, then runs the self-host-compiled
// binary and compares stdout + exit code.
func TestSelfHostTryDeferIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range tryDeferIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			// Native cross-check: stdout and exit code are the spec.
			nativeOut, nativeCode := compileAndRunX86_64(t, tc.main+"\n")
			if nativeCode != tc.want || nativeOut != tc.wantOut {
				t.Fatalf("%s native: out %q exit %d, want %q / %d", tc.name, nativeOut, nativeCode, tc.wantOut, tc.want)
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
			out, _ := cmd.Output()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want || string(out) != tc.wantOut {
				t.Errorf("%s self-host IR: out %q exit %d, want %q / %d (defers skipped at `?` reproduce the pre-#4334 gap)",
					tc.name, string(out), code, tc.wantOut, tc.want)
			}
		})
	}
}

// TestSelfHostTryDeferWasmIR runs the semantic core (fail-path firing,
// conditional registration, RC interplay) through the wasm IR backend.
func TestSelfHostTryDeferWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host try-defer wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range tryDeferIRCases {
		if tc.name == "success-defer-at-return-only" || tc.name == "lifo-order-at-try-edge" || tc.name == "option-none-propagation" {
			continue // covered on x86-64/arm64; keep the wasm leg lean
		}
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.main + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			out, _ := rcmd.Output()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if code := rcmd.ProcessState.ExitCode(); code != tc.want || string(out) != tc.wantOut {
				t.Errorf("%s wasm IR: out %q exit %d, want %q / %d", tc.name, string(out), code, tc.wantOut, tc.want)
			}
		})
	}
}

// TestSelfHostTryDeferIRArm64 runs the core failure-path case plus the RC
// interplay case under qemu via `asm_ir_run -target arm64`.
func TestSelfHostTryDeferIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tryDeferIRCases {
		if tc.name != "result-fail-defer-and-errdefer" && tc.name != "rc-owned-array-reclaimed-with-defer" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			// -ir is explicit here: asm_ir_run's non-IR route runs the strict
			// pre-codegen checker, which rejects the defer pass's `__defret =
			// Ok(…)` rebind in any Result-returning defer function (E004, a
			// pre-existing legacy-path gap noted on #4334) — defer+Result only
			// compiles through the IR path on this driver.
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.main+"\n"), "-ir", "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name+"-arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			out, _ := cmd.Output()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want || string(out) != tc.wantOut {
				t.Errorf("%s arm64 IR: out %q exit %d, want %q / %d", tc.name, string(out), code, tc.wantOut, tc.want)
			}
		})
	}
}
