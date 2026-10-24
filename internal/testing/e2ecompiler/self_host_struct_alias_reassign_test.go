package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type structAliasReassignCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const sarMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 20) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func structAliasReassignCases() []structAliasReassignCase {
	return []structAliasReassignCase{
		{
			name: "struct_alias_reassign",
			src: `struct P { xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let p: P = P { xs: id([7, 8]) };
    let keep: P = P { xs: id([0]) };
    let w: i32 = keep.xs.len();
    keep = p;
    return keep.xs[0] + keep.xs[keep.xs.len() - 1] + w - 1;
}
` + sarMain,
			want: 9, allocs: 40, frees: 40,
		},
		{
			name: "reassign_read_back_after_churn",
			src: `struct P { xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let p: P = P { xs: id([7, 8]) };
    let keep: P = P { xs: id([0]) };
    let w: i32 = keep.xs.len();
    keep = p;
    let j1: i32[] = [111, 222];
    let j2: i32[] = [333, 444];
    return keep.xs[0] + keep.xs[keep.xs.len() - 1] + j1[0] - j1[0] + j2[0] - j2[0] + w - 1;
}
` + sarMain,
			want: 9, allocs: 40, frees: 40,
		},
		{
			name: "struct_alias_bind_unchanged",
			src: `struct P { xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let p: P = P { xs: id([7, 8]) };
    let keep: P = p;
    return keep.xs[0] + keep.xs[keep.xs.len() - 1];
}
` + sarMain,
			want: 9, allocs: 20, frees: 20,
		},
		{
			name: "array_reassign_unchanged",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [7, i - i + 8];
    let keep: i32[] = [0];
    keep = xs;
    return keep[0] + keep[keep.len() - 1];
}
` + sarMain,
			want: 9, allocs: 20, frees: 20,
		},
		{
			name: "fresh_rhs_reassign_unchanged",
			src: `struct P { xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let keep: P = P { xs: id([0]) };
    let w: i32 = keep.xs.len();
    keep = P { xs: id([7, 8]) };
    return keep.xs[0] + keep.xs[keep.xs.len() - 1] + w - 1;
}
` + sarMain,
			want: 9, allocs: 20, frees: 20,
		},
		{
			name: "string_accumulator_unchanged",
			src: `function round(i: i32): i32 {
    let s: string = "";
    let k: i32 = 0;
    while (k < 4) { s = s + "ab"; k = k + 1; }
    return s.len();
}
` + sarMain,
			want: 63, allocs: 20, frees: 20,
		},
	}
}

func TestSelfHostStructAliasReassignX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structAliasReassignCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "sarea_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the credit was "+
					"granted without the retain that pays for it)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d", tc.name, summary, tc.frees)
			}
		})
	}
}

func TestSelfHostStructAliasReassignWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping struct alias-reassign wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range structAliasReassignCases() {
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
			watFile := filepath.Join(dir, "sarea_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("struct alias-reassign wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostStructAliasReassignIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structAliasReassignCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "sarea_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
