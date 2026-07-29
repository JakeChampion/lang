package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMethodWideReturn pins that an i64 returned from a method on a
// PRIMITIVE receiver keeps all 64 bits.
//
// irlower keys a method's return width in i64_ret_fns under "<Type>.<method>",
// and method_recv_tyname builds that key. It resolved a struct receiver (via
// expr_struct_type) and a u64/width-64 one, but returned "" for every other
// primitive — i32, string, f64, f32, u32, boolean. With no key, the wide return
// went untracked and the result was truncated to 32 bits:
//
//	function (n: f64) m(): i64 { return f64_bits(n / 3.0); }
//	a.m()   ->  0x00000000_55555555   (want 0x3FD5555555555555)
//
// The same body as a FREE function was correct, and a struct receiver was
// correct, which is what kept this hidden.
//
// Each case compares the method's result against the identical computation in a
// free function, so it asserts agreement rather than a hard-coded constant —
// the truncation is what differs, and a both-paths-wrong regression would still
// be caught by the native oracle rows in the differential suites.
func TestSelfHostMethodWideReturn(t *testing.T) {
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

	for _, tc := range []struct {
		name string
		prog string
	}{
		{
			"f64-receiver",
			`function (n: f64) m(): i64 { return f64_bits(n / 3.0); }
			 function big(n: f64): i64 { return f64_bits(n / 3.0); }
			 function main(): i32 { var a: f64 = 1.0; if (a.m() == big(a)) { return 7; } return 9; }`,
		},
		{
			"i32-receiver",
			`function (n: i32) m(): i64 { return f64_bits(1.0 / 3.0); }
			 function big(): i64 { return f64_bits(1.0 / 3.0); }
			 function main(): i32 { var a: i32 = 1; if (a.m() == big()) { return 7; } return 9; }`,
		},
		{
			"string-receiver",
			`function (s: string) m(): i64 { return f64_bits(1.0 / 3.0); }
			 function big(): i64 { return f64_bits(1.0 / 3.0); }
			 function main(): i32 { var s: string = "x"; if (s.m() == big()) { return 7; } return 9; }`,
		},
		{
			// The receiver kind that already worked — it resolves through
			// expr_struct_type, so it guards against a fix that regressed the
			// struct path while widening the primitive one.
			"struct-receiver",
			`struct P { x: i32 }
			 function (p: P) m(): i64 { return f64_bits(1.0 / 3.0); }
			 function big(): i64 { return f64_bits(1.0 / 3.0); }
			 function main(): i32 { var p: P = P { x: 1 }; if (p.m() == big()) { return 7; } return 9; }`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.prog))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "widret_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 7 {
				t.Errorf("exit = %d, want 7 (9 = method's i64 return truncated to 32 bits)", code)
			}
		})
	}
}
