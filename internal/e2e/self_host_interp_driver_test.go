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

func interpBundle(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, m := range []struct{ name, file string }{
		{"lexer", "lexer.fern"}, {"parser", "parser.fern"}, {"interp", "interp.fern"},
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", m.file))
		if err != nil {
			t.Fatalf("read %s: %v", m.file, err)
		}
		b.WriteString("///MODULE " + m.name + "\n")
		b.Write(src)
		b.WriteString("\n")
	}
	b.WriteString("///MODULE main\n")
	b.WriteString(interpDriverMod)
	return b.Bytes()
}

var interpProgs = []struct {
	name string
	src  string
	exit int
}{
	{"return-literal", "function main(): i32 { return 42; }", 42},
	{"arith", "function main(): i32 { return 6 * 7; }", 42},
	{"locals", "function main(): i32 { var x: i32 = 10; var y: i32 = 32; return x + y; }", 42},
	{"if", "function main(): i32 { if (5 > 3) { return 1; } return 0; }", 1},
	{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(19, 23); }", 42},
	{"float", "function main(): i32 { var f: f64 = 3.5; var g: f64 = 2.5; if (f + g > 5.0) { return 7; } return 0; }", 7},
	// `as` numeric casts — the interp's unary evaluator previously errored
	// on every `as_<Type>` op ("unknown unary op"); now an integer-target
	// cast is identity on an int / truncates a float, and a float-target
	// cast widens an int / is identity on a float.
	{"cast-i64-to-i32", "function main(): i32 { var v: i64 = 9; return v as i32; }", 9},
	{"cast-f64-to-i32", "function main(): i32 { var f: f64 = 3.9; return f as i32; }", 3},
	{"cast-i32-to-f64", "function main(): i32 { var n: i32 = 5; var f: f64 = n as f64; return (f + 0.5) as i32; }", 5},
	{"cast-in-i64-array-sum", "function main(): i32 { var xs: i64[] = [3, 5, 90]; var s: i64 = 0; for v in xs { s = s + v; } return s as i32; }", 98},
}

// TestSelfHostInterpDriverX86_64 is the keystone of the inference
// overhaul: the self-hosted compiler compiles the self-hosted
// INTERPRETER (interp.fern, whose Value union has VInt/VString/VFloat
// all with field `v` — previously mis-inferred). The resulting binary
// evaluates programs and exits with their result.
func TestSelfHostInterpDriverX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	for _, name := range []string{"interp.fern", "flatten.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "bundle_run.fern", "driver")
	interpAsm := runCapture(t, gcc, runner, driverBin, interpBundle(t))
	if len(interpAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the interp driver")
	}
	interpBin := buildBin(t, gcc, dir, "interp", string(interpAsm))

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
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "interp.fern", "flatten.fern", "bundle_run_arm64.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "bundle_run_arm64.fern", "driver")
	interpAsm := runCapture(t, x86gcc, x86runner, driverBin, interpBundle(t))
	interpBin := buildBin(t, arm64gcc, dir, "interp", string(interpAsm))

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
