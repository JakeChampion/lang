package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempFern writes one case's source into dir and returns its path, so the
// interpreter can be asked about the same text the driver is handed.
func writeTempFern(t *testing.T, dir, name, src string) string {
	t.Helper()
	path := filepath.Join(dir, "genarity_"+name+".fern")
	if err := os.WriteFile(path, []byte(src+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// genericArityCrashCases pin that a generic type-argument list whose length does
// not match the declaration is REJECTED rather than aborting the compiler.
//
// The monomorphiser registered an instantiation straight off the annotation
// (`mg_ty` for structs, `genum_key_from_anno` for enums) without comparing the
// supplied argument count to the declared type-parameter count. The clone loop
// then zipped the short key against the full parameter list, and `subst_ty`'s
// `cts[k]` indexed past the end: the driver died with "array index out of range"
// and exit 134 on programs the native compiler diagnoses cleanly (E019).
//
// Native's guard is at the same place — internal/monomorph refuses to record an
// instantiation when `len(sl.TypeArgs) != len(gen.TypeParams)` — so the zips
// downstream of it never see a short argument list either.
//
// The trigger is the UNSUPPLIED parameter being reachable, not the annotation
// itself: `struct Pair[A, B] { first: A, second: A }` with `Pair[i32]` never
// makes `tp_index` return 1 and so compiled fine even before the fix. That is
// the `unused_tparam_still_rejected` case below — it must still be refused,
// since native rejects it too, but it is the one shape that would pass a test
// that only asserted "no crash" on the shapes that happened to be reported.
//
// Teeth verified by rebuilding the compiler at each stage. With neither guard,
// ten of the eleven cases exit 134 — every one except `unused_tparam_still_
// rejected`, which compiled to exit 0. With the `mg_ty` guard alone the six
// struct cases pass and all four enum cases still exit 134, which is what
// showed the enum route needed its own guard rather than sharing mg_ty's.
var genericArityCrashCases = []struct {
	name string
	src  string
}{
	// Struct, one argument short, at each annotation position. The `var` and
	// return positions are the ones the checker does not report E019 for today,
	// so before the fix nothing stopped them reaching the monomorphiser.
	{"struct_var", `struct Pair[A, B] { first: A, second: B }
function main(): i32 { var p: Pair[i32] = Pair { first: 1, second: 2 }; return p.first; }`},
	{"struct_param", `struct Pair[A, B] { first: A, second: B }
function f(p: Pair[i32]): i32 { return 0; }
function main(): i32 { return 0; }`},
	{"struct_field", `struct Pair[A, B] { first: A, second: B }
struct Holder { p: Pair[i32] }
function main(): i32 { return 0; }`},
	{"struct_return", `struct Pair[A, B] { first: A, second: B }
function f(): Pair[i32] { return Pair { first: 1, second: 2 }; }
function main(): i32 { return 0; }`},
	// An array of the under-supplied instantiation, so the mismatch is reached
	// through mg_ty's recursion rather than at the top of the annotation.
	{"struct_nested_array", `struct Pair[A, B] { first: A, second: B }
function main(): i32 { var xs: Pair[i32][] = []; return 0; }`},
	// Two short rather than one, so the guard cannot be an off-by-one that only
	// happens to cover a single missing argument.
	{"struct_three_params_one_supplied", `struct T[A, B, C] { a: A, b: B, c: C }
function main(): i32 { var p: T[i32] = T { a: 1, b: 2, c: 3 }; return p.a; }`},
	{"unused_tparam_still_rejected", `struct Pair[A, B] { first: A, second: A }
function main(): i32 { var p: Pair[i32] = Pair { first: 1, second: 2 }; return p.first; }`},

	// Generic ENUMS take a different route to the same zip: ge_inst_of and
	// me_collect_anno both build the key through genum_key_from_anno, which is
	// why the guard lives there rather than on one caller.
	{"enum_var", `enum E[A, B] { L(A), R(B) }
function main(): i32 { var e: E[i32] = L(1); return 0; }`},
	{"enum_param", `enum E[A, B] { L(A), R(B) }
function f(e: E[i32]): i32 { return 0; }
function main(): i32 { return 0; }`},
	{"enum_field", `enum E[A, B] { L(A), R(B) }
struct H { e: E[i32] }
function main(): i32 { return 0; }`},
	{"enum_three_params_one_supplied", `enum E[A, B, C] { L(A), M(B), R(C) }
function main(): i32 { var e: E[i32] = L(1); return 0; }`},
}

// genericArityOKCases are the negative controls: correct arity must still
// monomorphise and run. Without these the guards could pass the cases above by
// refusing every generic instantiation.
var genericArityOKCases = []struct {
	name string
	src  string
	want int
}{
	{"struct_correct_arity", `struct Pair[A, B] { first: A, second: B }
function main(): i32 { var p: Pair[i32, string] = Pair { first: 7, second: "x" }; return p.first; }`, 7},
	{"enum_correct_arity", `enum E[A, B] { L(A), R(B) }
function main(): i32 { var e: E[i32, string] = L(4); match (e) { L(v) => { return v; }, R(s) => { return 0; } } }`, 4},
	{"enum_single_param", `enum Box2[T] { Wrap(T), Empty }
function main(): i32 { var b: Box2[i32] = Wrap(5); match (b) { Wrap(v) => { return v; }, Empty => { return 0; } } }`, 5},
	{"builtin_option", `function f(): Option[i32] { return Some(3); }
function main(): i32 { match (f()) { Some(v) => { return v; }, None => { return 0; } } }`, 3},
}

// TestSelfHostGenericArityNoCrashX86_64 asserts the driver never aborts on a
// mismatched-arity instantiation. Refusing the module is a legitimate outcome
// here — the programs are invalid and native rejects them — so the assertion is
// on the FAILURE MODE, not on the exit code: a diagnostic is fine, a bounds
// abort is not.
func TestSelfHostGenericArityNoCrashX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern",
		"parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern",
		"irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "genarity")

	for _, tc := range genericArityCrashCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// The interpreter is the validity check: every case here must be a
			// program native REJECTS, else the test is pinning a crash on a
			// legitimate program and the guard is wrong rather than the compiler.
			if _, code := runFixtureInterp(t, writeTempFern(t, dir, tc.name, tc.src), ""); code == 0 {
				t.Fatalf("%s: interp accepted the program — it is not an arity error, "+
					"so refusing it would be a regression, not a fix", tc.name)
			}

			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driver, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driver), "-ir")...)
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

// TestSelfHostGenericArityStillCompilesX86_64 is the other half: a correctly
// applied generic still monomorphises, emits, and produces the oracle's answer.
func TestSelfHostGenericArityStillCompilesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern",
		"parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern",
		"irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "genarityok")

	for _, tc := range genericArityOKCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, want := runFixtureInterp(t, writeTempFern(t, dir, tc.name, tc.src), ""); want != tc.want {
				t.Fatalf("%s: interp oracle = %d, want %d — the test program is invalid, "+
					"not the compiler", tc.name, want, tc.want)
			}
			asm := string(runCapture(t, gcc, runner, driver, []byte(tc.src+"\n"), "-ir"))
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
