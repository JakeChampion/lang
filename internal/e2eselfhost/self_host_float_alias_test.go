package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostFloatAliasIsF64 pins `float` as the f64 alias, not f32.
//
// irlower tagged a `float`-annotated local (and param, and method receiver) as
// f32 alongside a real `f32` annotation. f32 is stored as an 8-byte f64 and its
// arithmetic rounds through an f32_bits/f32_from_bits demote→promote pair, so
// the tag did not merely pick the "f32.<m>" method twin — it made the VALUE
// compute at f32 precision. It also propagated: an f64-declared local
// initialised from a `float` one inherited the tag (#5882).
//
// The check compares raw IEEE-754 bit patterns via f64_bits rather than
// rendered text, so it fails on the arithmetic itself and cannot be masked by
// whichever `.to_string()` overload dispatch happens to select:
//
//	1.0f64 / 3.0 = 0x3FD5555555555555 = 4599676419421066581
//	1.0f32 / 3.0 = 0x3FD5555560000000 = 4599676419600023552  (mantissa truncated)
//
// `float` must produce the first. The f32 rows are the other half of the
// contract: this must not be "fixed" by making f32 arithmetic f64 — a genuine
// f32, by annotation or by an `as f32` cast, still has to round.
//
// Driver is the stdin asm_run (no imports): f64_bits and i64 `.to_string()` are
// both builtins, so the whole contract is expressible without std/float — which
// also keeps this test independent of the f64 `.to_string()` method dispatch
// that #5885 moved onto std/float.
func TestSelfHostFloatAliasIsF64(t *testing.T) {
	const (
		f64Bits = "4599676419421066581"
		f32Bits = "4599676419600023552"
	)

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
		want string
	}{
		{
			"float-local",
			`function main(): i32 { var a: float = 1.0; print(f64_bits(a / 3.0).to_string()); return 0; }`,
			f64Bits,
		},
		{
			"float-param",
			`function f(x: float): i64 { return f64_bits(x / 3.0); }
			 function main(): i32 { print(f(1.0).to_string()); return 0; }`,
			f64Bits,
		},
		{
			// The propagation case: `d` is declared f64 and must stay f64 even
			// though its initialiser is a `float` local.
			"f64-local-from-float-local",
			`function main(): i32 { var c: float = 1.0; var d: f64 = c; print(f64_bits(d / 3.0).to_string()); return 0; }`,
			f64Bits,
		},
		{
			"f32-local-still-rounds",
			`function main(): i32 { var a: f32 = 1.0; print(f64_bits(a / 3.0).to_string()); return 0; }`,
			f32Bits,
		},
		{
			"f32-param-still-rounds",
			`function g(x: f32): i64 { return f64_bits(x / 3.0); }
			 function main(): i32 { print(g(1.0).to_string()); return 0; }`,
			f32Bits,
		},
		{
			"as-f32-cast-still-rounds",
			`function main(): i32 { var a: f64 = 1.0; var b = a as f32; print(f64_bits(b / 3.0).to_string()); return 0; }`,
			f32Bits,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.prog))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "floatalias_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			// print appends a newline.
			out, _ := cmd.Output()
			if strings.TrimRight(string(out), "\n") != tc.want {
				kind := "want f64 precision, got f32-rounded"
				if tc.want == f32Bits {
					kind = "want f32 rounding, got f64 precision"
				}
				t.Errorf("f64_bits = %q, want %q (%s)", string(out), tc.want, kind)
			}
		})
	}
}
