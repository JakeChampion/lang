package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type tupIdentElemCase struct {
	name string
	src  string
	want int
}

func tupIdentElemCases() []tupIdentElemCase {
	return []tupIdentElemCase{
		{
			name: "ident_elem_array",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    return t.0 + t.1[0] + t.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 10,
		},
		{
			name: "two_ident_elems",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let ys: i32[] = [i + 2, i + 3];
    let t: (i32[], i32[]) = (xs, ys);
    return t.0[0] + t.1[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 74,
		},
		{
			name: "ident_read_after",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    return t.1[0] + xs[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 40,
		},
		{
			name: "param_ident_elem",
			src: `function use_it(xs: i32[], i: i32): i32 {
    let t: (i32, i32[]) = (i, xs);
    return t.1[0] + t.1[1];
}
function main(): i32 { let xs: i32[] = [7, 11]; let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + use_it(xs, r); r = r + 1; } return x % 83; }`,
			want: 57,
		},
		{
			name: "last_use_before_return",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let acc: i32 = t.1[0];
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53,
		},
		{
			name: "last_use_scalar_read",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let acc: i32 = t.0;
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 53,
		},
		{
			name: "same_name_sibling_blocks",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let acc: i32 = 0;
    { let t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    { let t: (i32[], i32) = (xs, i); acc = acc + t.0[1]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40,
		},
		{
			name: "untaken_branch_null",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    if (i % 2 == 0) { let xs: i32[] = [i, i + 1]; let t: (i32, i32[]) = (i, xs); acc = t.1[0]; }
    return acc;
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 43,
		},
	}
}

func TestSelfHostTupleIdentElemExtractionHazardX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			name: "elem_extracted_escaping",
			src: `function grab(i: i32): i32[] {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    return t.1;
}
function decoy(n: i32): i32[] { return [n * 7, n * 11, n * 13]; }
function round(i: i32): i32 {
    let u: i32[] = grab(i);
    let d: i32[] = decoy(i);
    if (d[0] < 0) { return 0; }
    return u[0] + u[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40,
		},
		{
			name: "elem_extracted_local",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32, i32[]) = (i, xs);
    let u: i32[] = t.1;
    return u[0] + u[1];
}
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 40,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "tupextract_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("%s exited %d, want %d (99 = rc underflow: the element release "+
					"claimed a reference the frame handed to another owner)", tc.name, exit, tc.want)
			}
		})
	}
}

func TestSelfHostTupleIdentElemRetainX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupIdentElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupidentelem_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (both oracles agree on %d)", tc.name, exit, tc.want, tc.want)
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
				t.Errorf("%s: %s — live_bytes must be 0. The construction retain is per "+
					"round, so anything stranded here scales with the loop", tc.name, summary)
			}
			if allocs != frees {
				t.Errorf("%s: %s — allocs and frees must balance exactly; frees above "+
					"allocs is a double free, not an improvement", tc.name, summary)
			}
		})
	}
}

func TestSelfHostTupleIdentElemRetainWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple ident-elem retain wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range tupIdentElemCases() {
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
			watFile := filepath.Join(dir, "tupidentelem_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("tuple ident-elem retain wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostTupleIdentElemRetainIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupIdentElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupidentelem_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
