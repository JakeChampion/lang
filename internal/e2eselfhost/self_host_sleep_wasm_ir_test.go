package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSleepWasm is the wasm sibling TestSelfHostSleepMsI64IR never had.
// That test reads the emitted ASM, so nothing looked at what wasm did with the
// same program, and two bugs sat behind that gap (#9476, #9477): the wat
// assembler had no opcode for the 16-bit memory ops the preview1 subscription's
// subclockflags field is written with, and $__fern_sleep_ms declared an i32
// parameter where sleep_ms's is i64, so the module failed validation once the
// assembler could encode it at all. Either one alone made every wasm program
// containing a sleep unbuildable, on both lowerings.
//
// Each program is emitted both ways and run under wasmtime: the exit codes must
// match each other and the expected value, so a sleep that stops building, or
// builds to a module that traps, fails here rather than in whatever imports it.
func TestSelfHostSleepWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm sleep e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	emitAndRun := func(t *testing.T, name, src string, ir bool) int {
		t.Helper()
		args := []string{}
		if ir {
			args = append(args, "-ir")
		}
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed (ir=%v) for %s: %v\n%s", ir, name, err, diagnostics.String())
		}
		tag := "ast"
		if ir {
			tag = "ir"
		}
		watFile := filepath.Join(dir, name+"_"+tag+".wat")
		if err := os.WriteFile(watFile, wat, 0o644); err != nil {
			t.Fatalf("write %s wat: %v", tag, err)
		}
		run := exec.Command("wasmtime", "run", watFile)
		var runErr bytes.Buffer
		run.Stderr = &runErr
		_ = run.Run()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatalf("wasmtime did not exit normally (ir=%v) for %s:\n%s", ir, name, runErr.String())
		}
		// A module the assembler or the validator rejects surfaces here as a
		// load failure rather than a program exit, which reads identically to a
		// wrong answer unless it is named.
		if strings.Contains(runErr.String(), "failed to compile") || strings.Contains(runErr.String(), "failed to open") {
			t.Fatalf("wasmtime could not load the module (ir=%v) for %s:\n%s", ir, name, runErr.String())
		}
		return run.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			// The i64 count is what #9477 got wrong: the helper took an i32.
			name: "sleep-ms-i64-count",
			src: `function main(): i32 {
    var a: i64 = monotonic_ns();
    sleep_ms(1 as i64);
    var b: i64 = monotonic_ns();
    if (b < a) { return 1; }
    return 7;
}`,
			want: 7,
		},
		{
			// The nanosecond twin, which already had its parameter right and
			// still could not build: it writes the same 16-bit subclockflags.
			name: "sleep-ns",
			src: `function main(): i32 {
    sleep_ns(1000 as i64);
    return 5;
}`,
			want: 5,
		},
		{
			// A zero count takes the early-out arm, so the subscription is
			// never built; it must still be a module wasmtime can load.
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
			routed := emitAndRun(t, tc.name, tc.src, false)
			forced := emitAndRun(t, tc.name, tc.src, true)
			if routed != tc.want {
				t.Errorf("routed emit exited %d, want %d", routed, tc.want)
			}
			if forced != routed {
				t.Errorf("forced IR emit exited %d, routed %d: the two lowerings disagree", forced, routed)
			}
		})
	}
}

// TestSelfHostSleepWasmBinary is the half the WAT test above cannot reach. That
// one hands wasmtime the emitted text, so wasmtime's own parser assembles it and
// the self-host's assembler (watbin.fern) never runs. #9476 lives there: it had
// no opcode for the 16-bit memory ops, so producing a wasm BINARY from any
// program containing a sleep failed with "unknown instruction: i32.store16"
// while the same program's WAT ran fine.
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

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	// The subscription's subclockflags field is the 16-bit store, and it is
	// written on the arm a positive count takes.
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
