package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var nestedBodyPassBlindnessCases = []struct {
	name string
	src  string
}{
	{"fn-array-in-if", `function mk(): i32 { return 42; }
function mk2(): i32 { return 7; }

function main(): i32 {
    if (true) {
        let fns = [mk, mk2];
        if (fns[0]() != 42) { return 90; }
        if (fns[1]() != 7) { return 91; }
        return 0;
    }
    return 92;
}
`},
	{"fn-local-in-if", `function mk(): i32 { return 42; }

function main(): i32 {
    if (true) {
        let f = mk;
        if (f() != 42) { return 90; }
        return 0;
    }
    return 91;
}
`},
	{"fn-array-in-lambda", `function mk(): i32 { return 42; }
function mk2(): i32 { return 7; }

function main(): i32 {
    let g: () => i32 = (): i32 => {
        let fns = [mk, mk2];
        return fns[0]() + fns[1]();
    };
    if (g() != 49) { return 90; }
    return 0;
}
`},
	{"fn-local-in-while-if", `function mk(): i32 { return 42; }

function main(): i32 {
    let i: i32 = 0;
    while (i < 1) {
        if (true) {
            let f = mk;
            if (f() != 42) { return 90; }
            return 0;
        }
        i = i + 1;
    }
    return 91;
}
`},
	{"lambda-f64-param-literal", `function main(): i32 {
    let f: (f64) => i32 = (x: f64): i32 => {
        x = 3;
        if (x > 2.5) { return 0; }
        return 90;
    };
    return f(1.0);
}
`},
	{"lambda-f64-local-control", `function main(): i32 {
    let f: () => i32 = (): i32 => {
        let y: f64 = 3;
        if (y > 2.5) { return 0; }
        return 90;
    };
    return f();
}
`},
	{"lambda-captured-receiver", `struct Acc { v: f64 }

function (a: Acc) bump(d: f64): f64 { return a.v + d; }

function main(): i32 {
    let acc: Acc = Acc { v: 1.0 };
    let f: () => f64 = (): f64 => { return acc.bump(2); };
    let r: f64 = f();
    if (r > 2.5 && r < 3.5) { return 0; }
    return 90;
}
`},
	{"lambda-captured-receiver-nested", `struct Acc { v: f64 }

function (a: Acc) bump(d: f64): f64 { return a.v + d; }

function main(): i32 {
    let acc: Acc = Acc { v: 1.0 };
    let f: () => f64 = (): f64 => {
        let g: () => f64 = (): f64 => { return acc.bump(2); };
        return g();
    };
    let r: f64 = f();
    if (r > 2.5 && r < 3.5) { return 0; }
    return 90;
}
`},
	{"lambda-param-shadows-capture", `struct Acc { v: f64 }

function (a: Acc) bump(d: f64): f64 { return a.v + d; }

struct Ctr { n: i32 }

function (c: Ctr) bump(d: i32): i32 { return c.n + d; }

function run(f: (Ctr) => i32): i32 { return f(Ctr { n: 40 }); }

function main(): i32 {
    let acc: Acc = Acc { v: 1.0 };
    let r: i32 = run((acc: Ctr): i32 => { return acc.bump(2); });
    if (r != 42) { return 90; }
    let q: f64 = acc.bump(1);
    if (q > 1.5 && q < 2.5) { return 0; }
    return 91;
}
`},
	{"labeled-break-in-lambda", `function main(): i32 {
    let f: () => i32 = (): i32 => {
        let n: i32 = 0;
        outer: while (n < 100) {
            inner: while (true) {
                n = n + 1;
                break outer;
            }
            n = n + 100;
        }
        return n;
    };
    if (f() != 1) { return 90; }
    return 0;
}
`},
	{"fn-array-top-level-control", `function mk(): i32 { return 42; }
function mk2(): i32 { return 7; }

function main(): i32 {
    let fns = [mk, mk2];
    if (fns[0]() != 42) { return 90; }
    if (fns[1]() != 7) { return 91; }
    return 0;
}
`},
}

func TestSelfHostNestedBodyPassBlindnessIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range nestedBodyPassBlindnessCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "nbpb_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 (139 = the fn array was const-called; 90+ = a check failed)", tc.name, code)
			}
		})
	}
}

func TestSelfHostNestedBodyPassBlindnessIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range nestedBodyPassBlindnessCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "nbpb_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 (139 = the fn array was const-called; 90+ = a check failed)", tc.name, code)
			}
		})
	}
}

func TestSelfHostNestedBodyPassBlindnessIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-body-pass-blindness wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range nestedBodyPassBlindnessCases {
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
			watFile := filepath.Join(dir, "nestedbodyblind_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			out, runErr := run.CombinedOutput()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q: %v\n%s", tc.name, runErr, out)
			}
			if code := run.ProcessState.ExitCode(); code != 0 {
				t.Errorf("nested-body-pass-blindness wasm IR %q = %d, want 0\n%s", tc.name, code, out)
			}
		})
	}
}
