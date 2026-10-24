package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sliceBuiltinPrelude = `import "std/i32";
import "std/i64";
import "std/string";
` + strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
`

func sliceBuiltinHeap(body string) string {
	return sliceBuiltinPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
` + body + `
}
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`
}

var strSliceBuiltinRecvCases = []struct {
	name string
	src  string
	want int
}{
	{"str-slice-recv-builtin-upper-flat", sliceBuiltinHeap(`    return slice_unchecked(base, 4, base.len()).to_ascii_upper().len();`), 0},
	{"str-slice-recv-builtin-trim-refused", sliceBuiltinPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let t: str = slice_unchecked(base, 4, base.len()).trim();
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (has_sub(t, "XXXX")) { return 0 - 1; }
    if (!has_prefix(t, "efgh-a-wide")) { return 0 - 2; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 3; }
    return t.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 3000) { let r: i32 = round(pre); if (r != 102) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	{"str-slice-recv-builtin-split-refused", sliceBuiltinPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let parts: string[] = slice_unchecked(base, 4, base.len()).split("-");
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (parts.len() < 2) { return 0 - 1; }
    if (parts[0] != "efgh") { return 0 - 2; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 3; }
    return parts.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 18) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	{"str-slice-recv-builtin-source-live", sliceBuiltinPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let up: string = slice_unchecked(base, 4, base.len()).to_ascii_upper();
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("ZZZZZZZZ");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (has_sub(up, "XXXX")) { return 0 - 2; }
    if (!has_prefix(up, "EFGH-A-WIDE")) { return 0 - 3; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 4; }
    return base.len() + up.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 3000) { let r: i32 = round(pre); if (r != 208) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
}

func TestSelfHostStrSliceBuiltinRecvIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strSliceBuiltinRecvCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the view box was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrSliceBuiltinRecvIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strSliceBuiltinRecvCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the view box was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrSliceBuiltinRecvWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping slice builtin-receiver wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strSliceBuiltinRecvCases {
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
			watFile := filepath.Join(dir, strings.ReplaceAll(tc.name, "/", "_")+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (98 = the view box was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
