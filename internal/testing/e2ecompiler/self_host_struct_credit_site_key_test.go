package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type structKeyCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const structKeyP = `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
`

const structKeyMainB = `
function main(): i32 {
    let b: P = P { xs: [7, 8], s: w("b") };
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`

const structKeyMain = `
function main(): i32 { let t: i32 = 0; let i: i32 = 0; ` +
	`while (i < 100) { t = t + round(i); i = i + 1; } ` +
	`if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`

func structKeyCases() []structKeyCase {
	return []structKeyCase{
		{
			name: "collide_literal",
			src: structKeyP + `function round(base: P, i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
    if (i % 2 == 1) { let v: P = base;  t = t + v.xs.len(); }
    return t;
}` + structKeyMainB,
			want: 34, allocs: 152, frees: 152,
		},
		{
			name: "collide_literal_renamed",
			src: structKeyP + `function round(base: P, i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
    if (i % 2 == 1) { let u: P = base;  t = t + u.xs.len(); }
    return t;
}` + structKeyMainB,
			want: 34, allocs: 152, frees: 152,
		},
		{
			name: "collide_loop",
			src: structKeyP + `function round(base: P, i: i32): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 2) {
        if (k == 0) { let v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
        if (k == 1) { let v: P = base; t = t + v.xs.len(); }
        k = k + 1;
    }
    return t;
}` + structKeyMainB,
			want: 68, allocs: 302, frees: 302,
		},
		{
			name: "block_scoped",
			src: structKeyP + `function round(i: i32): i32 {
    let t: i32 = 0;
    { let v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
    return t;
}` + structKeyMain,
			want: 34, allocs: 300, frees: 300,
		},
		{
			name: "fn_scoped",
			src: structKeyP + `function round(i: i32): i32 {
    let v: P = P { xs: [i, i + 1], s: w("p") };
    return v.xs.len();
}` + structKeyMain,
			want: 34, allocs: 300, frees: 300,
		},
		{
			name: "producer_literal",
			src: structKeyP + `function mk(i: i32): P { return P { xs: [i, i + 1], s: w("p") }; }
function round(i: i32): i32 {
    let v: P = mk(i);
    return v.xs.len();
}` + structKeyMain,
			want: 34, allocs: 300, frees: 300,
		},
		{
			name: "producer_local_now_credited",
			src: structKeyP + `function mk(i: i32): P { let p: P = P { xs: [i, i + 1], s: w("p") }; return p; }
function round(i: i32): i32 {
    let v: P = mk(i);
    return v.xs.len();
}` + structKeyMain,
			want: 34, allocs: 300, frees: 300,
		},
		{
			name: "builder_nodeep",
			src: `struct B { ops: i32[] }
function (b: B) emit(x: i32): B { return B { ops: b.ops.append(x) }; }
@noinline function id(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let s: B = B { ops: id([1]) };
    s = s.emit(i);
    s = s.emit(i + 1);
    return s.ops.len();
}` + structKeyMain,
			want: 51, allocs: 500, frees: 500,
		},
	}
}

func TestSelfHostStructCreditSiteKeyX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "structkey_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a same-named "+
					"struct local inherited another binding's reclaim credit)", tc.name, exit, tc.want)
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
			if tc.allocs >= 0 && allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d. A change in what is ALLOCATED means the "+
					"probe stopped measuring this shape", tc.name, summary, tc.allocs)
			}
			if tc.frees >= 0 && frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d", tc.name, summary, tc.frees)
			}
		})
	}
}

func TestSelfHostStructCreditSiteKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping struct credit site-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range structKeyCases() {
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
			watFile := filepath.Join(dir, "structkey_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("struct credit site-key wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostStructCreditSiteKeyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "structkey_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
