package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateBinaryCases exercise the ExprBinary.ty / ExprUnary.ty carriers — the
// last two in the Phase-A annotate-and-consume migration
// (docs/TYPED-IR-REWRITE.md), after ExprCall.ty (#5531), ExprFieldAccess.ty,
// ExprIndex.ty (#6165), ExprSlice.ty and ExprIdent.ty.
//
// Ordinary arithmetic needs no carrier: irlower's predicates compose a binary's
// type from its OPERANDS, so `a * b` on two f64 locals is already f64. The one
// shape the operand walk cannot reach is a composite operator overload, where
// BOTH operands are structs and the result is whatever the method returns:
//
//	struct V { x: f64, y: f64 }
//	function (a: V) mul(b: V): f64 { return a.x * b.x + a.y * b.y; }
//	var d: f64 = p * q;                       // f64, and no walk over p / q says so
//
// Every case below BAILED the IR path before this change ("did not lower:
// binary `*`" under FERN_STRICT_IR), on both backends, while native compiled
// them and the interpreter ran them. The cause was two layers deep:
//
//   - the self-host CHECKER had no operator-overload arm at all, so `p * q`
//     typed unknown and was rejected E009 — a carrier alone would have stamped
//     "" and changed nothing;
//   - irlower's lowering then asked struct_ret_fns, a registry that records
//     only STRUCT returns, and read its "" as "no such method" rather than "a
//     return I do not record".
//
// So the struct-returning overload (overload_add_struct below) always worked
// and every scalar-returning one was refused. That control is what holds the
// line: it must keep working, and it exercises the registry path the tag does
// not.
//
// The i64 rows additionally need the carrier to reach the LOAD site —
// lower_i64's binary and unary arms — because infer_expr_width reports 64 off
// the same tag. Wired to only one of the two, `(p + q) % 100i64` bailed at the
// enclosing `as i32` instead.
var annotateBinaryCases = []struct {
	name string
	src  string
}{
	{"overload_mul_f64", `struct V { x: f64, y: f64 }
function (a: V) mul(b: V): f64 { return a.x * b.x + a.y * b.y; }
function main(): i32 {
  var p: V = V { x: 3.0, y: 4.0 };
  var q: V = V { x: 2.0, y: 5.0 };
  var d: f64 = p * q;
  return (d * 2.0) as i32;
}`}, // 52
	// The same overload consumed inline, so the enclosing `*` asks
	// expr_is_f64 about an ExprBinary rather than about a typed local.
	{"overload_mul_f64_direct", `struct V { x: f64, y: f64 }
function (a: V) mul(b: V): f64 { return a.x * b.x + a.y * b.y; }
function main(): i32 {
  var p: V = V { x: 3.0, y: 4.0 };
  var q: V = V { x: 2.0, y: 5.0 };
  return ((p * q) * 2.0) as i32;
}`}, // 52
	// i64 return: needs infer_expr_width AND lower_i64's binary arm.
	{"overload_add_i64", `struct C { n: i32 }
function (a: C) add(b: C): i64 { return (a.n as i64) * 1000000000i64 + (b.n as i64); }
function main(): i32 {
  var p: C = C { n: 5 };
  var q: C = C { n: 7 };
  return ((p + q) % 100i64) as i32;
}`}, // 7
	// The unary sibling at i64 width: lower_i64's ExprUnary arm.
	{"overload_neg_i64", `struct C { n: i32 }
function (a: C) neg(): i64 { return (a.n as i64) * 1000000000i64; }
function main(): i32 {
  var p: C = C { n: 5 };
  return (((-p) / 1000000000i64) + 37i64) as i32;
}`}, // 42
	{"overload_add_string", `struct N { n: i32 }
function (a: N) add(b: N): string { return "ab"; }
function main(): i32 {
  var p: N = N { n: 1 };
  var q: N = N { n: 2 };
  var s: string = p + q;
  return s.len() as i32;
}`}, // 2
	{"overload_neg_f64", `struct V { x: f64 }
function (a: V) neg(): f64 { return 0.0 - a.x; }
function main(): i32 {
  var p: V = V { x: 21.0 };
  var d: f64 = -p;
  return (d * 0.0 - d) as i32;
}`}, // 21
	// A boolean-returning overload: without the tag expr_is_bool answered
	// false and expr_scalar_type fell to its "i32" default.
	{"overload_rem_boolean", `struct V { x: i32 }
function (a: V) rem(b: V): boolean { return a.x > b.x; }
function main(): i32 {
  var p: V = V { x: 20 };
  var q: V = V { x: 22 };
  if (p % q) { return 7; }
  return 42;
}`}, // 42

	// Control: a STRUCT-returning overload. This is the shape struct_ret_fns
	// does record, so it lowered before the carrier and must still lower
	// through the registry — the tag must not have displaced it.
	{"overload_add_struct", `struct V { x: i32 }
function (a: V) add(b: V): V { return V { x: a.x + b.x }; }
function main(): i32 {
  var p: V = V { x: 20 };
  var q: V = V { x: 22 };
  return (p + q).x;
}`}, // 42
	// Control: the explicit method call the overload desugars to. It never
	// went through the binary arm at all, so it pins that the fix did not
	// change the ordinary struct-method path.
	{"method_call_control", `struct V { x: f64, y: f64 }
function (a: V) mul(b: V): f64 { return a.x * b.x + a.y * b.y; }
function main(): i32 {
  var p: V = V { x: 3.0, y: 4.0 };
  var q: V = V { x: 2.0, y: 5.0 };
  var d: f64 = p.mul(q);
  return (d * 2.0) as i32;
}`}, // 52
	// Control: ordinary f64 arithmetic, where the operand walk already
	// answers and the tag must be inert.
	{"plain_f64_arithmetic", `function main(): i32 {
  var a: f64 = 3.5;
  var b: f64 = 2.0;
  return ((a * b) + 1.0) as i32;
}`}, // 8
	// Control: ordinary i64 arithmetic through the width walk. An unsuffixed
	// literal types i32 in the self-host checker, so a tag-FIRST width leaf
	// would narrow this; the wiring is additive precisely so it cannot.
	{"plain_i64_arithmetic", `function main(): i32 {
  var a: i64 = 5000000000i64;
  return ((a + 7i64) % 100i64) as i32;
}`}, // 7
}

