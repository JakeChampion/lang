package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var arrarrIdentRowCases = []struct {
	name string
	src  string
	want int
	wasm bool
}{
	{"ident-row-with-literal-row", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    if (k == 1) { return "one"; }
    return "many";
}
function nested(k: i32): i32 {
    let inner: string[] = ["alpha-payload-" + tag(k), "beta-payload-" + tag(k)];
    let outer: string[][] = [inner, ["gamma-payload-" + tag(k)]];
    return outer[0][0].len() + outer[1][0].len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = (acc + nested(i % 3)) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = (acc + nested(j % 3)) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	{"ident-row-only", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function nested(k: i32): i32 {
    let inner: string[] = ["alpha-payload-" + tag(k), "beta-payload-" + tag(k)];
    let outer: string[][] = [inner];
    return outer[0][0].len() + outer[0][1].len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = (acc + nested(i % 2)) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = (acc + nested(j % 2)) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	{"producer-returns-ident-row", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function mk(k: i32): string[][] {
    let inner: string[] = ["alpha-payload-" + tag(k), "beta-payload-" + tag(k)];
    return [inner, ["gamma-payload-" + tag(k)]];
}
function consume(k: i32): i32 {
    let g: string[][] = mk(k);
    return g[0][0].len() + g[1][0].len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = (acc + consume(i % 2)) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = (acc + consume(j % 2)) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	{"row-read-after-is-refused", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function nested(k: i32): i32 {
    let inner: string[] = ["alpha-payload-" + tag(k)];
    let outer: string[][] = [inner];
    let a: i32 = outer[0][0].len();
    let b: i32 = inner[0].len();
    if (a != b) { return 0 - 1; }
    return a + b;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 400) {
        let v: i32 = nested(i % 2);
        if (v < 0) { return 97; }
        acc = (acc + v) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	{"row-element-aliases-live-local", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function nested(k: i32): i32 {
    let s: string = "shared-payload-" + tag(k);
    let inner: string[] = [s];
    let outer: string[][] = [inner];
    let a: i32 = outer[0][0].len();
    let b: i32 = s.len();
    if (a != b) { return 0 - 1; }
    return a + b;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 400) {
        let v: i32 = nested(i % 2);
        if (v < 0) { return 97; }
        acc = (acc + v) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	{"row-placed-twice", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function nested(k: i32): i32 {
    let inner: string[] = ["twice-payload-" + tag(k)];
    let o1: string[][] = [inner];
    let o2: string[][] = [inner];
    let a: i32 = o1[0][0].len();
    let b: i32 = o2[0][0].len();
    if (a != b) { return 0 - 1; }
    return a + b;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 400) {
        let v: i32 = nested(i % 2);
        if (v < 0) { return 97; }
        acc = (acc + v) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	{"row-mutated-between-decl-and-use", `function tag(k: i32): string {
    if (k == 0) { return "zero"; }
    return "many";
}
function nested(k: i32): i32 {
    let inner: string[] = ["first-payload-" + tag(k)];
    inner = inner.append("second-payload-" + tag(k));
    let outer: string[][] = [inner];
    return outer[0][0].len() + outer[0][1].len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 400) {
        let v: i32 = nested(i % 2);
        if (v <= 0) { return 97; }
        acc = (acc + v) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},
}

func TestSelfHostArrArrIdentRowReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrarrIdentRowCases {
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
				t.Errorf("%s = %d, want %d (98 = element strings leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostArrArrIdentRowReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrarrIdentRowCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = element strings leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostArrArrIdentRowReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping arr-of-arr ident-row reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range arrarrIdentRowCases {
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
				t.Errorf("%s = %d, want %d (98 = element strings leaked; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
