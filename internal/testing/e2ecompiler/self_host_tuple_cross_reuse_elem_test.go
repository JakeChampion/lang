package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type tupXReuseElemCase struct {
	name   string
	src    string
	want   int
	allocs int64
}

func tupXReuseElemCases() []tupXReuseElemCase {
	return []tupXReuseElemCase{
		{
			name: "donor_and_recipient_idents",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let a: i32 = t.1[0];
    let ys: i32[] = [i + 2, i + 3];
    let u: (i32, i32[]) = (i, ys);
    return a + u.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 300,
		},
		{
			name: "recipient_array_literal",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let a: i32 = t.1[0];
    let u: (i32, i32[]) = (i, [i + 2, i + 3]);
    return a + u.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 300,
		},
		{
			name: "donor_source_read_after",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let a: i32 = t.1[0];
    let ys: i32[] = [i + 2, i + 3];
    let u: (i32, i32[]) = (i, ys);
    return a + u.1[1] + xs[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 61, allocs: 300,
		},
		{
			name: "two_retained_positions",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let ys: i32[] = [i + 2, i + 3];
    let t: (i32[], i32[]) = (xs, ys);
    let a: i32 = t.0[0] + t.1[1];
    let ps: i32[] = [i + 4, i + 5];
    let qs: i32[] = [i + 6, i + 7];
    let u: (i32[], i32[]) = (ps, qs);
    return a + u.0[1] + u.1[0];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 35, allocs: 500,
		},
		{
			name: "chained_donor",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let a: i32 = t.1[0];
    let u: (i32, i32[]) = (i, [i + 2, i + 3]);
    let b: i32 = u.1[1];
    let v: (i32, i32[]) = (i, [i + 4, i + 5]);
    return a + b + v.1[0];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 29, allocs: 400,
		},
		{
			name: "param_ident_donor",
			src: `function feed(xs: i32[], i: i32): i32 {
    let t: (i32, i32[]) = (i, xs);
    let a: i32 = t.1[0];
    let ys: i32[] = [i + 2, i + 3];
    let u: (i32, i32[]) = (i, ys);
    return a + u.1[1];
}
function main(): i32 { let xs: i32[] = [7, 11]; let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + feed(xs, r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 57, allocs: 200,
		},
		{
			name: "loop_carried_recipient_literal",
			src: `function run(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let xs: i32[] = [i, i + 1];
        let t: (i32, i32[]) = (i, xs);
        let a: i32 = t.1[0];
        let u: (i32, i32[]) = (i, [i + 2, i + 3]);
        acc = acc + a + u.1[1];
        i = i + 1;
    }
    return acc;
}
function main(): i32 { let x: i32 = run(100); if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 300,
		},
		{
			name: "loop_carried_recipient_ident",
			src: `function run(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let xs: i32[] = [i, i + 1];
        let t: (i32, i32[]) = (i, xs);
        let a: i32 = t.1[0];
        let ys: i32[] = [i + 2, i + 3];
        let u: (i32, i32[]) = (i, ys);
        acc = acc + a + u.1[1];
        i = i + 1;
    }
    return acc;
}
function main(): i32 { let x: i32 = run(100); if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 74, allocs: 300,
		},
		{
			name: "recipient_source_read_after",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let a: i32 = t.1[0];
    let ys: i32[] = [i + 2, i + 3];
    let u: (i32, i32[]) = (i, ys);
    let b: i32 = u.1[1];
    return a + b + ys[0];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 78, allocs: 300,
		},
	}
}

func TestSelfHostTupleCrossReuseElemX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupXReuseElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupxreuse_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the donor release "+
					"claimed a reference someone else still owns)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want allocs=%d. This count is below the no-reuse "+
					"count by one box per round, so a change here means the cross-tuple "+
					"pairing stopped firing and the release assertions below went vacuous",
					tc.name, summary, tc.allocs)
			}
			if live != 0 {
				t.Errorf("%s: %s — live_bytes must be 0. The donor's children are "+
					"stranded per round, so anything here scales with the loop", tc.name, summary)
			}
			if allocs != frees {
				t.Errorf("%s: %s — allocs and frees must balance exactly; frees above "+
					"allocs is a double free, not an improvement", tc.name, summary)
			}
		})
	}
}

func TestSelfHostTupleCrossReuseElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple cross-reuse element wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupXReuseElemCases() {
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
			watFile := filepath.Join(dir, "tupxreuse_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple cross-reuse element wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostTupleCrossReuseElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupXReuseElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupxreuse_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
