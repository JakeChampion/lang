package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateIdentCases exercise the ExprIdent.ty carrier — the fifth in the
// Phase-A annotate-and-consume migration (docs/TYPED-IR-REWRITE.md), after
// ExprCall.ty (#5531), ExprFieldAccess.ty and ExprIndex.ty (#6165) and
// ExprSlice.ty.
//
// A bare name gets its type in irlower from the SLOT it reads, and a module
// `const` has no slot at all: its read is really a call to a zero-argument
// accessor. Each ident predicate therefore grew its own const clause, one at a
// time — expr_is_str (#2954), then expr_is_f64 and infer_expr_width (#4801) —
// and expr_is_u32, expr_is_u64 and expr_is_bool never got one. Both halves of
// that omission are live defects, and both are here:
//
//   - `const M: u32` read bare answered false to expr_is_u32, so a chained
//     `>>` selected i32.shr_s. wasm-only: a u32 with bit 31 set is a
//     signed-negative i32 there, while x86-64 and arm64 keep it zero-extended
//     in a 64-bit register and already matched.
//   - `const B: boolean` answered false to expr_is_bool, so expr_scalar_type
//     fell through to its "i32" default and `B.to_json()` rendered 1 / 0
//     rather than true / false. Both backends.
//
// The carrier replaces the per-predicate clauses with one leaf, id_type_tag,
// so the next scalar kind cannot be missed from three places independently.
// The controls below are the ones that used the retired clauses: they answer
// from the annotation now and must be unchanged.
var annotateIdentCases = []struct {
	name string
	src  string
}{
	// The u32 half. Every case shifts a const whose bit 31 is set, so a signed
	// shift and an unsigned one give different answers.
	{"const_u32_shift", `const M: u32 = 2147484527u32;
function main(): i32 { return ((M >> 1u32) % 100u32) as i32; }`}, // 63; was 11
	{"const_u32_shift_bound", `const M: u32 = 2147484527u32;
function main(): i32 { var v: u32 = M >> 1u32; return (v % 100u32) as i32; }`}, // 63; was 11
	{"const_u32_shift_wide", `const M: u32 = 3221225472u32;
function main(): i32 { return ((M >> 4u32) % 100u32) as i32; }`}, // 92; was 32

	// The boolean half. to_json is the reachable boolean method (std/json's
	// `impl Json for boolean`); a bool dispatched as i32 renders 1 / 0, which
	// differs from true / false in LENGTH as well as in text.
	{"const_bool_to_json_true", `import "std/json";
const B: boolean = true;
function main(): i32 { return B.to_json().len() as i32; }`}, // 4; was 1
	{"const_bool_to_json_false", `import "std/json";
const B: boolean = false;
function main(): i32 { return B.to_json().len() as i32; }`}, // 5; was 1

	// Control: the same u32 shift through a LOCAL. The slot carries u32, so
	// this always worked and the leaf must not consult the annotation for it —
	// id_type_tag returns "" for any name with a slot.
	{"local_u32_shift", `function main(): i32 {
    var m: u32 = 2147484527u32;
    return ((m >> 1u32) % 100u32) as i32;
}`}, // 63
	// Control: the same u32 const through a call PARAMETER, which types from
	// the param's slot rather than the ident.
	{"const_u32_via_param", `const A: u32 = 2147484527u32;
function shr(x: u32): u32 { return x >> 1u32; }
function main(): i32 { return (shr(A) % 100u32) as i32; }`}, // 63

	// Control: the i64 const width case (#4801). Without it the accessor
	// returns i64 and the lowering adds i32, which wasmtime rejects outright.
	{"const_i64_width", `const B: i64 = 5000000000i64;
function main(): i32 { return ((B + 1i64) % 100i64) as i32; }`}, // 1
	// Control: the f64 const case (#4801's sibling).
	{"const_f64", `const F: f64 = 12.5;
function main(): i32 { return (F * 4.0) as i32; }`}, // 50
	// Control: the string const case (#2954). A string box read as an array
	// header is a silent miscompile, so this one must not regress quietly.
	{"const_string_len", `const G: string = "hello";
function main(): i32 { return G.len() as i32; }`}, // 5
}

// TestSelfHostAnnotateIdentX86_64 runs the cases on the self-host x86-64
// backend, where the boolean half diverged and the u32 half never did (a u32
// stays zero-extended in a 64-bit register). fern.fern is the driver because it
// runs checker.annotate_module; a driver that skips annotation leaves every ty
// empty and cannot regress.
func TestSelfHostAnnotateIdentX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateIdentCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			rcmd := runX86_64Bin(runner, binPath)
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — a bare name with no slot must type from ExprIdent.ty", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostAnnotateIdentWasm is the wasm leg, and the one that carries the
// u32 cases: only there does a signed shift of a bit-31-set value differ.
func TestSelfHostAnnotateIdentWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping annotate-ident wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateIdentCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — a bare name with no slot must type from ExprIdent.ty", tc.name, got, want)
			}
		})
	}
}
