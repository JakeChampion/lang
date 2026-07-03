package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSALiftIRLower is the production-shaped end-to-end gate for the
// stack-IR -> SSA lift: it lowers a Fern SOURCE the way the real compiler does
// (AST -> ir.Op[] via irlower.lower_func_for, the asm_ir backend's input), then
// LIFTS that real IR to SSA, optimises, and emits via ssa_x86 / ssa_arm64. The
// emitted binary's exit code is checked against the native interpreter's result
// for the same source (the differential oracle). Where TestSelfHostSSALiftEmit
// drives hand-built ir.Op[], this drives ACTUAL irlower output — so it proves
// the lift consumes the real production IR, not synthetic ops, all the way to
// running native code on both backends.
//
// Coverage is the lift's current subset: integer control flow (straight-line,
// loops, if-merge, break, cross-function calls, recursion), string literals
// + length (const_str / str_len, which lower RC-free), i32 arrays
// (arr_make / arr_get / arr_set / arr_len), scalar-field structs
// (struct_make / struct_get, incl. nested), and tuples (tuple_make /
// tuple_get, incl. nested), with irlower's RC-helper calls stripped.
// Out-of-subset programs make the driver exit non-zero; only in-subset
// programs are listed here.
func TestSelfHostSSALiftIRLower(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	armgcc, qemu := arm64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "lexer.fern", "parser.fern", "astwalk.fern",
		"ir.fern", "ssa.fern", "ssa_x86.fern", "ssa_arm64.fern",
		"irlower.fern", "ssa_lift.fern", "ssa_lift_irlower_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, x86gcc, dir, "ssa_lift_irlower_run.fern", "ssa_lift_irlower_run")

	// emit feeds the source to the driver on stdin and returns the emitted asm.
	emit := func(t *testing.T, src string, args ...string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(bin, args...)
		} else {
			cmd = exec.Command(x86runner[0], append(append(x86runner[1:], bin), args...)...)
		}
		cmd.Stdin = strings.NewReader(src)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("emit driver failed for %q: %v", src, err)
		}
		return out
	}
	run := func(t *testing.T, asm []byte, gcc string, pie bool, mk func(string, ...string) *exec.Cmd, tag string) int {
		t.Helper()
		asmPath := filepath.Join(dir, "il_"+tag+".s")
		binPath := filepath.Join(dir, "il_"+tag)
		if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		gccArgs := []string{"-static", "-nostdlib"}
		if pie {
			gccArgs = append(gccArgs, "-no-pie")
		}
		gccArgs = append(gccArgs, asmPath, "-o", binPath)
		if out, err := exec.Command(gcc, gccArgs...).CombinedOutput(); err != nil {
			t.Fatalf("gcc failed: %v\n%s\n--- asm ---\n%s", err, out, asm)
		}
		cmd := mk(binPath)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("emitted program did not exit normally")
		}
		return cmd.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
	}{
		{"arith", `function main(): i32 { return (3 + 4) * 2 - 1; }`},
		{"loopsum", `function main(): i32 { var i = 1; var acc = 0; while (i <= 5) { acc = acc + i; i = i + 1; } return acc; }`},
		{"branch", `function main(): i32 { var x = 0; if (7 > 3) { x = 42; } return x; }`},
		{"breakloop", `function main(): i32 { var i = 0; while (i < 100) { if (i == 42) { break; } i = i + 1; } return i; }`},
		{"callsum", `function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(20, 22); }`},
		{"factrec", `function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }`},
		{"strlen", `function main(): i32 { var s: string = "hello"; return s.len(); }`},
		{"strlen2", `function main(): i32 { return ("abcd").len() + ("xy").len(); }`},
		{"strpick", `function main(): i32 { var s: string = "hi"; var t: string = "world"; if (s.len() < t.len()) { return t.len(); } return s.len(); }`},
		// Harder shapes over real irlower output: nested loops, mutual recursion
		// across two functions, bitwise / shift operators, nested if, and an
		// early return out of a loop.
		{"nestloop", `function main(): i32 { var t = 0; var i = 0; while (i < 4) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }`},
		{"mutualrec", `function isodd(n: i32): boolean { if (n == 0) { return false; } return iseven(n - 1); } function iseven(n: i32): boolean { if (n == 0) { return true; } return isodd(n - 1); } function main(): i32 { if (iseven(10)) { return 1; } return 0; }`},
		{"bitwise", `function main(): i32 { var a = 12; var b = 10; return (a & b) + (a | b) + (a ^ b); }`},
		{"shift", `function main(): i32 { return (1 << 5) + (64 >> 2); }`},
		{"nestif", `function main(): i32 { var x = 7; if (x > 5) { if (x > 10) { return 1; } return 2; } return 3; }`},
		{"earlyret", `function main(): i32 { var i = 0; while (i < 100) { if (i * i > 50) { return i; } i = i + 1; } return 99; }`},
		// i32 arrays over real irlower output (slice 2): literal + index, length,
		// element update via `.with`, a loop summing elements, and a borrowed
		// array param across a call. These lower with arr_make / arr_get /
		// arr_set / arr_len plus the RC-helper calls the lift strips.
		{"arrlit", `function main(): i32 { var a = [10, 20, 30]; return a[1]; }`},
		{"arrlen", `function main(): i32 { var a = [1, 2, 3, 4]; return a.len(); }`},
		{"arrwith", `function main(): i32 { var a = [1, 2, 3]; a = a.with(1, 99); return a[0] + a[1] + a[2]; }`},
		{"arrsum", `function main(): i32 { var a = [5, 10, 15]; var s = 0; var i = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`},
		{"arrpass", `function sum3(a: i32[]): i32 { return a[0] + a[1] + a[2]; } function main(): i32 { var xs = [7, 8, 9]; return sum3(xs); }`},
		// Scalar-field structs over real irlower output (slice 3): literal +
		// field read, a borrowed struct param across a call, a boolean field
		// driving a branch, a spread functional-update, and a nested struct
		// field (a pointer field, stored/read i32-wide in the low SSA heap).
		{"structlit", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 10, y: 32 }; return p.x + p.y; }`},
		{"structfn", `struct P { x: i32, y: i32 } function sx(p: P): i32 { return p.x; } function main(): i32 { var p = P { x: 5, y: 9 }; return sx(p) + p.y; }`},
		{"boolfield", `struct F { a: boolean, n: i32 } function main(): i32 { var f = F { a: true, n: 7 }; if (f.a) { return f.n; } return 0; }`},
		{"structupd", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 1, y: 2 }; p = P { ...p, x: 40 }; return p.x + p.y; }`},
		{"structnest", `struct Inner { v: i32 } struct Outer { inner: Inner, k: i32 } function main(): i32 { var o = Outer { inner: Inner { v: 30 }, k: 12 }; return o.inner.v + o.k; }`},
		// Tuples over real irlower output (slice 4): a pair + element reads, a
		// tuple returned from a function, a nested tuple (a pointer element),
		// and a boolean-element tuple driving a branch.
		{"tuplepair", `function main(): i32 { var t = (10, 32); return t.0 + t.1; }`},
		{"tuplefn", `function mk(): (i32, i32) { return (5, 9); } function main(): i32 { var t = mk(); return t.0 + t.1; }`},
		{"tuplenest", `function main(): i32 { var t = (1, (2, 3)); return t.0 + t.1.0 + t.1.1; }`},
		{"tuplebool", `function main(): i32 { var t = (true, 7); if (t.0) { return t.1; } return 0; }`},
	}
	for _, tc := range cases {
		tc := tc
		ref := runInterpExit(t, tc.src) // independent oracle: the interpreter
		t.Run("x86_64/"+tc.name, func(t *testing.T) {
			if len(x86runner) != 0 {
				t.Skip("emitted x86-64 runs natively; skipping under an exec runner")
			}
			mk := func(b string, a ...string) *exec.Cmd { return exec.Command(b, a...) }
			if got := run(t, emit(t, tc.src), x86gcc, true, mk, "x86-"+tc.name); got != ref {
				t.Errorf("x86-64 irlower->lift %s = %d, interp = %d", tc.name, got, ref)
			}
		})
		t.Run("arm64/"+tc.name, func(t *testing.T) {
			mk := func(b string, a ...string) *exec.Cmd { return runArm64Bin(qemu, b, a...) }
			if got := run(t, emit(t, tc.src, "-target", "arm64"), armgcc, false, mk, "arm-"+tc.name); got != ref {
				t.Errorf("arm64 irlower->lift %s = %d, interp = %d", tc.name, got, ref)
			}
		})
	}
}
