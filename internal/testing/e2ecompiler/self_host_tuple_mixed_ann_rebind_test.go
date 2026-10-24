package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type tupMixedAnnCase struct {
	name string
	src  string
	want int
}

func tupMixedAnnCases() []tupMixedAnnCase {
	return []tupMixedAnnCase{
		{
			name: "compound_scalar_elem",
			src: `function round(i: i32): i32 {
    let ys: i32[] = [i, i + 1, i + 2, i + 3];
    let u: (i32, i32[]) = (i + 1, ys);
    return u.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 75,
		},
		{
			name: "assign_rebind",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let ys: i32[] = [i + 2, i + 3];
    let t: (i32, i32[]) = (i, xs);
    t = (i + 1, ys);
    return t.1[0];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 8,
		},
		{
			name: "two_tuples_one_ident",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let u: (i32, i32[]) = (i + 1, xs);
    return t.1[0] + u.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 39,
		},
		{
			name: "branch_rebind",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let ys: i32[] = [i + 2, i + 3];
    let t: (i32, i32[]) = (i, xs);
    if (i % 2 == 0) { t = (i + 1, ys); }
    return t.1[0];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 75,
		},
		{
			name: "old_elem_read_after_rebind",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let ys: i32[] = [i + 2, i + 3];
    let t: (i32, i32[]) = (i, xs);
    t = (i + 1, ys);
    return t.1[0] + xs[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 83,
		},
		{
			name: "elem_read_after",
			src: `function round(i: i32): i32 {
    let ys: i32[] = [i, i + 1, i + 2, i + 3];
    let u: (i32, i32[]) = (i + 1, ys);
    let s: i32 = u.1[1];
    return s + ys[0];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 39,
		},
	}
}

func TestSelfHostTupleMixedAnnRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupMixedAnnCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupmixed_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (both oracles agree on %d; an offset of "+
					"+N is N rc underflows)", tc.name, exit, tc.want, tc.want)
			}
			summary := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					summary = line
				}
			}
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
			if live != 0 {
				t.Errorf("%s: %s — live_bytes must be 0; the retain is per round, so "+
					"anything stranded scales with the loop", tc.name, summary)
			}
			if allocs != frees {
				t.Errorf("%s: %s — allocs and frees must balance exactly", tc.name, summary)
			}
		})
	}
}

func TestSelfHostTupleMixedAnnHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []tupMixedAnnCase{
		{
			name: "elem_extracted_escaping",
			src: `function keep(i: i32): i32[] {
    let ys: i32[] = [i, i + 1];
    let u: (i32, i32[]) = (i + 1, ys);
    return u.1;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { let k: i32[] = keep(r); x = x + k[0]; r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 53,
		},
		{
			name: "rc_literal_child_stays_tuprc",
			src: `function round(i: i32): i32 {
    let u: (i32, i32[]) = (i + 1, [i, i + 2]);
    return u.1[0];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 200) { x = x + round(r); r = r + 1; } return (x % 89) + __rc_underflow_count(); }`,
			want: 53,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "tupmixedhaz_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("%s exited %d, want %d (an offset of +N is N rc underflows — "+
					"a wrongly granted credit, not a wrong sum)", tc.name, exit, tc.want)
			}
		})
	}
}

func TestSelfHostTupleMixedAnnRebindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple mixed-ann rebind wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupMixedAnnCases() {
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
			watFile := filepath.Join(dir, "tupmixed_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple mixed-ann rebind wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostTupleMixedAnnRebindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupMixedAnnCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupmixed_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
