package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostDeferResultStrictChecker(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
		{"errdefer-result", `function f(x: i32): Result[i32, i32] { errdefer print("E"); if (x < 0) { return Err(1); } return Ok(x); }
function main(): i32 { match (f(5)) { Ok(v) => { return v; }, Err(e) => { return 0 - e; } } }`},
		{"defer-result", `function f(x: i32): Result[i32, i32] { defer print("D"); if (x < 0) { return Err(2); } return Ok(x + 1); }
function main(): i32 { match (f(6)) { Ok(v) => { return v; }, Err(e) => { return 0 - e; } } }`},
		{"errdefer-option", `function f(x: i32): Option[i32] { errdefer print("E"); if (x < 0) { return None; } return Some(x + 2); }
function main(): i32 { match (f(7)) { Some(v) => { return v; }, None => { return 0; } } }`},
		{"void-defer", `function f(n: i32, c: Cell[i32]): void { defer c.set(c.get() + 10); if (n > 0) { c.set(c.get() + 1); return; } c.set(c.get() + 2); return; }
function main(): i32 { let c: Cell[i32] = cell_new(0); f(1, c); f(0, c); return c.get(); }`},
		{"defer-and-errdefer-err-path", `function f(x: i32): Result[i32, i32] { defer print("D"); errdefer print("E"); if (x < 0) { return Err(4); } return Ok(x); }
function main(): i32 { match (f(0 - 9)) { Ok(v) => { return v; }, Err(e) => { return e; } } }`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			_, want := runInterp(t, src)
			got := emitAndRun(t, src)
			if got != want {
				t.Errorf("%s: exit %d, interpreter %d", tc.name, got, want)
			}
		})
	}
}
