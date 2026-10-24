package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var tupleScalarElemReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"tuple-scalar-binary-elem-flat", `function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let t: (i32, i32[]) = (i + 1, [i, i + 1]);
        acc = (acc + t.0 + t.1[1]) % 251;
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
	{"tuple-nested-scalar-binary-flat", `function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let t: ((i32, i32[]), i32) = ((i, [i, i + 1]), i + 1);
        acc = (acc + t.1) % 251;
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
	{"tuple-string-elem-with-scalar-sibling-flat", `function churn(n: i32, s: string): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let t: (i32, string) = (i + 1, "a-wide-string-past-the-inline-threshold-" + s);
        acc = (acc + t.0 + t.1.len()) % 251;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let s: string = "tail";
    let w: i32 = churn(1000, s);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(1000, s);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    if (s.len() != 4) { return 96; }
    return (b2 - b1) / 1000;
}`, 0},
	{"tuple-call-elem-alias-safe", `@noinline function id(xs: i32[]): i32[] { return xs; }
function churn(n: i32): i32 {
    let live: i32[] = [7, 8, 9];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let t: (i32, i32[], i32[]) = (i + 1, [i, i + 1], id(live));
        acc = (acc + t.0 + t.1[1] + t.2[0] + t.2[2]) % 251;
        i = i + 1;
    }
    return (acc + live[0] * 3 + live[1] * 5 + live[2] * 7) % 251;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 72},
	{"tuple-scalar-binary-escape-safe", `function churn(n: i32): (i32, i32[]) {
    let last: (i32, i32[]) = (0, [0, 0]);
    let i: i32 = 0;
    while (i < n) {
        let t: (i32, i32[]) = (i + 1, [i, i + 1]);
        last = t;
        i = i + 1;
    }
    return last;
}
function main(): i32 {
    let r: (i32, i32[]) = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    return (r.0 + r.1[0] + r.1[1]) % 97;
}`, 89},
	{"tuple-unproven-concat-safe", `function churn(n: i32, a: string, b: string): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let t: (i32, string, i32[]) = (i + 1, a + b, [i, i + 1]);
        acc = (acc + t.0 + t.1.len() + t.2[0]) % 251;
        i = i + 1;
    }
    return (acc + a.len() + b.len()) % 251;
}
function main(): i32 {
    let a: string = "a-wide-left-side-past-the-inline-threshold";
    let b: string = "-and-a-wide-right-side-too";
    let w: i32 = churn(1000, a, b);
    let x: i32 = churn(1000, a, b);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    if (a.len() != 41 || b.len() != 26) { return 96; }
    return w;
}`, 96},
}

const tupleScalarElemReclaimFailFmt = "%s = %d, want %d (a small non-zero is the leaked bytes per round; 99 = over-release; 97 = value corrupted; 96 = an operand was freed under a live local)"

func TestSelfHostTupleScalarElemReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleScalarElemReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(tupleScalarElemReclaimFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTupleScalarElemReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleScalarElemReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(tupleScalarElemReclaimFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTupleScalarElemReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple scalar-element reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupleScalarElemReclaimCases {
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
				t.Errorf(tupleScalarElemReclaimFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
