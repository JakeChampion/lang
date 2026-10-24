package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type strArrFwdCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func strArrFwdCases() []strArrFwdCase {
	return []strArrFwdCase{
		{
			name: "forwarder_called_len",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + keep.get().len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 500,
		},
		{
			name: "forwarder_declared_never_called",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
@noinline function hold(h: Holder): i32 { return 0; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + 3 + hold(keep);
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 500,
		},
		{
			name: "free_fn_forwarder_called_len",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function grab(h: Holder): string[] { return h.xs; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + grab(keep).len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 500,
		},
		{
			name: "forwarder_holder_outlives_churn",
			src: `struct Holder { xs: string[] }
function (h: Holder) get(): string[] { return h.xs; }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function main(): i32 {
    let live: Holder = Holder { xs: [w("y"), w("z")] };
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + keep.get().len() + live.get().len();
        i = i + 1;
    }
    t = t + live.get().len() * 7;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 1, allocs: 504, frees: 504,
		},
		{
			name: "no_forwarder_direct_read",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + keep.xs.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 500,
		},
		{
			name: "forwarder_result_bound",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        let got: string[] = keep.get();
        t = t + got.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 500,
		},
		{
			name: "forwarder_element_read",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        let s0: string = keep.get()[0];
        t = t + s0.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 18, allocs: 500, frees: 500,
		},
		{
			name: "forwarder_iterated",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        for s in keep.get() { t = t + s.len(); }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 16, allocs: 500, frees: 500,
		},
		{
			name: "free_fn_forwarder_result_bound",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function grab(h: Holder): string[] { return h.xs; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        let got: string[] = grab(keep);
        t = t + got.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 500, frees: 500,
		},
		{
			name: "forwarder_element_escapes_by_call",
			src: `struct Holder { xs: string[] }
function (h: Holder) get(): string[] { return h.xs; }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function stash(h: Holder): string { return h.get()[0]; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        let got: string = stash(keep);
        t = t + got.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 18, allocs: 500, frees: 500,
		},
		{
			name: "two_field_forwarder_not_registered",
			src: `struct Holder { xs: string[], ys: string[] }
function (h: Holder) get(pick: boolean): string[] { if (pick) { return h.xs; } return h.ys; }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b")], ys: [w("c")] };
        t = t + keep.get(true).len() + keep.get(false).len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 15, allocs: 600, frees: 600,
		},
		{
			name: "shared_element_two_holders",
			src: `struct Holder { xs: string[] }
function (h: Holder) get(): string[] { return h.xs; }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let s: string = w("a");
        let h1: Holder = Holder { xs: [s, s] };
        let h2: Holder = Holder { xs: [s] };
        t = t + h1.get().len() + h2.get().len() + s.len();
        let j1: string = w("p");
        let j2: string = w("q");
        t = t + (s[0] as i32) + s.len() + j1.len() - j1.len() + j2.len() - j2.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 4, allocs: 700, frees: 700,
		},
		{
			name: "forwarder_len_plus_direct_element",
			src: `struct Holder { xs: string[] }
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-0123456789"; }
function (h: Holder) get(): string[] { return h.xs; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let keep: Holder = Holder { xs: [w("a"), w("b"), w("c")] };
        t = t + keep.get().len();
        let j1: string = w("p");
        let j2: string = w("q");
        let j3: string = w("r");
        t = t + (keep.xs[0][0] as i32) + (keep.xs[2][0] as i32) + keep.xs[1].len()
            + j1.len() - j1.len() + j2.len() - j2.len() + j3.len() - j3.len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 19;
}`,
			want: 6, allocs: 800, frees: 800,
		},
	}
}

func TestSelfHostStrArrFieldForwarderX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strArrFwdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "safwd_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; any other change means a "+
					"reclaim reached a buffer the program still reads)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want frees=%d",
					tc.name, summary, tc.frees)
			}
		})
	}
}

func TestSelfHostStrArrFieldForwarderWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string[]-field forwarder wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strArrFwdCases() {
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
			watFile := filepath.Join(dir, "safwd_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("string[]-field forwarder wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostStrArrFieldForwarderIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strArrFwdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "safwd_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
