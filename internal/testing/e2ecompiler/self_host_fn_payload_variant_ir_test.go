package e2ecompiler

import (
	"strings"
	"testing"
)

var fnPayloadVariantCases = []struct {
	name string
	src  string
}{
	{"named-fn", "enum Box { Fn(i32, (i32) => i32), Empty }\n" +
		"function add(x: i32): i32 { return x + 1; }\n" +
		"function main(): i32 {\n" +
		"    let b: Box = Fn(10, add);\n" +
		"    match (b) { Fn(n, f) => { return n + f(31); }, Empty => { return 0; } }\n" +
		"}\n"},
	{"capturing-closure", "enum Box { Fn(i32, (i32) => i32), Empty }\n" +
		"function main(): i32 {\n" +
		"    let k: i32 = 5;\n" +
		"    let g: (i32) => i32 = (x: i32): i32 => x + k;\n" +
		"    let b: Box = Fn(10, g);\n" +
		"    match (b) { Fn(n, f) => { return n + f(27); }, Empty => { return 0; } }\n" +
		"}\n"},
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
	{"generic-enum", "enum Box[T] { Fn(T, (T) => T), Empty }\n" +
		"function inc(x: i32): i32 { return x + 1; }\n" +
		"function main(): i32 {\n" +
		"    let b: Box[i32] = Fn(41, inc);\n" +
		"    match (b) { Fn(n, f) => { return f(n); }, Empty => { return 0; } }\n" +
		"}\n"},
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

func TestSelfHostFnPayloadVariantIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range fnPayloadVariantCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src)
			if strings.Contains(asm, "call __fn_Fn") || strings.Contains(asm, "call __fn_Wait") {
				t.Fatalf("%s: variant constructor mis-emitted as a direct call (#4364 regression)\n%s", tc.name, asm)
			}
			if code, _ := cli.runX86(t, asm); code != 42 {
				t.Errorf("%s (x86-64) exited %d, want 42", tc.name, code)
			}
		})
	}
}

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
