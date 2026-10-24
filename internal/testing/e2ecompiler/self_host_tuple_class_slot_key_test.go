package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type tupClassKeyCase struct {
	name string
	src  string
	want int
}

func tupClassKeyCases() []tupClassKeyCase {
	return []tupClassKeyCase{
		{
			name: "sibling_blocks",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let acc: i32 = 0;
    { let t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    { let t: (i32, i32[]) = (i, [i + 7, i + 9]); acc = acc + t.1[1]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 10,
		},
		{
			name: "sibling_blocks_reversed",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let acc: i32 = 0;
    { let t: (i32, i32[]) = (i, [i + 7, i + 9]); acc = t.1[1]; }
    { let t: (i32, i32[]) = (i, xs); acc = acc + t.1[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 10,
		},
		{
			name: "if_arms",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let acc: i32 = 0;
    if (i % 2 == 0) { let t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    else { let t: (i32, i32[]) = (i, [i + 7, i + 9]); acc = t.1[1]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 5,
		},
		{
			name: "loop_bodies",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 2) { let t: (i32, i32[]) = (i, xs); acc = acc + t.1[0]; k = k + 1; }
    let m: i32 = 0;
    while (m < 2) { let t: (i32, i32[]) = (i, [i + 7, i + 9]); acc = acc + t.1[1]; m = m + 1; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 20,
		},
		{
			name: "same_class_both_blocks",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let acc: i32 = 0;
    { let t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    { let t: (i32, i32[]) = (i, xs); acc = acc + t.1[1]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40,
		},
		{
			name: "distinct_names",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let acc: i32 = 0;
    { let t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    { let u: (i32, i32[]) = (i, [i + 7, i + 9]); acc = acc + u.1[1]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 10,
		},
	}
}

func leakSummaryLine(stderr string) string {
	out := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			out = line
		}
	}
	return out
}

func TestSelfHostTupleClassSlotKeyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupClassKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupclasskey_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: two same-named "+
					"tuple locals resolved one another's class credit)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — allocs and frees must balance at live_bytes 0. A site "+
					"key that resolves to no credit leaks here instead of over-releasing, "+
					"which the exit code above would not show", tc.name, summary)
			}
		})
	}
}

func TestSelfHostTupleClassSlotKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple class slot-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupClassKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "tupclasskey_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple class slot-key wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostTupleClassSlotKeyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupClassKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupclasskey_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
