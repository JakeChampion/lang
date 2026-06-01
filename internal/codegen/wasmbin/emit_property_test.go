package wasmbin

// Thin, hermetic property tests for the wasm backend — the third
// sibling alongside internal/codegen/arm64/emit_property_test.go and
// internal/codegen/x86_64/emit_property_test.go.
//
// wasmbin is the largest of the three backends (the active wasm
// emitter, ~6k lines of runtime + ~3k lines of section assembly).
// build_test.go exercises end-to-end compile + wasmtime runs;
// determinism_test.go pins reproducibility on a small map-indexed
// matrix. This file adds the missing third axis: a feature spread
// matching the native backends' featureMatrix so a "this construct
// doesn't lower" regression surfaces here as a single named subtest
// failure instead of falling through to an opaque e2e break.
//
// As with the native suites the assertions are spelling-independent:
// each program must lower without error, the module bytes must
// validate as wasm (magic + version), and substantially more code
// must produce substantially more bytes. We do NOT spell-match
// instructions or section ordering (which churn). All emit paths
// (plain core module + the preview-2 CLI-wrap path that threads
// through a different post-pass) are exercised.
//
// Coverage parity is the point: when arm64 / x86-64 grow a case,
// wasmbin gets the same case so a feature gap in any one backend
// surfaces in the matching subtest. The featureMatrix here mirrors
// the native sibs verbatim (sans the `function (p: Point) sq()`-
// style receiver method, which the wasm path takes through the same
// __method_-mangling rewrite, hence covered by the same source).

import (
	"bytes"
	"testing"
)

// featureMatrix mirrors the arm64 / x86-64 featureMatrix in
// emit_property_test.go: a spread of small self-contained programs
// (each defines main, no imports) covering the core language
// surface the backend must lower — arithmetic, control flow,
// recursion (TCO-eligible), loops, structs + receiver methods,
// function values, string output / concat, array iteration,
// match-on-Option, and closures (capture-by-value, capture-by-
// string). Every program must lower without error under every
// option variant.
var featureMatrix = map[string]string{
	"minimal": `
function main(): i32 { return 0; }`,

	"arithmetic": `
function main(): i32 {
	var x: i32 = 6;
	var y: i32 = 7;
	return x * y + (y - x) / 2;
}`,

	"if_else": `
function classify(n: i32): i32 {
	if (n > 0) { return 1; } else if (n < 0) { return -1; }
	return 0;
}
function main(): i32 { return classify(-5) + classify(5) + classify(0); }`,

	"while_loop": `
function main(): i32 {
	var i: i32 = 0;
	var sum: i32 = 0;
	while (i < 10) { sum = sum + i; i = i + 1; }
	return sum;
}`,

	"recursion_tco": `
function fact(n: i32, acc: i32): i32 {
	if (n == 0) { return acc; }
	return fact(n - 1, acc * n);
}
function main(): i32 { return fact(5, 1); }`,

	"struct_method": `
struct Point { x: i32, y: i32 }
function (p: Point) sq(): i32 { return p.x * p.x + p.y * p.y; }
function main(): i32 {
	var p: Point = Point { x: 3, y: 4 };
	return p.sq();
}`,

	"function_value": `
function add(a: i32, b: i32): i32 { return a + b; }
function apply(f: (i32, i32) => i32, a: i32, b: i32): i32 { return f(a, b); }
function main(): i32 { return apply(add, 20, 22); }`,

	"string_print": `
function main(): i32 {
	print("hello");
	return 0;
}`,

	"bitwise": `
function main(): i32 {
	var a: i32 = 0xF0;
	var b: i32 = 0x0F;
	return (a | b) & 0xFF ^ (a >> 2);
}`,

	"string_concat": `
function main(): i32 {
	var s: string = "hello" + " " + "world";
	return s.len();
}`,

	"array_iterate": `
function main(): i32 {
	var xs: i32[] = [1, 2, 3];
	xs = xs.push(4);
	var sum: i32 = 0;
	for x in xs { sum = sum + x; }
	return sum;
}`,

	"match_option": `
function maybe(n: i32): Option[i32] {
	if (n > 0) { return Some(n); }
	return None;
}
function main(): i32 {
	match (maybe(7)) {
		Some(v) => { return v; },
		None => { return 0; }
	}
	return -1;
}`,

	"closure_capture": `
function adder(n: i32): (i32) => i32 {
	return function (x: i32): i32 { return x + n; };
}
function main(): i32 {
	var add10: (i32) => i32 = adder(10);
	var add20: (i32) => i32 = adder(20);
	return add10(1) + add20(2);
}`,

	"closure_string_capture": `
function greeter(name: string): () => string {
	return function (): string { return "hello, " + name; };
}
function main(): i32 {
	var g: () => string = greeter("world");
	return g().len();
}`,
}

