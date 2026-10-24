package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type structProducerCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func structProducerCases() []structProducerCase {
	return []structProducerCase{
		{
			name: "producer_returns_local",
			src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
function mk(): P { let p: P = P { xs: [1,2,3], s: w("p") }; return p; }
function round(i: i32): i32 { let v: P = mk(); return v.xs.len(); }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 200) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 19, allocs: 400, frees: 400,
		},
		{
			name: "producer_returns_literal",
			src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
function mk(): P { return P { xs: [1,2,3], s: w("p") }; }
function round(i: i32): i32 { let v: P = mk(); return v.xs.len(); }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 200) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 19, allocs: 400, frees: 400,
		},
		{
			name: "inline_no_producer",
			src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }

function round(i: i32): i32 { let v: P = P { xs: [1,2,3], s: w("p") }; return v.xs.len(); }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 200) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 19, allocs: 400, frees: 400,
		},
		{
			name: "minimal_array_field",
			src: `struct P { xs: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
function mk(): P { let p: P = P { xs: id([1, 2, 3]) }; return p; }
function round(i: i32): i32 { let v: P = mk(); return v.xs.len(); }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 51, allocs: 100, frees: 100,
		},
		{
			name: "sibling_alias",
			src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
function mk(): P { let p: P = P { xs: [1,2,3], s: w("p") }; return p; }
function round(base: P, i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: P = mk();  t = t + v.xs.len(); }
    if (i % 2 == 1) { let v: P = base;  t = t + v.xs.len(); }
    return t;
}
function main(): i32 {
    let b: P = P { xs: [7, 8], s: w("b") };
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 1, allocs: 102, frees: 102,
		},
		{
			name: "sibling_alias_renamed",
			src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
function mk(): P { let p: P = P { xs: [1,2,3], s: w("p") }; return p; }
function round(base: P, i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: P = mk();  t = t + v.xs.len(); }
    if (i % 2 == 1) { let u: P = base;  t = t + u.xs.len(); }
    return t;
}
function main(): i32 {
    let b: P = P { xs: [7, 8], s: w("b") };
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 1, allocs: 102, frees: 102,
		},
		{
			name: "field_read_admitted",
			src: `struct P { xs: i32[] }
function mk(i: i32): P { let p: P = P { xs: [i, i + 1] }; let stolen: i32[] = p.xs; return p; }
function round(i: i32): i32 { let v: P = mk(i); return v.xs.len(); }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 200, frees: 200,
		},
		{
			name: "refused_param_returned",
			src: `struct P { xs: i32[] }
@noinline function mk(p: P): P { return p; }
function round(i: i32): i32 { let src: P = P { xs: [i, i + 1] }; let v: P = mk(src); return v.xs.len(); }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 200, frees: 200,
		},
		{
			name: "refused_aliased",
			src: `struct P { xs: i32[] }
function keepit(q: P): i32 { return q.xs.len(); }
function mk(i: i32): P { let p: P = P { xs: [i, i + 1] }; let alias: P = p; return p; }
function round(i: i32): i32 { let v: P = mk(i); return v.xs.len(); }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 200, frees: 200,
		},
		{
			name: "admitted_reassigned_all_fresh",
			src: `struct P { xs: i32[] }
@noinline function hold(p: P): i32 { return 0; }
function mk(i: i32): P { let p: P = P { xs: [i, i + 1] }; p = P { xs: [i + 2 + hold(p), i + 3] }; return p; }
function round(i: i32): i32 { let v: P = mk(i); return v.xs.len(); }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 300, frees: 300,
		},
		{
			name: "two_declarations",
			src: `struct P { xs: i32[] }
function mk(i: i32): P {
    if (i % 2 == 0) { let p: P = P { xs: [i, i + 1] }; return p; }
    let p: P = P { xs: [i + 2, i + 3] };
    return p;
}
function round(i: i32): i32 { let v: P = mk(i); return v.xs.len(); }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 200, frees: 200,
		},
		{
			name: "refused_receiver_field_move",
			src: `struct P { xs: i32[] }
function (p: P) grow(): P { return P { xs: p.xs }; }
function mk(i: i32): P { let p: P = P { xs: [i, i + 1] }; let q: P = p.grow(); return p; }
function round(i: i32): i32 { let v: P = mk(i); return v.xs.len(); }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 300, frees: 300,
		}}
}

func TestSelfHostStructProducerLocalX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structProducerCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "structprod_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = the "+
					"segfault this widening caused on the name-keyed table)", tc.name, exit, tc.want)
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
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d", tc.name, summary, tc.frees)
			}
			if live != 0 {
				t.Errorf("%s: %s — want live_bytes=0", tc.name, summary)
			}
		})
	}
}

func TestSelfHostStructProducerLocalWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping struct producer-local wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range structProducerCases() {
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
			watFile := filepath.Join(dir, "structprod_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("struct producer-local wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostStructProducerLocalIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structProducerCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "structprod_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
