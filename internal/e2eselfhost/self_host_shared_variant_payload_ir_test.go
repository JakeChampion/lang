package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Two enums may declare the same variant name at DIFFERENT payload types. The
// parser desugars every variant into a StructDecl keyed by the bare name, so
// `enum A { W(i32) }` + `enum B { W(f64) }` leaves two decls called `W`, and the
// by-name decl accessors (decl_field_type, struct_field_width, decl_field_count,
// decl_is_leaksafe) all answer for whichever was declared FIRST.
//
// A match arm on the SECOND enum therefore read its payload at the first enum's
// type and width. Where the two widths agree that is only a wrong dispatch tag;
// where they don't it is a silent MISCOMPILE — an f64 payload read as i32 gives
// the low half of the double, an i64 payload truncates, a string payload is
// typed i32 so `.len()` reads garbage. Measured against the interpreter oracle
// before the fix: f64 7→2, string 5→0, i64 9→2.
//
// An arm is resolved against the SCRUTINEE, so the arm's enum is known: the
// pattern's `Enum.` qualifier when it has one, else the scrutinee's type. The
// lowering now reads every per-decl property through the decl that names
// (variant_arm_decl_index), not through the first decl of that name.
//
// The sibling file self_host_shared_variant_name_ir_test.go covers the
// same-name/same-shape half, which bailed the module rather than miscompiling.
var sharedVariantPayloadCases = []struct {
	name string
	src  string
}{
	// f64 payload behind an i32 one: the read width differs, so the wrong
	// decl produced a value that compared unequal to its own literal.
	{"f64-behind-i32", `enum A { W(i32), P }
enum B { W(f64), Q }
function main(): i32 { var b: B = B.W(4.5); match (b) { B.W(n) => { if (n == 4.5) { return 7; } return 2; }, _ => { return 1; } } }`},
	// The same shape as a match EXPRESSION: the result temp is classified
	// from the payload field type too, on a separate code path.
	{"f64-behind-i32-match-expr", `enum A { W(i32), P }
enum B { W(f64), Q }
function main(): i32 { var b: B = B.W(4.5); var v: f64 = match (b) { B.W(n) => n, _ => 0.0 }; if (v == 4.5) { return 7; } return 2; }`},
	// i64 behind i32: the payload truncated to its low 32 bits.
	{"i64-behind-i32", `enum A { W(i32), P }
enum B { W(i64), Q }
function main(): i32 { var b: B = B.W(4294967296); match (b) { B.W(n) => { if (n == 4294967296) { return 9; } return 2; }, _ => { return 1; } } }`},
	{"i64-behind-i32-match-expr", `enum A { W(i32), P }
enum B { W(i64), Q }
function main(): i32 { var b: B = B.W(4294967296); var v: i64 = match (b) { B.W(n) => n, _ => 0 }; if (v == 4294967296) { return 9; } return 2; }`},
	// string behind i32: the binding was typed i32, so `.len()` did not
	// resolve as a string method.
	{"string-behind-i32", `enum A { W(i32), P }
enum B { W(string), Q }
function main(): i32 { var b: B = B.W("hello"); match (b) { B.W(n) => { return n.len(); }, _ => { return 1; } } }`},
	// A struct payload behind an i32 one: this half BAILED the module
	// (`did not lower: field access .x`) rather than miscompiling.
	{"struct-behind-i32", `struct Pt { x: i32, y: i32 }
enum A { W(i32), P }
enum B { W(Pt), Q }
function main(): i32 { var b: B = B.W(Pt { x: 3, y: 4 }); match (b) { B.W(n) => { return n.x + n.y; }, _ => { return 1; } } }`},
	// i32 behind i64, i.e. the wide decl first: `return n` from an i32
	// function saw an i64-typed binding and bailed the module.
	{"i32-behind-i64", `enum A { W(i64), P }
enum B { W(i32), Q }
function main(): i32 { var b: B = B.W(4); match (b) { B.W(n) => { return n; }, _ => { return 1; } } }`},
	// A unit variant first, a payload variant second: the arity check read
	// the unit decl's zero fields and bailed.
	{"payload-behind-unit", `enum A { W, P }
enum B { W(i32), Q }
function main(): i32 { var b: B = B.W(4); match (b) { B.W(n) => { return n; }, _ => { return 1; } } }`},
	// Multi-payload variants whose payload types are TRANSPOSED, so a
	// per-field lookup that resolves the decl but keeps first-wins per field
	// still reads both fields at the wrong type.
	{"transposed-multi-payload", `enum A { W(i32, string), P }
enum B { W(string, i32), Q }
function main(): i32 { var b: B = B.W("abcd", 3); match (b) { B.W(s, n) => { return s.len() + n; }, _ => { return 1; } } }`},
	// UNQUALIFIED pattern: no `Enum.` qualifier to resolve against, so the
	// arm's enum comes from the scrutinee's type alone.
	{"unqualified-pattern", `enum A { W(i32), P }
enum B { W(f64), Q }
function main(): i32 { var b: B = B.W(4.5); match (b) { W(n) => { if (n == 4.5) { return 7; } return 2; }, _ => { return 1; } } }`},
	// A PARAMETER scrutinee rather than a local, so the enum is recovered
	// from the param's declared type.
	{"param-scrutinee", `enum A { W(i32), P }
enum B { W(f64), Q }
function f(b: B): i32 { match (b) { B.W(n) => { if (n == 4.5) { return 7; } return 2; }, _ => { return 1; } } }
function main(): i32 { return f(B.W(4.5)); }`},
	// Both enums matched in one program, so neither can be right by
	// accident of being the only one used.
	{"both-enums-matched", `enum A { W(i32), P }
enum B { W(f64), Q }
function main(): i32 { var a: A = A.W(2); var b: B = B.W(4.5); var r: i32 = a_of(a);
    match (b) { B.W(n) => { if (n == 4.5) { r = r + 10; } }, _ => {} }
    return r; }
function a_of(a: A): i32 { match (a) { A.W(n) => { return n; }, _ => { return 0; } } }`},
	// A single enum, unchanged by the fix: the by-name and by-owner lookups
	// agree whenever the variant name is unique.
	{"single-enum-control", `enum S2 { W(f64), Q }
function main(): i32 { var b: S2 = S2.W(4.5); match (b) { S2.W(n) => { if (n == 4.5) { return 7; } return 2; }, _ => { return 1; } } }`},
	// A union member (`type X = A | B`) shares the arm-lowering path but is
	// an ordinary uniquely-named struct with real field names — it must keep
	// binding the box pointer, not a `__ev` payload.
	{"union-member-control", `struct Num { value: i32 }
struct Word { n: i32 }
type Tok = Num | Word;
function main(): i32 { var t: Tok = Num { value: 6 }; match (t) { Num(x) => { return x.value; }, Word(w) => { return w.n; } } }`},
}

func TestSelfHostSharedVariantPayloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range sharedVariantPayloadCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.src + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, prog)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
