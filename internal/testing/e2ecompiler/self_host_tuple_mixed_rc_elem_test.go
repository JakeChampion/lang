package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type tupMixedRcCase struct {
	name    string
	src     string
	want    int
	balance bool
}

const tupMixedRcMain = "\nfunction main(): i32 { let x: i32 = 0; let r: i32 = 0; " +
	"while (r < 100) { x = x + round(r); r = r + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return x % 83; }"

func tupMixedRcCases() []tupMixedRcCase {
	return []tupMixedRcCase{
		{
			name: "mixed_sweep",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			name: "mixed_literal_first",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32[], i32[]) = ([i + 2, i + 3], xs);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			name: "all_ident",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let ys: i32[] = [i + 4, i + 5];
    let t: (i32[], i32[]) = (xs, ys);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 25, balance: true,
		},
		{
			name: "all_fresh",
			src: `function round(i: i32): i32 {
    let t: (i32[], i32[]) = ([i, i + 1], [i + 2, i + 3]);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			name: "scalar_ident",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    return t.1[0] + t.1[1];
}` + tupMixedRcMain,
			want: 40, balance: true,
		},
		{
			name: "dup_ident",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32[], i32[]) = (xs, xs);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 40, balance: true,
		},
		{
			name: "param_element",
			src: `function round(base: i32[], i: i32): i32 {
    let t: (i32[], i32[]) = (base, [i + 2, i + 3]);
    return t.0[0] + t.1[1];
}
function main(): i32 { let b: i32[] = [7, 8]; let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(b, r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 57, balance: true,
		},
		{
			name: "sibling_alias",
			src: `function round(base: i32[], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: (i32[], i32[]) = (base, [i + 1, i + 2]); t = t + v.1[0]; }
    if (i % 2 == 1) { let v: (i32[], i32[]) = (base, base); t = t + v.1[0]; }
    return t;
}
function main(): i32 { let b: i32[] = [7, 8]; let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 28, balance: true,
		},
		{
			name: "nested_mixed",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, (i32[], i32[])) = (i, (xs, [i + 2, i + 3]));
    return t.1.0[0] + t.1.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			name: "unannotated_source",
			src: `function mk(i: i32): i32[] { return [i, i + 1]; }
function round(i: i32): i32 {
    let xs = mk(i);
    let t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			name: "loop_scoped",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 3) { let t: (i32[], i32[]) = (xs, [i + k, i + k + 1]); acc = acc + t.1[0]; k = k + 1; }
    return acc;
}` + tupMixedRcMain,
			want: 44, balance: true,
		},
		{
			name: "rebound",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    let k: i32 = 0;
    while (k < 3) { t = (xs, [i + k, i + k + 1]); k = k + 1; }
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			name: "discarded_literal",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    (xs, [i + 2, i + 3]);
    return xs[0];
}` + tupMixedRcMain,
			want: 53, balance: true,
		},
		{
			name: "elem_escapes_still_leaks",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    let keep: i32[] = t.0;
    return keep[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74,
		},
	}
}

func TestSelfHostTupleMixedRcElemX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupMixedRcCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupmixedrc_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a position was "+
					"released that the construction never retained)", tc.name, exit, tc.want)
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
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0. A mixed rc tuple in "+
					"neither class releases nothing at all, box included", tc.name, summary)
			}
		})
	}
}

func TestSelfHostTupleMixedRcElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping mixed rc tuple element wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupMixedRcCases() {
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
			watFile := filepath.Join(dir, "tupmixedrc_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("mixed rc tuple element wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostTupleMixedRcElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupMixedRcCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupmixedrc_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
