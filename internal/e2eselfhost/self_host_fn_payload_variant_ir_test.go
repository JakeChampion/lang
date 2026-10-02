package e2eselfhost

import (
	"strings"
	"testing"
)

// An enum variant carrying a FUNCTION-typed payload — the shape std/task's
// Step/Future need — must round-trip on the self-host IR path: construct the
// variant with a function value in payload position, store it, `match`-bind the
// payload back out, and call it indirectly. docs/SELF-HOST-FN-PAYLOAD-VARIANT-GAP.md
// (#4364) documented this failing on the older SSA/AST path (a variant
// constructor mis-emitted as `call __fn_<Variant>`). It now composes on the IR
// path — the closure-conv work (#4354 + the CLOSURE-CONV slices) lowers a
// function value in payload position as an ordinary pointer-sized payload — so
// this pins the construction → store → match-bind → indirect-call round-trip
// across the shapes the issue names.
//
// The shapes include the generic recursive `Future[T]`, whose payload fn
// returns the generic enum itself.
//
// Each program computes 42 via the payload function; the interp oracle agrees.
var fnPayloadVariantCases = []struct {
	name string
	src  string
}{
	// Repro B from the gap doc: a named function as the payload of a 2-arg variant.
	{"named-fn", "enum Box { Fn(i32, (i32) => i32), Empty }\n" +
		"function add(x: i32): i32 { return x + 1; }\n" +
		"function main(): i32 {\n" +
		"    let b: Box = Fn(10, add);\n" +
		"    match (b) { Fn(n, f) => { return n + f(31); }, Empty => { return 0; } }\n" +
		"}\n"},
	// A CAPTURING closure (bound to a local) as the payload.
	{"capturing-closure", "enum Box { Fn(i32, (i32) => i32), Empty }\n" +
		"function main(): i32 {\n" +
		"    let k: i32 = 5;\n" +
		"    let g: (i32) => i32 = (x: i32): i32 => x + k;\n" +
		"    let b: Box = Fn(10, g);\n" +
		"    match (b) { Fn(n, f) => { return n + f(27); }, Empty => { return 0; } }\n" +
		"}\n"},
	// The recursive std/task `Step` shape: the payload fn returns the enum itself.
	{"recursive-step", "enum Step { Done(i32), Wait(i32, (i32) => Step) }\n" +
		"function resume(tok: i32): Step { return Done(tok + 1); }\n" +
		"function main(): i32 {\n" +
		"    let s: Step = Wait(41, resume);\n" +
		"    match (s) {\n" +
		"        Wait(tok, cont) => {\n" +
		"            match (cont(tok)) { Done(v) => { return v; }, Wait(t2, c2) => { return 0; } }\n" +
		"        },\n" +
		"        Done(v) => { return v; }\n" +
		"    }\n" +
		"}\n"},
	// A GENERIC enum instantiated at i32, with a fn payload.
	{"generic-enum", "enum Box[T] { Fn(T, (T) => T), Empty }\n" +
		"function inc(x: i32): i32 { return x + 1; }\n" +
		"function main(): i32 {\n" +
		"    let b: Box[i32] = Fn(41, inc);\n" +
		"    match (b) { Fn(n, f) => { return f(n); }, Empty => { return 0; } }\n" +
		"}\n"},
	// The canonical Blocker-2 shape (docs/ASYNC-SELFHOST-IR.md): a generic AND
	// recursive user enum whose payload fn RETURNS the generic enum itself —
	// exactly std/task's `Future[T] = Ready(T) | Pending(i32, (i32) => Future[T])`.
	{"future-generic-recursive", "enum Future[T] { Ready(T), Pending(i32, (i32) => Future[T]) }\n" +
		"function step(tok: i32): Future[i32] { return Ready(tok + 1); }\n" +
		"function main(): i32 {\n" +
		"    let f: Future[i32] = Pending(41, step);\n" +
		"    match (f) {\n" +
		"        Pending(tok, cont) => {\n" +
		"            match (cont(tok)) { Ready(v) => { return v; }, Pending(t2, c2) => { return 0; } }\n" +
		"        },\n" +
		"        Ready(v) => { return v; }\n" +
		"    }\n" +
		"}\n"},
}

// TestSelfHostFnPayloadVariantIR pins the round-trip on x86-64.
func TestSelfHostFnPayloadVariantIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range fnPayloadVariantCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src)
			// Constructors must lower as values, not calls to variant names.
			if strings.Contains(asm, "call __fn_Fn") || strings.Contains(asm, "call __fn_Wait") {
				t.Fatalf("%s: variant constructor mis-emitted as a direct call (#4364 regression)\n%s", tc.name, asm)
			}
			if code, _ := cli.runX86(t, asm); code != 42 {
				t.Errorf("%s (x86-64) exited %d, want 42", tc.name, code)
			}
		})
	}
}

// TestSelfHostFnPayloadVariantIRWasm is the wasm leg.
func TestSelfHostFnPayloadVariantIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range fnPayloadVariantCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != 42 {
				t.Errorf("%s (wasm) exited %d, want 42", tc.name, code)
			}
		})
	}
}
