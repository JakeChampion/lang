package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostDeferResultStrictChecker pins #4594: defer/errdefer in a
// Result- or Option-returning function must pass the self-host emit's
// asmcore.check_module gate (asmcore.entry_check_errors) and compile.
//
// The parse-level defer pass (parser.fern lower_defers_func) rewrites every
// `return E` into `__defret = E; …; return __defret`, pre-declaring the shared
// temp as `let __defret = 0` (i32). When the function returns Result/Option
// that made the strict checker false-positive: E003 on `__defret = Ok(x)`
// (assigning a wrapper to an i32 slot) and E002 on `return __defret`
// (returning i32 where Result was declared). The fix binds every compiler-
// synthesized `__`-prefixed temp as `unknown` in the check gate
// (asmcore.check_stmt), and `assignable` treats unknown as compatible in both
// directions — so the desugared body type-checks. The interpreter is the
// spec: each program must exit with the code it gives.
func TestSelfHostDeferResultStrictChecker(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	// emitAndRun pipes src to the driver, assembles the emitted asm, runs it,
	// and returns its exit code. An empty emit means the checker gate aborted
	// with exit(1) — the #4594 bug.
	emitAndRun := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver rejected valid program (#4594 regression): %v\nstderr:\n%s\n--- src ---\n%s", err, stderr.String(), src)
		}
		innerAsm := filepath.Join(dir, "defret_inner.s")
		innerBin := filepath.Join(dir, "defret_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
	}{
		// The canonical repro from the issue: errdefer in a Result-returning
		// function, both the Ok and Err returns rewritten through __defret.
		{"errdefer-result", `function f(x: i32): Result[i32, i32] { errdefer print("E"); if (x < 0) { return Err(1); } return Ok(x); }
function main(): i32 { match (f(5)) { Ok(v) => { return v; }, Err(e) => { return 0 - e; } } }`},
		// Plain defer (fires on every return) in a Result-returning function.
		{"defer-result", `function f(x: i32): Result[i32, i32] { defer print("D"); if (x < 0) { return Err(2); } return Ok(x + 1); }
function main(): i32 { match (f(6)) { Ok(v) => { return v; }, Err(e) => { return 0 - e; } } }`},
		// Option-returning function: Some/None both flow through __defret.
		{"errdefer-option", `function f(x: i32): Option[i32] { errdefer print("E"); if (x < 0) { return None; } return Some(x + 2); }
function main(): i32 { match (f(7)) { Some(v) => { return v; }, None => { return 0; } } }`},
		// defer + errdefer together, Err path taken (exercises both cleanup
		// lists and the __defret rewrite on an Err return).
		// A void function: its returns carry no value, so the expansion
		// declares no `__defret`. The defer's effect on both returns reaches
		// the exit code through the cell.
		{"void-defer", `function f(n: i32, c: Cell[i32]): void { defer c.set(c.get() + 10); if (n > 0) { c.set(c.get() + 1); return; } c.set(c.get() + 2); return; }
function main(): i32 { let c: Cell[i32] = cell_new(0); f(1, c); f(0, c); return c.get(); }`},
		{"defer-and-errdefer-err-path", `function f(x: i32): Result[i32, i32] { defer print("D"); errdefer print("E"); if (x < 0) { return Err(4); } return Ok(x); }
function main(): i32 { match (f(0 - 9)) { Ok(v) => { return v; }, Err(e) => { return e; } } }`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			// The interpreter is the spec.
			_, want := runInterp(t, src)
			got := emitAndRun(t, src)
			if got != want {
				t.Errorf("%s: exit %d, interpreter %d", tc.name, got, want)
			}
		})
	}
}
