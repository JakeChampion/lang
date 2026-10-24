package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type enumFieldShareCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const efsMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func enumFieldShareCases() []enumFieldShareCase {
	const decls = `struct P { e: E, n: i32 }
enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
`
	return []enumFieldShareCase{
		{
			name: "local_no_match",
			src: decls + `function round(i: i32): i32 {
    let src: E = E.A([i, i + 5]);
    let p: P = P { e: src, n: i };
    return p.n % 101;
}
` + efsMain,
			want: 3, allocs: 300, frees: 300,
		},
		{
			name: "local_then_match",
			src: decls + `function round(i: i32): i32 {
    let src: E = E.A([i, i + 5]);
    let p: P = P { e: src, n: i };
    let t: i32 = 0;
    match (src) { E.A(xs) => { t = xs.len(); }, E.B => { t = 0; } }
    return (t + p.n) % 101;
}
` + efsMain,
			want: 5, allocs: 300, frees: 300,
		},
		{
			name: "call_bound_local",
			src: decls + `function round(i: i32): i32 {
    let src: E = mkv(i);
    let p: P = P { e: src, n: i };
    return p.n % 101;
}
` + efsMain,
			want: 3, allocs: 300, frees: 300,
		},
		{
			name: "conditional_share",
			src: decls + `function round(i: i32): i32 {
    let src: E = mkv(i);
    let t: i32 = 0;
    if (i % 2 == 0) {
        let p: P = P { e: src, n: i };
        t = (match (p.e) { E.A(xs) => xs.len(), E.B => 0 }) + p.n;
    }
    return t % 101;
}
` + efsMain,
			want: 28, allocs: 250, frees: 250,
		},
		{
			name: "call_arg_borrowed_by_callee",
			src: decls + `function sink(e: E, k: i32): i32 {
    return (match (e) { E.A(xs) => xs.len(), E.B => 0 }) + k;
}
function round(i: i32): i32 {
    let src: E = E.A([i, i + 5]);
    let p: P = P { e: src, n: i };
    return (sink(src, p.n)) % 101;
}
` + efsMain,
			want: 5, allocs: 300, frees: 300,
		},
	}
}

func TestSelfHostEnumFieldShareX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range enumFieldShareCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "enumfieldshare_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a release ran "+
					"under a live claim)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want frees=%d. FEWER means a release credit stopped "+
					"resolving; MORE on the refused row means the carve-out reached a "+
					"position no retain covers", tc.name, summary, tc.frees)
			}
		})
	}
}

func TestSelfHostEnumFieldShareWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping enum field-share wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range enumFieldShareCases() {
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
			watFile := filepath.Join(dir, "enumfieldshare_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("enum field-share wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostEnumFieldShareIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range enumFieldShareCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "enumfieldshare_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
