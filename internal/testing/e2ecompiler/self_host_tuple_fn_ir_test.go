package e2ecompiler

import (
	"os/exec"
	"testing"
)

// tupleFnIRCases pin tuples with FUNCTION-typed elements on the self-host IR
// path. Three layers carry them:
//   - parser: a depth-1 comma inside the parens means TUPLE, so
//     `((i32) => i32, i32)` is a tuple type whose fn-typed element keeps its
//     signature — the result type is what lets the checker type a call through
//     the element (#7961);
//   - lift: every fn-VALUED tuple element (capturing lambda, no-capture
//     lambda, unshadowed bare fn name) wraps into a `__mkclo$…` env box, so
//     the element representation is uniformly a closure box;
//   - the lowering: the "clo" element tag drives env-first `t.N(args)`
//     dispatch, closure-local binding for `let f = t.0`, and the destructure
//     bind.
//
// Exit codes are cross-checked against the Go reference (`fern -interp`).
var tupleFnIRCases = []struct {
	name string
	src  string
	exit int
}{
	// A capturing lambda in a tuple RETURNED from a factory, element called
	// through the caller's binding — the original probe that exited 255.
	{"returned-capturing", "function mk(): ((i32) => i32, i32) { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 1); return t; } function main(): i32 { let t = mk(); return t.0(37); }", 42},
	// Non-capturing lambda element (wrapped to a $wrap trampoline box).
	{"returned-nocapture", "function mk(): ((i32) => i32, i32) { let t = ((x: i32): i32 => { return x + 1; }, 1); return t; } function main(): i32 { let t = mk(); return t.0(41); }", 42},
	// Local tuple, never crosses a function boundary.
	{"local-tuple", "function main(): i32 { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 1); return t.0(37); }", 42},
	// A bare NAMED function element, local binding.
	{"named-fn-local", "function dbl(x: i32): i32 { return x * 2; } function main(): i32 { let t = (dbl, 1); return t.0(21); }", 42},
	// A bare NAMED function element in a RETURNED tuple: the lift wraps the
	// unshadowed module-fn ident into a trampoline box, so the declared-type
	// "clo" tag and the runtime representation agree.
	{"named-fn-returned", "function dbl(x: i32): i32 { return x * 2; } function mk(): ((i32) => i32, i32) { return (dbl, 1); } function main(): i32 { let t = mk(); return t.0(21); }", 42},
	// TWO closures in one tuple.
	{"two-closures", "function mk(): ((i32) => i32, (i32) => i32) { let n = 1; let m = 2; let t = ((x: i32): i32 => { return x + n; }, (x: i32): i32 => { return x + m; }); return t; } function main(): i32 { let t = mk(); return t.0(19) + t.1(20); }", 42},
	// Destructure the returned tuple and call the bound element (`let (f, k)
	// = mk(); f(…)`): the "clo" tag binds f a closure local.
	{"destructure-call", "function mk(): ((i32) => i32, i32) { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 5); return t; } function main(): i32 { let (f, k) = mk(); return f(32) + k; }", 42},
	// Regression: a plain scalar/string tuple keeps its precise spelling and
	// behaviour under the new fn-segment coarsening.
	{"scalar-tuple-regress", "function mk(): (string, i32) { return (\"hello\", 37); } function main(): i32 { let t = mk(); return t.0.len() + t.1; }", 42},
	// A tuple-with-fn PARAMETER (`callit(t: ((i32) => i32, i32))`): the param's
	// fn element is tagged "clo", so `t.0(args)` inside the callee dispatches
	// env-first.
	{"tuple-fn-param", "function callit(t: ((i32) => i32, i32)): i32 { return t.0(37); } function main(): i32 { let n = 5; let t = ((x: i32): i32 => { return x + n; }, 1); return callit(t); }", 42},
	// A closure in an OPTION payload, UNANNOTATED (`let o = Some(<lambda>)`):
	// the lift wraps the payload into a `__mkclo$` box, the binding is typed
	// "Option[clo]", and the match bind marks f a closure local — checked
	// BEFORE the struct/enum branch (is_enum_like_name must not claim "clo"),
	// or `f(37)` bare-calls the box.
	{"option-clo-payload-local", "function main(): i32 { let n = 5; let o = Some((x: i32): i32 => { return x + n; }); match (o) { Some(f) => { return f(37); }, None => { return 0; } } }", 42},
	// The ANNOTATED sibling: the coarse "fn" payload tag reads as enum-like,
	// so the closure-local mark must run before the struct/enum bind branch.
	{"option-fn-payload-annotated", "function main(): i32 { let n = 5; let o: Option[(i32) => i32] = Some((x: i32): i32 => { return x + n; }); match (o) { Some(f) => { return f(37); }, None => { return 0; } } }", 42},
	// A bare fn NAME as the payload, rather than a lambda (#7959). The
	// annotation is what rules out the const reading of a zero-arg name, so
	// the payload wrap only fires if the annotation survives intact —
	// flatten's type rewrite used to hand a function type to its
	// generic-application branch, and `Option[() => i32]` arrived at the lift
	// as `Option[[i32]]` (an empty base is all the brackets are left of).
	// The wrap then declined, the name kept the zero-arg const reading, and
	// the payload held a1's RESULT: `f()` called address 3.
	{"option-zeroarg-fnname-payload", "function a1(): i32 { return 3; } function main(): i32 { let o: Option[() => i32] = Some(a1); match (o) { Some(f) => { return f(); }, None => { return 0; } } }", 3},
	// Its >0-arg sibling, which took the fn-VALUE path and always worked —
	// here so a fix that keys on arity cannot pass by breaking this one.
	{"option-onearg-fnname-payload", "function inc(x: i32): i32 { return x + 1; } function main(): i32 { let o: Option[(i32) => i32] = Some(inc); match (o) { Some(f) => { return f(41); }, None => { return 0; } } }", 42},
	// Regression guard for the bind-order move: an enum-payload closure
	// (`Op.Apply(<lambda>)` matched and called) keeps working.
	{"enum-fn-payload-regress", "enum Op { Apply((i32) => i32), Nop } function main(): i32 { let n = 5; let o = Op.Apply((x: i32): i32 => { return x + n; }); match (o) { Apply(f) => { return f(37); }, Nop => { return 0; } } }", 42},
	// A NESTED tuple's closure element via an intermediate binding
	// (`let inner = t.0; inner.0(37)`): the binding carries the inner element
	// types, so the inner closure element dispatches env-first.
	{"nested-tuple-clo-via-binding", "function main(): i32 { let n = 5; let t = (((x: i32): i32 => { return x + n; }, 1), 2); let inner = t.0; return inner.0(37); }", 42},
	// Scalar sibling of the nested transfer (pins the tag hand-off shape).
	{"nested-tuple-scalar-via-binding", "function main(): i32 { let t = ((7, 1), 2); let inner = t.0; return inner.0 + 35; }", 42},
	// A scalar-returning CALL as a tuple element (`(add(1,2), 4)`) must
	// lower rather than bail the construction (#5051).
	{"scalar-call-elem", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { let u = (add(1, 2), 4); return u.0 + u.1 + 35; }", 42},
	// A closure tuple-element CALL as an element of ANOTHER tuple literal
	// (`(t.0(3), t.1)`).
	{"clo-elem-call-in-tuple", "function main(): i32 { let k = 4; let t = ((x: i32): i32 => { return x + k; }, k); let u = (t.0(3), t.1); return u.0 + u.1 + 31; }", 42},
	// The #5051 loop-churn differential: a while body rebinding a tuple whose
	// lambda captures a var with an IDENT/ARITHMETIC init (`let k = i % 7`) —
	// the capture's type resolves through nested bindings and i32 ident/arith
	// chains, so the lift wraps it.
	{"loop-tuple-clo-churn", "function main(): i32 { let acc = 0; let i = 0; while (i < 1000) { let k = i % 7; let t = ((x: i32): i32 => { return x + k; }, k); let u = (t.0(3), t.1); acc = (acc + u.0 + u.1) % 1000; i = i + 1; } return acc % 256; }", 226},
	// The DIRECT nested chain `t.0.0(args)` (no intermediate binding): the
	// compact nested element tag "(clo,i32)" joins with a BARE comma, which
	// split_top_commas must split like ", " for the dispatch to find "clo".
	{"direct-chain-call", "function main(): i32 { let n = 5; let t = (((x: i32): i32 => { return x + n; }, 1), 2); return t.0.0(37); }", 42},
	// Loop-churn sibling of the direct chain (a differential-probe repro).
	{"direct-chain-churn", "function main(): i32 { let acc = 0; let i = 0; while (i < 500) { let k = i % 5; let t = (((x: i32): i32 => { return x + k; }, k), i % 3); acc = (acc + t.0.0(2) + t.1) % 1000; i = i + 1; } return acc % 256; }", 243},
	// A closure element of an UNANNOTATED array-of-tuples, called through an
	// element binding (`let t = a[0]; t.0(3)`): the element tuple type comes
	// from the literal's first element, so the call dispatches through the
	// closure rather than as a method `__fn_i32__0` that does not exist.
	{"arrtuple-elem-binding-call", "function main(): i32 { let k = 4; let a = [((x: i32): i32 => { return x + k; }, k)]; let t = a[0]; return t.0(3) + t.1 + 31; }", 42},
	// The inline form `a[j].0(args)` churned in a loop (a differential-probe
	// repro).
	{"arrtuple-elem-inline-churn", "function main(): i32 { let acc = 0; let i = 0; while (i < 300) { let k = i % 6; let a = [((x: i32): i32 => { return x + k; }, k), ((x: i32): i32 => { return x * 2 + k; }, k + 1)]; let j = 0; while (j < a.len()) { acc = (acc + a[j].0(2) + a[j].1) % 1000; j = j + 1; } i = i + 1; } return acc % 256; }", 100},
	// A STRING-capturing lambda in a tuple (`let s = "ab" + "c"` captured for
	// `s.len()`): the capture is typed string from the string+string concat,
	// so the lift wraps it (a string capture occupies the env box's pointer
	// slot).
	{"string-capture-tuple-churn", "function main(): i32 { let acc = 0; let i = 0; while (i < 200) { let s = \"ab\" + \"c\"; let t = ((x: i32): i32 => { return x + s.len(); }, i % 4); acc = (acc + t.0(2) + t.1) % 1000; i = i + 1; } return acc % 256; }", 44},
}

// TestSelfHostTupleFnIRX86_64 — fn-typed tuple elements through the PRODUCTION
// x86-64 IR path (asm_ir_run).
func TestSelfHostTupleFnIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleFnIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostTupleFnIRArm64 — CI-gated arm64 counterpart via the arm64 IR
// path (asm_ir_run `-target arm64-linux`). Shares the fixes in parser.fern +
// the lowering; tuple slots are uniform 8-byte on both register backends.
func TestSelfHostTupleFnIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleFnIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
