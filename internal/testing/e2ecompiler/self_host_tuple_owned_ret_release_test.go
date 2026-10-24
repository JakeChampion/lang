package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var tupleOwnedRetReleaseCases = []struct {
	name string
	src  string
	want int
}{
	{"tupown-cond-lit-then-local", `function mk(i: i32): (i32, i32[]) {
    if (i % 7 == 0) { return (0, [0]); }
    let t: (i32, i32[]) = (i, [i, i + 1]);
    return t;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"tupown-uncond-local", `function mk(i: i32): (i32, i32[]) {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    return t;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"tupown-early-local-late-lit", `function mk(i: i32): (i32, i32[]) {
    if (i % 7 != 0) { let t: (i32, i32[]) = (i, [i, i + 1]); return t; }
    return (0, [0]);
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"tupown-both-literal", `function mk(i: i32): (i32, i32[]) {
    if (i % 7 == 0) { return (0, [0]); }
    return (i, [i, i + 1]);
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"tupown-local-before-early-lit", `function mk(i: i32): (i32, i32[]) {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    if (i % 7 == 0) { return (0, [0]); }
    return t;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"tupown-two-locals", `function mk(i: i32): (i32, i32[]) {
    if (i % 2 == 0) { let t: (i32, i32[]) = (i, [i, i + 1]); return t; }
    let u: (i32, i32[]) = (i, [i, i + 1]);
    return u;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let r: (i32, i32[]) = mk(i); t = t + r.0 + r.1.len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"tupown-loop-resident-callee", `function mk(n: i32): (i32, i32[]) {
    let j: i32 = 0;
    while (j < n) {
        let t: (i32, i32[]) = (j, [j, j + 1]);
        if (j >= n - 1) { return t; }
        j = j + 1;
    }
    return (0, [0]);
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let r: (i32, i32[]) = mk(3 + (i % 4)); t = t + r.0 + r.1.len(); i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
	{"tupown-elem-extract-return", `function mk(i: i32): (i32, i32[]) {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    return t;
}
function pick(i: i32): i32[] {
    let r: (i32, i32[]) = mk(i);
    return r.1;
}
function churn(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let xs: i32[] = pick(i); t = t + xs.len() + xs[0]; i = i + 1; }
    return t % 251;
}
function main(): i32 {
    let w: i32 = churn(200);
    let b1: i64 = __heap_bump_bytes();
    let x: i32 = churn(200);
    let b2: i64 = __heap_bump_bytes();
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return ((b2 - b1) / 200) as i32;
}`, 0},
}

const tupleOwnedRetFailFmt = "%s = %d, want %d (a small non-zero is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostTupleOwnedRetReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleOwnedRetReleaseCases {
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
				t.Errorf(tupleOwnedRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTupleOwnedRetReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleOwnedRetReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(tupleOwnedRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTupleOwnedRetReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping owned tuple return release wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupleOwnedRetReleaseCases {
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
			if code := rcmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(tupleOwnedRetFailFmt, tc.name, code, tc.want)
			}
		})
	}
}
