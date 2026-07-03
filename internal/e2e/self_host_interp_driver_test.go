package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// interpDriver bundles lexer + parser + interp + a stdin driver (reads
// source via read_all_stdin, evaluates it with interp.eval_module, and
// exits with the program's VInt / VBool result). It's the input the
// self-hosted compiler compiles into a self-hosted INTERPRETER.
const interpDriverMod = "import \"./lexer\";\n" +
	"import \"./parser\";\n" +
	"import \"./interp\";\n" +
	"function main(): i32 {\n" +
	"    var src: string = read_all_stdin();\n" +
	"    var mod: parser.Module = parser.parse_module(lexer.tokenize(src));\n" +
	"    var result: interp.Value = interp.eval_module(mod);\n" +
	"    match (result) {\n" +
	"        interp.VInt(i) => { return i.v; },\n" +
	"        interp.VBool(b) => { if (b.v) { return 1; } return 0; },\n" +
	"        _ => { return 254; }\n" +
	"    }\n" +
	"    return 254;\n" +
	"}\n"

var interpProgs = []struct {
	name string
	src  string
	exit int
}{
	{"return-literal", "function main(): i32 { return 42; }", 42},
	{"arith", "function main(): i32 { return 6 * 7; }", 42},
	// Hex integer literals (#4341): eval_expr used the decimal-only
	// util.digits_to_i32, which stopped at the `x` and returned 0. Now it uses
	// the hex/binary-aware util.lit_to_i32. `0x1F` = 31; `0x10 + 1` = 17 (each
	// operand parsed independently, no fold in the interp).
	{"hex-literal", "function main(): i32 { return 0x1F; }", 31},
	{"hex-arith", "function main(): i32 { return 0x10 + 1; }", 17},
	// Scientific-notation float literals (#4342): str_to_f64 parsed integer +
	// fraction only and dropped the exponent, so `1e3` evaluated to 1.0. Now it
	// scales by 10**exp. Each check returns 7 iff the exponent is honoured.
	{"sci-float-exp", "function main(): i32 { if (1e3 == 1000.0) { return 7; } return 0; }", 7},
	{"sci-float-frac", "function main(): i32 { if (1.5e2 == 150.0) { return 7; } return 0; }", 7},
	{"sci-float-neg-exp", "function main(): i32 { var b: f64 = 1e-2; if (b > 0.009 && b < 0.011) { return 7; } return 0; }", 7},
	// Postfix chains on a bool literal (#4338): parse_primary's true/false
	// arms returned the bare bool ExprResult without threading it through
	// parse_postfix (unlike the number / string arms), so `true.to_i()` was
	// parsed as just `true` and the `.to_i() + 41` suffix was silently
	// dropped — the interp returned 1 (the bool) instead of 42. Now both
	// bool arms route through parse_postfix, so the method call + add apply.
	{"bool-literal-postfix", "function (b: boolean) to_i(): i32 { if (b) { return 1; } return 0; } function main(): i32 { return true.to_i() + 41; }", 42},
	{"locals", "function main(): i32 { var x: i32 = 10; var y: i32 = 32; return x + y; }", 42},
	{"if", "function main(): i32 { if (5 > 3) { return 1; } return 0; }", 1},
	{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(19, 23); }", 42},
	// Default parameter values — fill_default_args_module (run in
	// interp.eval_module) completes the omitted trailing argument.
	{"default-one", "function inc(n: i32, by: i32 = 1): i32 { return n + by; } function main(): i32 { return inc(41); }", 42},
	{"default-multi", "function box(w: i32, h: i32 = 2, d: i32 = 3): i32 { return w * 100 + h * 10 + d; } function main(): i32 { return box(1) - 81; }", 42},
	{"float", "function main(): i32 { var f: f64 = 3.5; var g: f64 = 2.5; if (f + g > 5.0) { return 7; } return 0; }", 7},
	// `as` numeric casts — the interp's unary evaluator previously errored
	// on every `as_<Type>` op ("unknown unary op"); now an integer-target
	// cast is identity on an int / truncates a float, and a float-target
	// cast widens an int / is identity on a float.
	{"cast-i64-to-i32", "function main(): i32 { var v: i64 = 9; return v as i32; }", 9},
	{"cast-f64-to-i32", "function main(): i32 { var f: f64 = 3.9; return f as i32; }", 3},
	{"cast-i32-to-f64", "function main(): i32 { var n: i32 = 5; var f: f64 = n as f64; return (f + 0.5) as i32; }", 5},
	{"cast-in-i64-array-sum", "function main(): i32 { var xs: i64[] = [3, 5, 90]; var s: i64 = 0; for v in xs { s = s + v; } return s as i32; }", 98},
	// Non-numeric `as <Type>` ascription (#2669) — `as_i32[]` is a zero-cost
	// identity on the value. eval_unary previously errored ("unknown unary op:
	// as_i32[]") on every non-numeric cast; it now passes the operand through
	// unchanged, matching the AST/IR emitters.
	{"asc-array-identity", "function main(): i32 { var a = [3, 4] as i32[]; return a[0] + a[1]; }", 7},
	// Range-for `for i in LOW..HIGH`: the parser emits a synthetic
	// __range(LOW, HIGH) for-iter that the IR path lowers (irlower) but the
	// interpreter doesn't understand — parser.desugar_ranges_module (run in
	// interp.eval_module) rewrites it to a counting while-loop so the interp
	// evaluates it. Without that, an undesugared __range iter mis-evaluates
	// (a 254 non-i32 result). Covers continue/break (the increment is at the
	// top of the desugared loop) and empty/reversed (zero iterations).
	{"range-sum", "function main(): i32 { var s = 0; for i in 0..5 { s = s + i; } return s; }", 10},
	{"range-continue", "function main(): i32 { var s = 0; for i in 0..10 { if (i % 2 == 1) { continue; } s = s + i; } return s; }", 20},
	{"range-break", "function main(): i32 { var s = 0; for i in 0..100 { if (i == 5) { break; } s = s + i; } return s; }", 10},
	{"range-empty", "function main(): i32 { var c = 7; for i in 5..5 { c = c + 1; } return c; }", 7},
	{"range-nested", "function main(): i32 { var t = 0; for i in 0..3 { for j in 0..3 { t = t + 1; } } return t; }", 9},
}

