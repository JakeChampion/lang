package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostSleepWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm sleep e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	emitAndRun := func(t *testing.T, name, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed for %s: %v\n%s", name, err, diagnostics.String())
		}
		watFile := filepath.Join(dir, name+".wat")
		if err := os.WriteFile(watFile, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		for _, body := range wasmFuncBodies(string(wat), "$__fern_sleep_") {
			if strings.Contains(body, "$__fern_alloc") {
				t.Errorf("a sleep helper allocates (%s) — the bump heap has no free, "+
					"so that is a leak per call (#9480):\n%s", name, body)
			}
		}
		run := exec.Command("wasmtime", "run", watFile)
		var runErr bytes.Buffer
		run.Stderr = &runErr
		_ = run.Run()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatalf("wasmtime did not exit normally for %s:\n%s", name, runErr.String())
		}
		if strings.Contains(runErr.String(), "failed to compile") || strings.Contains(runErr.String(), "failed to open") {
			t.Fatalf("wasmtime could not load the module for %s:\n%s", name, runErr.String())
		}
		return run.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "sleep-ms-i64-count",
			src: `function main(): i32 {
    let a: i64 = monotonic_ns();
    sleep_ms(1 as i64);
    let b: i64 = monotonic_ns();
    if (b < a) { return 1; }
    return 7;
}`,
			want: 7,
		},
		{
			name: "sleep-ns",
			src: `function main(): i32 {
    sleep_ns(1000 as i64);
    return 5;
}`,
			want: 5,
		},
		{
			name: "sleep-ms-zero",
			src: `function main(): i32 {
    sleep_ms(0 as i64);
    return 3;
}`,
			want: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRun(t, tc.name, tc.src); got != tc.want {
				t.Errorf("exited %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSelfHostSleepWasmBinary(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm sleep binary e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	const src = `function main(): i32 {
    sleep_ms(1 as i64);
    sleep_ns(1000 as i64);
    return 9;
}
`
	progDir := t.TempDir()
	prog := filepath.Join(progDir, "prog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	out := filepath.Join(progDir, "prog.wasm")
	cmd := exec.Command(fernBin, "-target", "wasm32-wasi", "-emit", "core-module", "-o", out, prog, stdlibRoot)
	built, _ := cmd.CombinedOutput()
	if cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("self-host could not build a wasm binary containing a sleep:\n%s", built)
	}

	run := exec.Command("wasmtime", "run", out)
	var runErr bytes.Buffer
	run.Stderr = &runErr
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", runErr.String())
	}
	if code := run.ProcessState.ExitCode(); code != 9 {
		t.Fatalf("assembled module exited %d, want 9:\n%s", code, runErr.String())
	}
}
