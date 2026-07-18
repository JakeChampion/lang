package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostClofldDropIRX86_64 pins the closure struct-FIELD shallow drop
// (the clofld slice): a struct whose fn-typed fields are admitted by the
// clofld scan — fresh-lambda-only construction values (each lifts to a
// sole-owned rc=1 `__mkclo$` env box), no base-including construction of the
// type, and no read of the field outside a direct `h.f(x)` call — routes
// through struct_routes_field_reclaim, and the k_clo arm of
// `__struct_drop_<T>` / the fr_clo arm of `__field_reclaim_<T>` shallow-free
// the field's env box (rc-guarded __fern_arr_dec; captures leak — the
// k_struct one-level model). The moves_fields NODEEP walk exempts calls
// through the local's own fn fields (`h.f(10)` moves nothing), so a called
// fn-field struct stays deep-drop-worthy.
//
// The loop-REINIT release path also routes: a loop-nested `var h: H = …`
// re-bind reclaims the prior iteration's env box through __field_reclaim_<T>'s
// fr_clo arm (the NODEEP fn-field exemption resolves the local's type with the
// nesting-aware fresh_struct_lit_type_deep, so a loop/if-nested declaration is
// no longer wrongly NODEEP'd into the box-only shallow dec). The churn case
// asserts the reclaim call and proves it stays BALANCED at scale.
func TestSelfHostClofldDropIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int, wantAsmSubstr string) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		if wantAsmSubstr != "" && !strings.Contains(string(asm), wantAsmSubstr) {
			t.Fatalf("%s: emitted asm missing %q — the fn-field struct was not admitted / the k_clo drop not emitted", name, wantAsmSubstr)
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
	// calls __struct_drop_H, whose k_clo arm frees the env box. Value 13.
	run(t, `struct H { f: (i32) => i32, id: i32 } function main(): i32 { var h: H = H { f: function (x: i32): i32 { return x + 3; }, id: 1 }; var r: i32 = h.f(10); return r; }`,
		"clofld-drop-fires", 13, "call __fn___struct_drop_H")

	// CHURN balance + loop-REINIT reclaim: the c1 shape — a capturing lambda
	// field built and called per iteration, 2M cycles. The loop-nested
	// re-bind routes through __field_reclaim_H (fr_clo frees the prior
	// iteration's env box); values right and the underflow detector clean
	// prove the admission + drops never over-release.
	run(t, `struct H { f: (i32) => i32, id: i32 } function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var k: i32 = i % 5; var h: H = H { f: function (x: i32): i32 { return x + k; }, id: i }; if (h.f(10) != 10 + k) { bad = 1; } i = i + 1; } return bad; } function main(): i32 { var v: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"clofld-capture-churn-balanced", 0, "call __fn___field_reclaim_H")

	// NON-admitted: a bare closure IDENT as the field value (an alias of a
	// live closure local) — the clofld store gate marks the field unsafe and
	// the type keeps the sound leak; g stays callable after h's drop.
	run(t, `struct H { f: (i32) => i32, id: i32 } function main(): i32 { var g = function (x: i32): i32 { return x * 2; }; var h: H = H { f: g, id: 3 }; var r: i32 = h.f(5) + g(2) + h.id; if (r != 17) { return 90; } if (__rc_underflow() != 0) { return 99; } return 0; }`,
		"clofld-ident-excluded", 0, "")

	// NON-admitted: a BASE COPY carries the field's box into a second struct
	// (two drops of one box if admitted) — the scan excludes the type; both
	// structs' fields stay callable.
	run(t, `struct H { f: (i32) => i32, id: i32 } function main(): i32 { var h1: H = H { f: function (x: i32): i32 { return x + 1; }, id: 3 }; var h2: H = H { ...h1, id: 4 }; var r: i32 = h1.f(5) + h2.f(10) + h2.id; if (r != 21) { return 90; } if (__rc_underflow() != 0) { return 99; } return 0; }`,
		"clofld-base-copy-excluded", 0, "")
}
