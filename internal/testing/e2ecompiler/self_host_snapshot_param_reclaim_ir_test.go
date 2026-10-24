package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostSnapshotParamReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	run := func(t *testing.T, prog string, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		if strings.Contains(string(asm), "__fern_snapshot_dec") {
			t.Errorf("%s: retired snapshot runtime is still emitted", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	run(t, `struct C { a: i32, n: i32 }
function (c: C) step(v: i32): C { return C { a: c.a + v, n: c.n + 1 }; }
function heavy(c: C): i32 {
    let k: i32 = 0;
    while (k < 200) { c = c.step(k); k = k + 1; }
    return c.n;
}
function main(): i32 {
    let f: i32 = 0;
    while (f < 3000000) {
        let seed: C = C { a: 0, n: 0 };
        let r: i32 = heavy(seed);
        f = f + 1 + (r - r);
    }
    return 0;
}`, "snap_param_churn", 0)

	run(t, `struct C { a: i32, n: i32 }
function (c: C) step(v: i32): C { return C { a: c.a + v, n: c.n + 1 }; }
@noinline function thread(c: C): i32 { c = c.step(10); c = c.step(20); return c.a; }
function main(): i32 {
    let seed: C = C { a: 5, n: 0 };
    let t: i32 = thread(seed);
    return seed.a + t;
}`, "snap_param_caller_intact", 40)

	run(t, `struct E { buf: i32[], n: i32 }
function (e: E) emit(v: i32): E { return E { buf: e.buf.append(v), n: e.n + 1 }; }
function build(e: E): E { e = e.emit(1); e = e.emit(2); e = e.emit(3); return e; }
function main(): i32 {
    let e: E = E { buf: [], n: 0 };
    e = build(e);
    let sum: i32 = 0; let j: i32 = 0;
    while (j < e.buf.len()) { sum = sum + e.buf[j]; j = j + 1; }
    return sum + e.n;
}`, "snap_param_heapfield", 9)
}

func TestSelfHostSnapshotParamReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		if strings.Contains(string(asm), "__fern_snapshot_dec") {
			t.Errorf("%s: retired snapshot runtime is still emitted", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	run(t, `struct C { a: i32, n: i32 }
function (c: C) step(v: i32): C { return C { a: c.a + v, n: c.n + 1 }; }
function thread(c: C): i32 { c = c.step(10); c = c.step(20); return c.a; }
function main(): i32 {
    let seed: C = C { a: 5, n: 0 };
    let t: i32 = thread(seed);
    return seed.a + t;
}`, "snap_param_caller_intact_arm64", 40)

	run(t, `struct C { a: i32, n: i32 }
function (c: C) step(v: i32): C { return C { a: c.a + v, n: c.n + 1 }; }
@noinline function build(c: C): i32 {
    let k: i32 = 0;
    while (k < 50) { c = c.step(k); k = k + 1; }
    return c.a;
}
function main(): i32 {
    let seed: C = C { a: 0, n: 0 };
    return build(seed) - 1218;
}`, "snap_param_scalar_churn_arm64", 7)
}

func TestSelfHostSnapshotParamReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm snapshot-param reclaim e2e")
	}
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	run := func(t *testing.T, prog, name, rebinder string, want int) {
		t.Helper()
		wat := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(wat) == 0 {
			t.Fatalf("%s: wasm emitter produced 0 bytes", name)
		}
		ws := string(wat)
		at := strings.Index(ws, "(func $"+rebinder+" ")
		if at < 0 {
			t.Fatalf("%s: no $%s in the WAT", name, rebinder)
		}
		body, _, _ := strings.Cut(ws[at:], "\n  (func ")
		if !strings.Contains(body, "call $__fern_arr_dec") {
			t.Errorf("%s: $%s releases no intermediate box\n%s", name, rebinder, body)
		}
		watPath := filepath.Join(dir, name+".wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
		_, _ = cmd.Output()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d — reclaim corrupted the caller's box?\n--- WAT ---\n%s", name, code, want, ws)
		}
	}

	run(t, `struct C { a: i32, n: i32 }
function (c: C) step(v: i32): C { return C { a: c.a + v, n: c.n + 1 }; }
function thread(c: C): i32 { c = c.step(10); c = c.step(20); return c.a; }
function main(): i32 {
    let seed: C = C { a: 5, n: 0 };
    let t: i32 = thread(seed);
    return seed.a + t;
}`, "snap_param_caller_intact_wasm", "thread", 40)

	run(t, `struct C { a: i32, n: i32 }
function (c: C) step(v: i32): C { return C { a: c.a + v, n: c.n + 1 }; }
function build(c: C): i32 {
    let k: i32 = 0;
    while (k < 50) { c = c.step(k); k = k + 1; }
    return c.a;
}
function main(): i32 {
    let seed: C = C { a: 0, n: 0 };
    return build(seed) - 1218;
}`, "snap_param_scalar_churn_wasm", "build", 7)
}
