package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostFieldReclaimIRX86_64(t *testing.T) {
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

	run(t, `struct B { ops: i32[], n: i32 }
function (b: B) emit(v: i32): B { return B { ops: b.ops.append(v), n: b.n + 1 }; }
function (b: B) clear(): B { return B { ops: [], n: 0 }; }
function churn(): i32 {
    let b: B = B { ops: [], n: 0 };
    let f: i32 = 0;
    while (f < 200000000) {
        b = b.emit(f);
        if (b.n >= 100) { b = b.clear(); }
        f = f + 1;
    }
    return b.n - b.n;
}
function main(): i32 { return churn(); }`, "field_reclaim_churn", 0)

	run(t, `struct B { ops: i32[], n: i32 }
function (b: B) emit(v: i32): B { return B { ops: b.ops.append(v), n: b.n + 1 }; }
function (b: B) clear(): B { return B { ops: [], n: 0 }; }
function thread(b: B): i32 {
    let f: i32 = 0;
    while (f < 100) { b = b.emit(f); if (b.n >= 30) { b = b.clear(); } f = f + 1; }
    let r: i32 = b.n;
    b = b.clear();
    return r - r;
}
function main(): i32 {
    let g: i32 = 0;
    while (g < 3000000) {
        let seed: B = B { ops: [], n: 0 };
        g = g + 1 + thread(seed);
    }
    return 0;
}`, "field_reclaim_snap_churn", 0)

	run(t, `struct B { ops: i32[], n: i32 }
function (b: B) emit(v: i32): B { return B { ops: b.ops.append(v), n: b.n + 1 }; }
function build(b: B): B { b = b.emit(10); b = b.emit(20); b = b.emit(30); return b; }
function main(): i32 {
    let b: B = B { ops: [], n: 0 };
    b = build(b);
    let sum: i32 = 0; let j: i32 = 0;
    while (j < b.ops.len()) { sum = sum + b.ops[j]; j = j + 1; }
    return sum + b.n;
}`, "field_reclaim_value", 63)

	run(t, `struct B { ops: i32[], n: i32 }
function (b: B) emit(v: i32): B { return B { ops: b.ops.append(v), n: b.n + 1 }; }
function thread(b: B): i32 { b = b.emit(1); b = b.emit(2); return b.n; }
function main(): i32 {
    let seed: B = B { ops: [7], n: 0 };
    let t: i32 = thread(seed);
    let sum: i32 = 0; let j: i32 = 0;
    while (j < seed.ops.len()) { sum = sum + seed.ops[j]; j = j + 1; }
    return sum + t;
}`, "field_reclaim_caller_intact", 9)

	run(t, `struct B { ops: i32[], n: i32 }
function step(c: B): B { return B { ops: c.ops.append(c.n), n: c.n + 1 }; }
function main(): i32 {
    let f: i32 = 0;
    while (f < 2000000) {
        let c: B = B { ops: [], n: 0 };
        let k: i32 = 0;
        while (k < 100) { c = step(c); k = k + 1; }
        f = f + 1 + (c.n - c.n);
    }
    return 0;
}`, "field_reclaim_local_churn", 0)
}
