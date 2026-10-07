package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostClofldDropIRX86_64 pins the release of a closure held in a
// struct FIELD: a fresh lambda stored into a fn-typed field is released with
// the struct, and a loop-nested `let h: H = …` re-bind releases the prior
// iteration's env box. Calling through the local's own fn field (`h.f(10)`)
// moves nothing, so the struct still owns the box.
//
// The churn case proves the release stays BALANCED at scale (values right, the
// underflow counter clean). The excluded cases — a bare closure ident aliased
// into the field, and a base copy carrying the field's box into a second
// struct — must keep every owner callable, so an over-release shows up as a
// wrong value or exit 99.
func TestSelfHostClofldDropIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
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
			t.Errorf("%s exited %d, want %d (99 = over-release)", name, code, want)
		}
	}

	// DROP FIRES: an admitted straight-line fn-field struct — the exit sweep
	// releases the env box with the struct. Value 13.
	run(t, `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let h: H = H { f: (x: i32): i32 => { return x + 3; }, id: 1 }; let r: i32 = h.f(10); return r; }`,
		"clofld-drop-fires", 13)

	// CHURN balance + loop-REINIT reclaim: the c1 shape — a capturing lambda
	// field built and called per iteration, 2M cycles. The loop-nested re-bind
	// releases the prior iteration's env box; values right and the underflow
	// detector clean prove the release never over-releases.
	run(t, `struct H { f: (i32) => i32, id: i32 } function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let k: i32 = i % 5; let h: H = H { f: (x: i32): i32 => { return x + k; }, id: i }; if (h.f(10) != 10 + k) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"clofld-capture-churn-balanced", 0)

	// NON-admitted: a bare closure IDENT as the field value (an alias of a
	// live closure local) — the clofld store gate marks the field unsafe and
	// the type keeps the sound leak; g stays callable after h's drop.
	run(t, `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let g = (x: i32): i32 => { return x * 2; }; let h: H = H { f: g, id: 3 }; let r: i32 = h.f(5) + g(2) + h.id; if (r != 17) { return 90; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`,
		"clofld-ident-excluded", 0)

	// NON-admitted: a BASE COPY carries the field's box into a second struct
	// (two drops of one box if admitted) — the scan excludes the type; both
	// structs' fields stay callable.
	run(t, `struct H { f: (i32) => i32, id: i32 } function main(): i32 { let h1: H = H { f: (x: i32): i32 => { return x + 1; }, id: 3 }; let h2: H = H { ...h1, id: 4 }; let r: i32 = h1.f(5) + h2.f(10) + h2.id; if (r != 21) { return 90; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`,
		"clofld-base-copy-excluded", 0)
}