// emitOptionVariants is the set of BuildOptions every featureMatrix
// program must lower under. Mirrors determinism_test.go's variant
// list (the same emit paths that need to be reproducible also need
// to lower the full feature surface). Components are NOT wrapped here
// (PrintMainResult / HttpHandler / Preview2WASI thread through their
// own emit pipelines that need source-shape preconditions, e.g. a
// `handle` function for HttpHandler — out of scope for this matrix).
var emitOptionVariants = map[string]BuildOptions{
	"plain":     {},
	"synth_cli": {SynthCliRun: true, SynthStart: true},
	"force_mem": {ForceMemorySection: true},
}

// wasmModuleMagic is the 8-byte wasm module preamble — 4-byte magic
// `\0asm` + 4-byte version `1`. Any valid wasm module starts with
// these bytes; a build that doesn't is structurally broken (truncated
// / wrong format / wrong section order — anything that prevents the
// magic from landing at offset 0).
var wasmModuleMagic = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// hasWasmMagic reports whether bin starts with the wasm module
// preamble (magic + version). Used as a structural sanity check on
// emitter output before the more detailed property assertions.
func hasWasmMagic(bin []byte) bool {
	return len(bin) >= len(wasmModuleMagic) && bytes.Equal(bin[:len(wasmModuleMagic)], wasmModuleMagic)
}

// TestBuildFeatureMatrix asserts every program in featureMatrix
// lowers to a structurally-valid wasm module under every option
// variant. A backend gap surfaces as a named subtest failure
// (e.g. `TestBuildFeatureMatrix/synth_cli/closure_string_capture`)
// pointing straight at the case + option combo that broke, rather
// than an opaque e2e failure downstream.
func TestBuildFeatureMatrix(t *testing.T) {
	for variant, opts := range emitOptionVariants {
		variant, opts := variant, opts
		t.Run(variant, func(t *testing.T) {
			for name, src := range featureMatrix {
				name, src := name, src
				t.Run(name, func(t *testing.T) {
					bin := mustBuild(t, src, opts)
					if len(bin) == 0 {
						t.Fatal("emitted module is empty")
					}
					if !hasWasmMagic(bin) {
						preview := bin
						if len(preview) > 16 {
							preview = preview[:16]
						}
						t.Errorf("emitted module missing wasm magic preamble (got prefix % x)", preview)
					}
				})
			}
		})
	}
}

// TestBuildGrowsWithCode is the wasm sibling of arm64 / x86-64's
// TestEmitGrowsWithCode: a coarse "the emitter actually walked the
// body" check that catches a class of regressions where a function
// silently fails to lower yet still returns without error. A
// substantially larger program must produce substantially more
// bytes than a trivial one.
func TestBuildGrowsWithCode(t *testing.T) {
	small := mustBuild(t, featureMatrix["minimal"], BuildOptions{})
	big := mustBuild(t, featureMatrix["recursion_tco"], BuildOptions{})
	if len(big) <= len(small) {
		t.Fatalf("expected non-trivial program to emit more bytes than minimal: big=%d, small=%d", len(big), len(small))
	}
}

// (Determinism is covered separately in determinism_test.go's
// TestBuildDeterministic, which runs each featureMatrix sibling
// across the same option variants and asserts byte-identical
// repeat builds. Keeping the two concerns split mirrors the
// arm64 / x86-64 split between TestEmitFeatureMatrix and
// TestEmitDeterministic.)
