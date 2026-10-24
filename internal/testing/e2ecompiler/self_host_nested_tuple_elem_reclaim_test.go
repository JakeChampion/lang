package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var nestedTupleElemReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"nested-scalar-tuple-elem-unread", `function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let t: (i32, i32[], (i32, i32)) = (i, [i, i + 1], (i, i + 1));
        let r: i32 = t.0 + t.1[0];
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(1000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	{"nested-scalar-tuple-elem-read-through", `function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let t: (i32, i32[], (i32, i32)) = (i, [i, i + 1], (i, i + 1));
        let r: i32 = t.0 + t.1[0] + t.2.0;
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(1000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	{"nested-scalar-tuple-only", `function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let t: (i32, (i32, i32)) = (i, (i, i + 1));
        let r: i32 = t.0 + t.1.0;
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(1000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	{"nested-tuple-whole-extraction-safe", `function churn(n: i32): i32 {
    let keep: (i32, i32) = (0, 0);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let t: (i32, i32[], (i32, i32)) = (i, [i, i + 1], (i, i + 1));
        keep = t.2;
        acc = (acc + t.0) % 91;
        i = i + 1;
    }
    return (acc + keep.0 + keep.1) % 91;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 89},
	{"nested-tuple-with-alias-elem-safe", `function churn(n: i32): i32 {
    let live: i32[] = [7, 8, 9];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let t: (i32, i32[], (i32, i32)) = (i, live, (i, i + 1));
        acc = (acc + t.0 + t.1[0] + t.2.0) % 91;
        i = i + 1;
    }
    return (acc + live[0] * 3 + live[1] * 5 + live[2] * 7) % 91;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 28},
}

const nestedTupleElemFailFmt = "%s = %d, want %d (a small non-zero is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostNestedTupleElemReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	t.Setenv("FERN_LEAKCHECK", "1")
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range nestedTupleElemReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			stderr, code := hevRun(t, runner, bin)
			if code != tc.want {
				t.Errorf(nestedTupleElemFailFmt, tc.name, code, tc.want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", tc.name, allocs, frees, live)
			}
		})
	}
}

func TestSelfHostNestedTupleElemReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range nestedTupleElemReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(nestedTupleElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostNestedTupleElemReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping nested-tuple element reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range nestedTupleElemReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
				t.Errorf(nestedTupleElemFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