// TestSelfHostInterpDriverX86_64 is the keystone of the inference
// overhaul: the self-hosted compiler compiles the self-hosted
// INTERPRETER (interp.fern, whose Value union has VInt/VString/VFloat
// all with field `v` — previously mis-inferred). The resulting binary
// evaluates programs and exits with their result.
func TestSelfHostInterpDriverX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	// The interp "driver" is just a program importing ./lexer + ./parser +
	// ./interp, compiled by the file-based asm driver (no bundle_run).
	files := map[string]string{"main.fern": interpDriverMod}
	for _, m := range []string{"util", "lexer", "parser", "interp"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", m+".fern"))
		if err != nil {
			t.Fatalf("read %s.fern: %v", m, err)
		}
		files[m+".fern"] = string(src)
	}
	interpAsm, progDir := compileFilesModload(t, runner, driverBin, files)
	if len(interpAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the interp driver")
	}
	interpBin := buildBin(t, gcc, progDir, "interp", interpAsm)

	for _, tc := range interpProgs {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(interpBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], interpBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("interp(%q) exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostInterpDriverArm64 — CI-gated arm64 counterpart.
func TestSelfHostInterpDriverArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)
	files := map[string]string{"main.fern": interpDriverMod}
	for _, m := range []string{"util", "lexer", "parser", "interp"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", m+".fern"))
		if err != nil {
			t.Fatalf("read %s.fern: %v", m, err)
		}
		files[m+".fern"] = string(src)
	}
	interpAsm, progDir := compileFilesModload(t, x86runner, driverBin, files)
	interpBin := buildBin(t, arm64gcc, progDir, "interp", interpAsm)

	for _, tc := range interpProgs {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runArm64Bin(qemu, interpBin)
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("interp(%q) exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
