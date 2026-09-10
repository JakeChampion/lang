package e2eselfhost

import "testing"

// --- A closure array reaches a callee through a parameter or a passthrough
//
// The self-host keeps two representations for a fn-typed array element: a
// bare code pointer for a named function, an env box for a closure. Which one
// a callee's `a[i](x)` dispatches on is decided by an interprocedural proof
// (fn_param_sigs_of's '3' flag): every call site must provably pass an array
// of boxes. The proof knew a lambda written inline, a `__mkclo$` box and a
// factory call as box elements, and a closurearr-returning call as a box
// array. It did not know an IDENT the caller had bound to a closure
// (`var f = (x) => x; call0([f])`), nor a generic passthrough handing such an
// array straight back (`id([f, f])`), so those arrays read as bare-pointer
// arrays, the callee's param went unproven, and `a[0](7)` jumped into the box
// — SIGSEGV on both natives where the Go compiler answers 7. Nightly
// differential seed 24937 is the passthrough shape.
//
// The two bare-function rows are the other half of the contract: a named-fn
// array must stay on plain dispatch, or a correct program would bare-call an
// env-first thunk. Every want was read off `bin/fern -interp` and the native
// x86-64 backend, never off the self-host.

const closureArrParamProlog = "function id[T](x: T): T { return x; }\n" +
	"function inc(x: i32): i32 { return x + 1; }\n" +
	"function dbl(x: i32): i32 { return x * 2; }\n" +
	"function call0(a: ((i32) => i32)[]): i32 { return a[0](7); }\n" +
	"function call1(a: ((i32) => i32)[]): i32 { return a[1](7) + a[0](1); }\n" +
	"function sum2(a: ((i32) => i32)[]): i32 { var acc: i32 = 0; for x in a { acc = acc + x(2); } return acc; }\n" +
	"function idc(x: ((i32) => i32)[]): ((i32) => i32)[] { return x; }\n"

// idc2 forwards its param to idc, so it is declared only by the row that
// calls it: an uncalled forwarder's param has no call site to prove it, and
// its `idc(x)` would then keep idc's own param unproven for everyone.
const closureArrForwarder = "function idc2(x: ((i32) => i32)[]): ((i32) => i32)[] { return idc(x); }\n"

func closureArrParamCases() []struct {
	name, decls, body string
	want              int
} {
	return []struct {
		name, decls, body string
		want              int
	}{
		// A closure LOCAL as the element: at the call, and bound to an array first.
		{"closure_local_in_literal_arg", "", "var f: (i32) => i32 = ((x: i32) => x); return call0([f]);", 7},
		{"closure_local_array_arg", "", "var f: (i32) => i32 = ((x: i32) => x); var fs: ((i32) => i32)[] = [f]; return call0(fs);", 7},
		{"capturing_local_array_arg", "", "var k: i32 = 3; var f: (i32) => i32 = ((x: i32) => x + k); var fs: ((i32) => i32)[] = [f]; return call0(fs);", 10},
		// The generic passthrough, with a literal and with a local behind it —
		// seed 24937's shape. Read back in the CALLER too (sum2 is the callee).
		{"passthru_literal", "", "var f: (i32) => i32 = ((x: i32) => x); var fs: ((i32) => i32)[] = id([f, f]); return sum2(fs);", 4},
		{"passthru_local", "", "var f: (i32) => i32 = ((x: i32) => x); var a: ((i32) => i32)[] = [f, f]; var fs: ((i32) => i32)[] = id(a); return sum2(fs);", 4},
		{"passthru_caller_reads", "", "var f: (i32) => i32 = ((x: i32) => x); var fs: ((i32) => i32)[] = id([f, f]); var acc: i32 = 0; for x in fs { acc = acc + x(2); } return acc;", 4},
		// The NON-generic identity (#8870): `idc` returns a closure array
		// exactly when its param is proven one, and that proof accepts a
		// closurearr-returning call as an argument — the two registries are
		// one fixpoint. idc2 needs both directions: its param from main's
		// literal, then idc's from idc2's forwarded param, then idc's return
		// from its param, then idc2's return from the call to idc.
		{"identity_param", "", "var v0: (i32) => i32 = ((x: i32) => x); var v1: (i32) => i32 = ((x: i32) => x + 1); var v3: ((i32) => i32)[] = idc([v1, v0]); return v3[1](7);", 7},
		{"identity_param_forwarded", closureArrForwarder, "var v0: (i32) => i32 = ((x: i32) => x); var v1: (i32) => i32 = ((x: i32) => x + 1); var v3: ((i32) => i32)[] = idc2([v1, v0]); return v3[0](7);", 8},
		// Bare named functions stay bare: plain dispatch, no env — through
		// the identity too, whose param is then NOT proven a closure array.
		{"bare_fn_literal_arg", "", "return call1([inc, dbl]);", 16},
		{"bare_fn_local_array_arg", "", "var fs: ((i32) => i32)[] = [inc]; return call0(fs);", 8},
		{"bare_fn_through_identity", "", "var fs: ((i32) => i32)[] = idc([inc, dbl]); return fs[1](7);", 14},
	}
}

func TestSelfHostClosureArrParamX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range closureArrParamCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := closureArrParamProlog + tc.decls + "function main(): i32 { " + tc.body + " }\n"
			asm := hevCompile(t, runner, driverBin, src, nil)
			progBin := buildBin(t, gcc, dir, "closurearrparam_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s: exit %d, want %d (a signal here is the callee bare-calling an env box as code)", tc.name, exit, tc.want)
			}
		})
	}
}
