package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var optIndexRecoveryCases = []struct {
	name string
	src  string
	want int
}{
	{"nested-index", `
function main(): i32 {
    let aoa: Option[i32][][] = [[Some(4), None], [Some(9)]];
    match (aoa[0][0]) { Some(v) => { return v; }, None => { return 1; } }
}
`, 4},
	{"tuple-elem-index", `
function main(): i32 {
    let t: (Option[i32][], i32) = ([Some(7), None], 3);
    match (t.0[0]) { Some(v) => { return v; }, None => { return 1; } }
}
`, 7},
	{"nested-index-try", `
function first(aoa: Option[i32][][]): Option[i32] {
    let v: i32 = aoa[0][0]?;
    return Some(v + 1);
}
function main(): i32 {
    match (first([[Some(4), None]])) { Some(v) => { return v; }, None => { return 1; } }
}
`, 5},
	{"local-index", `
function main(): i32 {
    let a: Option[i32][] = [Some(4), None];
    match (a[0]) { Some(v) => { return v; }, None => { return 1; } }
}
`, 4},
	{"struct-field-index", `
struct B { o: Option[i32][] }
function main(): i32 {
    let b: B = B { o: [Some(5), None] };
    match (b.o[0]) { Some(v) => { return v; }, None => { return 1; } }
}
`, 5},
	{"nested-index-none", `
function main(): i32 {
    let aoa: Option[i32][][] = [[None, Some(4)]];
    match (aoa[0][0]) { Some(v) => { return v; }, None => { return 6; } }
}
`, 6},
}

func TestSelfHostOptIndexRecoveryIRX86_64(t *testing.T) {
	gcc, runner, driverBin := strictIRDriver(t)
	dir := filepath.Dir(driverBin)

	for _, tc := range optIndexRecoveryCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, stderr, code := runDriver(t, runner, driverBin, []byte(tc.src), true)
			if code != 0 || len(asm) == 0 {
				t.Fatalf("%s did not lower on the IR path (exit %d):\n%s", tc.name, code, stderr)
			}
			progBin := buildBin(t, gcc, dir, "optidx_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostOptIndexRecoveryIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host opt-index-recovery wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range optIndexRecoveryCases {
		t.Run(tc.name, func(t *testing.T) {
			wat, stderr, code := runDriver(t, runner, driverBin, []byte(tc.src), true)
			if code != 0 || len(wat) == 0 {
				t.Fatalf("%s did not lower on the wasm IR path (exit %d):\n%s", tc.name, code, stderr)
			}
			watFile := filepath.Join(dir, "opt_index_recovery_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := run.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("opt-index-recovery wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
