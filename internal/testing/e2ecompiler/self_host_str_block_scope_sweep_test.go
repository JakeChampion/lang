package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const strBlockW = "function w(a: string): string { return a + \"!\"; }\n"

const strBlockMain = "\nfunction main(): i32 { let x: i32 = 0; let r: i32 = 0; " +
	"while (r < 100) { x = x + round(r); r = r + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return x % 83; }"

type strBlockCase struct {
	name string
	src  string
	want int
}

func strBlockCases() []strBlockCase {
	return []strBlockCase{
		{
			name: "plain_block",
			src: strBlockW + `function round(i: i32): i32 {
    let acc: i32 = 0;
    { let s: string = w("ab"); acc = acc + s.len(); }
    return acc + i;
}` + strBlockMain,
			want: 21,
		},
		{
			name: "loop_body",
			src: strBlockW + `function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 3) { let s: string = w("ab"); acc = acc + s.len(); k = k + 1; }
    return acc + i;
}` + strBlockMain,
			want: 40,
		},
		{
			name: "if_arm",
			src: strBlockW + `function round(i: i32): i32 {
    let acc: i32 = 0;
    if (i % 3 == 0) { let s: string = w("abc"); acc = acc + s.len(); }
    else { let s: string = w("de"); acc = acc + s.len(); }
    return acc + i;
}` + strBlockMain,
			want: 55,
		},
		{
			name: "function_scope_unchanged",
			src: strBlockW + `function round(i: i32): i32 {
    let s: string = w("ab");
    return s.len() + i;
}` + strBlockMain,
			want: 21,
		},
		{
			name: "untaken_branch",
			src: strBlockW + `function round(i: i32): i32 {
    let acc: i32 = 0;
    if (i % 2 == 0) { let s: string = w("ab"); acc = acc + s.len(); }
    return acc + i;
}` + strBlockMain,
			want: 37,
		},
		{
			name: "move_on_return_not_swept",
			src: strBlockW + `function build(n: i32): string { let acc: string = w("a"); return acc; }
function round(i: i32): i32 { let s: string = build(i); return s.len() + i; }` + strBlockMain,
			want: 4,
		},
	}
}

func TestSelfHostStrBlockScopeSweepX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strBlockCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strblock_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the sweep claimed a "+
					"box that was moved out, or a slot that never held one)", tc.name, exit, tc.want)
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
				t.Fatalf("%s allocated nothing — the probe is not exercising the path. "+
					"Every string here comes from w(), never a fold-able literal", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — the box is allocated per round, so a missing release "+
					"scales with the loop", tc.name, summary)
			}
		})
	}
}

func TestSelfHostStrBlockScopeSweepWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping block-scoped string sweep wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strBlockCases() {
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
			watFile := filepath.Join(dir, "strblock_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("block-scoped string sweep wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostStrBlockScopeSweepIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strBlockCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "strblock_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
