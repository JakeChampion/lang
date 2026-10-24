package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var consumedAppendCases = []struct {
	name string
	src  string
	want int
	wasm bool
}{
	{"copy-path-receiver-read-after", `function sink(xs: i32[]): i32 { let s: i32 = 0; let i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function work(n: i32): i32 {
    let full: i32[] = [n, n + 1];
    let a: i32 = sink(full.append(10));
    return a + sink(full) + full.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = (acc + work(i % 8)) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = (acc + work(j % 8)) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	{"inplace-path-frees-nothing", `function sink(xs: i32[]): i32 { let s: i32 = 0; let i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function work(n: i32): i32 {
    let roomy: i32[] = [];
    let i: i32 = 0;
    while (i < 3) { roomy = roomy.append(n + i); i = i + 1; }
    let a: i32 = sink(roomy.append(20));
    let b: i32 = sink(roomy);
    if (a <= b) { return 0 - 1; }
    return a + b + roomy.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let v: i32 = work(i % 8); if (v < 0) { return 97; } acc = (acc + v) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { let w: i32 = work(j % 8); if (w < 0) { return 97; } acc = (acc + w) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, 0, true},

	{"element-receiver", `function sink(xs: i32[]): i32 { let s: i32 = 0; let i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function work(n: i32): i32 {
    let xs: i32[][] = [[n], [n + 1]];
    let a: i32 = sink(xs[0].append(30));
    return a + sink(xs[0]) + sink(xs[1]) + xs[0].len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = (acc + work(i % 8)) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = (acc + work(j % 8)) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	{"param-receiver-in-loop", `function sink(xs: i32[]): i32 { let s: i32 = 0; let i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }
function walk(path: i32[]): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 8) { t = t + sink(path.append(i)); i = i + 1; } return t; }
function work(n: i32): i32 {
    let full: i32[] = [n, n + 1];
    return walk(full) + sink(full) + full.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = (acc + work(i % 8)) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = (acc + work(j % 8)) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	{"pointer-elem-receiver-read-after", `function sink(xs: string[]): i32 { let s: i32 = 0; let i: i32 = 0; while (i < xs.len()) { s = s + xs[i].len(); i = i + 1; } return s; }
function tag(k: i32): string { if (k == 0) { return "zero"; } return "many"; }
function work(n: i32): i32 {
    let ss: string[] = ["alpha-" + tag(n)];
    let a: i32 = sink(ss.append("beta-" + tag(n)));
    let b: i32 = sink(ss);
    if (a <= b) { return 0 - 1; }
    return a + b + ss.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let v: i32 = work(i % 2); if (v < 0) { return 97; } acc = (acc + v) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { let v: i32 = work(j % 2); if (v < 0) { return 97; } acc = (acc + v) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},
}

func TestSelfHostConsumedAppendReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range consumedAppendCases {
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
				t.Errorf("%s = %d, want %d (98 = append temps leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostConsumedAppendReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range consumedAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = append temps leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostConsumedAppendReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping consumed-append reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range consumedAppendCases {
		if !tc.wasm {
			continue
		}
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
				t.Errorf("%s = %d, want %d (98 = append temps leaked; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