// TestSelfHostAnnotateBinaryX86_64 runs the cases on the self-host x86-64
// backend. fern.fern is the driver because it runs checker.annotate_module; a
// driver that skips annotation leaves every ty empty, and these cases then bail
// exactly as they did before the change.
func TestSelfHostAnnotateBinaryX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateBinaryCases {
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
				t.Errorf("%s = %d, want %d (interp oracle) — a composite operator overload must type from ExprBinary.ty / ExprUnary.ty", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostAnnotateBinaryWasm is the wasm leg. Both backends bailed
// identically here, so this leg is not carrying a distinct defect — it is what
// proves the desugared call's result width is emitted consistently, which is
// the half the wasm validator checks and x86-64 does not.
func TestSelfHostAnnotateBinaryWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping annotate-binary wasm cases")
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

	for _, tc := range annotateBinaryCases {
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
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}

// annotateBinaryRejectCases pin the other direction: teaching the self-host
// checker about operator overloads must NOT make it accept an operator on a
// type that does not define the method. Native rejects both of these E009, and
// so must the self-host — otherwise the checker-codes differential is the gate
// that would have caught it, and only after this had shipped.
var annotateBinaryRejectCases = []struct {
	name string
	src  string
}{
	{"binary_without_method", `struct W { x: i32 }
function main(): i32 {
  var a: W = W { x: 1 };
  var b: W = W { x: 2 };
  var c: W = a + b;
  return c.x;
}`},
	{"unary_without_method", `struct W { x: i32 }
function main(): i32 {
  var a: W = W { x: 1 };
  var c: W = -a;
  return c.x;
}`},
}

// TestSelfHostAnnotateBinaryRejectsX86_64 asserts the self-host checker still
// emits E009 for an operator with no overload method behind it.
func TestSelfHostAnnotateBinaryRejectsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateBinaryRejectCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			out, _ := runX86_64Bin(runner, fernBin, "-check", mainPath, stdlibRoot).CombinedOutput()
			if !strings.Contains(string(out), "E009") {
				t.Errorf("%s: want E009 from the self-host checker, got:\n%s", tc.name, out)
			}
		})
	}
}
