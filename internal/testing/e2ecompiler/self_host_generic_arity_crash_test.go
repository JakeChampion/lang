package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFern(t *testing.T, dir, name, src string) string {
	t.Helper()
	path := filepath.Join(dir, "genarity_"+name+".fern")
	if err := os.WriteFile(path, []byte(src+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

var genericArityCrashCases = []struct {
	name string
	src  string
}{
	{"struct_var", `struct Pair[A, B] { first: A, second: B }
function main(): i32 { let p: Pair[i32] = Pair { first: 1, second: 2 }; return p.first; }`},
	{"struct_param", `struct Pair[A, B] { first: A, second: B }
function f(p: Pair[i32]): i32 { return 0; }
function main(): i32 { return 0; }`},
	{"struct_field", `struct Pair[A, B] { first: A, second: B }
struct Holder { p: Pair[i32] }
function main(): i32 { return 0; }`},
	{"struct_return", `struct Pair[A, B] { first: A, second: B }
function f(): Pair[i32] { return Pair { first: 1, second: 2 }; }
function main(): i32 { return 0; }`},
	{"struct_nested_array", `struct Pair[A, B] { first: A, second: B }
function main(): i32 { let xs: Pair[i32][] = []; return 0; }`},
	{"struct_three_params_one_supplied", `struct T[A, B, C] { a: A, b: B, c: C }
function main(): i32 { let p: T[i32] = T { a: 1, b: 2, c: 3 }; return p.a; }`},
	{"unused_tparam_still_rejected", `struct Pair[A, B] { first: A, second: A }
function main(): i32 { let p: Pair[i32] = Pair { first: 1, second: 2 }; return p.first; }`},

	{"enum_var", `enum E[A, B] { L(A), R(B) }
function main(): i32 { let e: E[i32] = L(1); return 0; }`},
	{"enum_param", `enum E[A, B] { L(A), R(B) }
function f(e: E[i32]): i32 { return 0; }
function main(): i32 { return 0; }`},
	{"enum_field", `enum E[A, B] { L(A), R(B) }
struct H { e: E[i32] }
function main(): i32 { return 0; }`},
	{"enum_three_params_one_supplied", `enum E[A, B, C] { L(A), M(B), R(C) }
function main(): i32 { let e: E[i32] = L(1); return 0; }`},
}

var genericArityOKCases = []struct {
	name string
	src  string
	want int
}{
	{"struct_correct_arity", `struct Pair[A, B] { first: A, second: B }
function main(): i32 { let p: Pair[i32, string] = Pair { first: 7, second: "x" }; return p.first; }`, 7},
	{"enum_correct_arity", `enum E[A, B] { L(A), R(B) }
function main(): i32 { let e: E[i32, string] = L(4); match (e) { L(v) => { return v; }, R(s) => { return 0; } } }`, 4},
	{"enum_single_param", `enum Box2[T] { Wrap(T), Empty }
function main(): i32 { let b: Box2[i32] = Wrap(5); match (b) { Wrap(v) => { return v; }, Empty => { return 0; } } }`, 5},
	{"builtin_option", `function f(): Option[i32] { return Some(3); }
function main(): i32 { match (f()) { Some(v) => { return v; }, None => { return 0; } } }`, 3},
}

func TestSelfHostGenericArityNoCrashX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern",
		"parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern",
		"irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "genarity")

	for _, tc := range genericArityCrashCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, code := runFixtureInterp(t, writeTempFern(t, dir, tc.name, tc.src), ""); code == 0 {
				t.Fatalf("%s: interp accepted the program — it is not an arity error, "+
					"so refusing it would be a regression, not a fix", tc.name)
			}

			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driver)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driver)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			_ = cmd.Run()

			if got := stderr.String(); strings.Contains(got, "array index out of range") {
				t.Errorf("%s: compiler aborted instead of rejecting the program:\n%s",
					tc.name, got)
			}
			if code := cmd.ProcessState.ExitCode(); code < 0 || code == 134 {
				t.Errorf("%s: compiler died (exit %d) instead of rejecting the program; stderr:\n%s",
					tc.name, code, stderr.String())
			}
		})
	}
}

func TestSelfHostGenericArityStillCompilesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern",
		"parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern",
		"irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "genarityok")

	for _, tc := range genericArityOKCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, want := runFixtureInterp(t, writeTempFern(t, dir, tc.name, tc.src), ""); want != tc.want {
				t.Fatalf("%s: interp oracle = %d, want %d — the test program is invalid, "+
					"not the compiler", tc.name, want, tc.want)
			}
			asm := string(runCapture(t, gcc, runner, driver, []byte(tc.src+"\n")))
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "genarityok_"+tc.name, asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: self-host run = %d, want %d — the arity guard refused a "+
					"correctly applied generic", tc.name, code, tc.want)
			}
		})
	}
}
