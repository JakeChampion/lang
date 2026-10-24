package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var strArrFieldTransientReadCases = []struct {
	name string
	src  string
	want int
}{
	{"strarr-field-transient-elem-read-flat", `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let o: string[] = []; let i: i32 = 0; while (i < 3) { o = o.append(w(pre)); i = i + 1; } return o; }
function node(name: string, deps: string[], mtime: i32): Node { return Node { name: name, deps: deps, mtime: mtime }; }
function round(pre: string, n: i32): i32 { let f: Node = node(w(pre), mk(pre), n); return f.deps.len() + f.name.len() + f.deps[0].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre, i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let a: i32 = churn(2000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(2000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, 0},
	{"strarr-field-view-elem-read-refused", `struct View { title: string, lines: string[] }
function w(pre: string): string { return pre + "  a-wide-element-past-the-inline-threshold  "; }
function mk(pre: string): string[] { let o: string[] = []; let i: i32 = 0; while (i < 3) { o = o.append(w(pre)); i = i + 1; } return o; }
function doc(title: string, lines: string[]): View { return View { title: title, lines: lines }; }
function expect(pre: string): i32 { let r: string[] = mk(pre); return r[0].trim().len() + w(pre).len(); }
function round(pre: string): i32 { let d: View = doc(w(pre), mk(pre)); let v: str = d.lines[0].trim(); let pad: string[] = mk(pre); if (pad.len() < 0) { return 0; } return v.len() + d.title.len(); }
function main(): i32 { let pre: string = "ab"; let e: i32 = expect(pre); let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != e) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	{"strarr-field-bound-elem-read-refused", `struct Held { title: string, lines: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let o: string[] = []; let i: i32 = 0; while (i < 3) { o = o.append(w(pre)); i = i + 1; } return o; }
function doc(title: string, lines: string[]): Held { return Held { title: title, lines: lines }; }
function hold(pre: string): i32 { let d: Held = doc(w(pre), mk(pre)); let t: string = d.lines[0]; return t.len() + d.title.len(); }
function round(pre: string): i32 { let k: i32 = hold(pre); let pad: string[] = mk(pre); let pad2: string[] = mk(pre); if (pad.len() < 0 || pad2.len() < 0) { return 0; } return k; }
function main(): i32 { let pre: string = "ab"; let e: i32 = w("ab").len() * 2; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != e) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
}

func TestSelfHostStrArrFieldTransientReadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldTransientReadCases {
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
				t.Errorf("%s = %d, want %d (98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrArrFieldTransientReadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldTransientReadCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrArrFieldTransientReadWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping string[]-field transient-read wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strArrFieldTransientReadCases {
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
				t.Errorf("%s = %d, want %d (98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
