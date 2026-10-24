package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var closureAliasIRCases = []struct {
	name string
	src  string
	want int
}{
	{"bare-alias-call",
		`function main(): i32 {
    let k: i32 = 3;
    let c = () => k + 1;
    let d = c;
    return d();
}`, 4},
	{"alias-both-names-array-capture",
		`function go(k: i32): i32 {
    let xs: i32[] = [k, k + 1];
    let c = () => xs[0] + xs[1];
    let d = c;
    let a: i32 = c();
    let b: i32 = d();
    if (a != b) { return 98; }
    return a;
}
function main(): i32 { return go(3); }`, 7},
	{"param-alias",
		`function apply1(f: (i32) => i32): i32 { let g = f; return g(4); }
function main(): i32 { let k: i32 = 5; let c = (x: i32) => x * k; return apply1(c); }`, 20},
	{"chained-and-branch-alias",
		`function main(): i32 {
    let k: i32 = 2;
    let c = () => k + 5;
    let d = c;
    let e = d;
    let r1: i32 = e();
    let pick: boolean = true;
    let f = c;
    if (pick) { f = d; }
    return r1 * 10 + f();
}`, 77},
}

func TestSelfHostClosureAliasIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range closureAliasIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			if code := runInterpExit(t, tc.src+"\n"); code != tc.want {
				t.Fatalf("%s interpreter exited %d, want %d", tc.name, code, tc.want)
			}
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, "cla-"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally (the pre-#4557 alias call segfaults)", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s self-host IR exited %d, want %d (139 = raw env-box call, #4557)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostClosureAliasWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping closure-alias wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range closureAliasIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s wasm IR exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostClosureAliasIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range closureAliasIRCases {
		if tc.name != "bare-alias-call" && tc.name != "param-alias" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "cla-"+tc.name+"-arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s arm64 IR exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
