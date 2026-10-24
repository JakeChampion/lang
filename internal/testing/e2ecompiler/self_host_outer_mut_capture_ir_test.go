package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var outerMutCaptureIRCases = []struct {
	name string
	src  string
	want int
}{
	{"array-reassign",
		`function main(): i32 {
    let a: i32[] = [10, 1];
    let f: () => i32 = (): i32 => { return a[0]; };
    a = [42, 1];
    return f();
}`, 42},
	{"string-reassign",
		`function main(): i32 {
    let s: string = "aa";
    let f: () => i32 = (): i32 => { return s.len(); };
    s = "abcdef";
    return f() + 36;
}`, 42},
	{"struct-reassign",
		`struct B { v: i32 }
function main(): i32 {
    let b: B = B { v: 10 };
    let f: () => i32 = (): i32 => { return b.v; };
    b = B { v: 42 };
    return f();
}`, 42},
	{"i32-outer-reassign",
		`function main(): i32 {
    let n: i32 = 10;
    let f: () => i32 = (): i32 => { return n; };
    n = 42;
    return f();
}`, 42},
	{"outer-mutate-then-capture",
		`function main(): i32 {
    let total: i32 = 0;
    total = total + 15;
    let f: () => i32 = (): i32 => { return total + 27; };
    return f();
}`, 42},
	{"loop-accumulator",
		`function main(): i32 {
    let s: i32 = 0;
    let i: i32 = 0;
    let add: () => i32 = (): i32 => { s = s + i; return 0; };
    while (i < 4) {
        i = i + 1;
        let r: i32 = add();
    }
    return s + 32;
}`, 42},
	{"outer-and-inner-write",
		`function main(): i32 {
    let x: i32 = 0;
    let f: () => i32 = (): i32 => { x = x + 4; return 0; };
    x = 3;
    let r: i32 = f();
    return x + 35;
}`, 42},
	{"with-selfreassign-then-capture",
		`function main(): i32 {
    let a: i32[] = [0, 1];
    a = a.with(0, 42);
    let f: () => i32 = (): i32 => { return a[0]; };
    return f();
}`, 42},
	{"alias-reassign-live",
		`function main(): i32 {
    let keep: i32[] = [40, 7];
    let a: i32[] = [10, 1];
    let f: () => i32 = (): i32 => { return a[0]; };
    a = keep;
    a = [1, 2];
    a = keep;
    let x: i32 = f();
    let y: i32 = keep[0];
    return x + y - 38;
}`, 42},
	{"loop-string-grow",
		`function main(): i32 {
    let s: string = "x";
    let f: () => i32 = (): i32 => { return s.len(); };
    let i: i32 = 0;
    while (i < 40) {
        s = s + "y";
        i = i + 1;
    }
    return f() + 1;
}`, 42},
	{"escape-loop-body-array",
		`function apply(x: i32, f: (i32) => i32): i32 { return f(x); }
function main(): i32 {
    let total: i32 = 0;
    let ks: i32[] = [20, 20];
    for k in ks {
        let seen: string[] = [];
        seen = seen.append("a");
        function rw(x: i32): i32 { return x + seen.len(); }
        total = total + apply(k, rw);
    }
    return total;
}`, 42},
	{"escape-loop-body-array-inner-loop",
		`struct P { name: string }
struct F { ps: P[] }
function apply(x: i32, f: (i32) => i32): i32 { return f(x); }
function main(): i32 {
    let fs: F[] = [F { ps: [P { name: "a" }, P { name: "b" }] }, F { ps: [] }];
    let total: i32 = 38;
    for fd in fs {
        let names: string[] = [];
        for pd in fd.ps { names = names.append(pd.name); }
        function rw(x: i32): i32 { return x + names.len(); }
        total = total + apply(1, rw);
    }
    return total;
}`, 42},
	{"escape-array-reassign",
		`function mk(): () => i32 {
    let a: i32[] = [10, 1];
    let f: () => i32 = (): i32 => { return a[0]; };
    a = [42, 1];
    return f;
}
function main(): i32 {
    let g: () => i32 = mk();
    return g();
}`, 42},
	{"escape-string-reassign",
		`function mk(): () => i32 {
    let s: string = "aa";
    let f: () => i32 = (): i32 => { return s.len(); };
    s = "abcdef";
    return f;
}
function main(): i32 {
    let g: () => i32 = mk();
    return g() + 36;
}`, 42},
	{"escape-struct-reassign",
		`struct B { v: i32 }
function mk(): () => i32 {
    let b: B = B { v: 10 };
    let f: () => i32 = (): i32 => { return b.v; };
    b = B { v: 42 };
    return f;
}
function main(): i32 {
    let g: () => i32 = mk();
    return g();
}`, 42},
	{"escape-i32-reassign",
		`function mk(): () => i32 {
    let n: i32 = 10;
    let f: () => i32 = (): i32 => { return n; };
    n = 42;
    return f;
}
function main(): i32 {
    let g: () => i32 = mk();
    return g();
}`, 42},
	{"escape-mixed-write",
		`function mk(): () => i32 {
    let n: i32 = 0;
    let f: () => i32 = (): i32 => { n = n + 2; return n; };
    n = 38;
    return f;
}
function main(): i32 {
    let g: () => i32 = mk();
    let r1: i32 = g();
    return g();
}`, 42},
	{"escape-container-reassign",
		`function main(): i32 {
    let a: i32[] = [10, 1];
    let f: () => i32 = (): i32 => { return a[0]; };
    let fs: (() => i32)[] = [f];
    a = [42, 1];
    return fs[0]();
}`, 42},
	{"escape-two-closures-shared-capture",
		`function main(): i32 {
    let a: i32[] = [10, 1];
    let direct: () => i32 = (): i32 => { return a[0]; };
    let esc: () => i32 = (): i32 => { return a[0] + 1; };
    let keep: (() => i32)[] = [esc];
    a = [20, 1];
    return direct() + keep[0]();
}`, 41},
}

func TestSelfHostOuterMutCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range outerMutCaptureIRCases {
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
			bin := buildBin(t, gcc, dir, "omc-"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s self-host IR exited %d, want %d (10/38 = stale by-value snapshot, #5301)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostOuterMutCaptureWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping outer-mut-capture wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range outerMutCaptureIRCases {
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

func TestSelfHostOuterMutCaptureIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range outerMutCaptureIRCases {
		switch tc.name {
		case "array-reassign", "string-reassign", "struct-reassign", "outer-and-inner-write",
			"escape-array-reassign", "escape-mixed-write":
		default:
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "omc-"+tc.name+"-arm64", string(asm))
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
