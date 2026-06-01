package wasmbin

// Determinism guard for the wasm backend.
//
// The op-level coverage in wasmbin_test.go and the run-under-wasmtime
// coverage in build_test.go are thorough, but neither pins
// *reproducibility*: that building the same source twice yields
// byte-identical module bytes. That property is load-bearing here —
// the self-host fixed-point gates assert byte-equal output across
// compiler stages, and reproducible builds depend on it — and it is
// exactly the property most at risk from nondeterminism leaking in
// through Go map iteration order (type dedup tables, function/string
// interning, import ordering all index through maps).
//
// The native backends (arm64, x86-64) gained the same guard in their
// emit_property_test.go suites; this is the wasm sibling. It asserts
// on the emitted bytes directly — no toolchain, no wasmtime — so a
// regression points straight at the emitter.

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// determinismMatrix is a spread of small self-contained programs
// exercising the map-indexed emit paths most prone to ordering
// nondeterminism: multiple functions (function-table / type dedup),
// several distinct string literals (string interning), repeated type
// signatures (type-section dedup), and closures (env layout).
var determinismMatrix = map[string]string{
	"minimal": `
function main(): i32 { return 0; }`,

	"many_funcs_same_sig": `
function a(x: i32): i32 { return x + 1; }
function b(x: i32): i32 { return x + 2; }
function c(x: i32): i32 { return x + 3; }
function main(): i32 { return a(1) + b(2) + c(3); }`,

	"string_interning": `
function main(): i32 {
	print("alpha");
	print("beta");
	print("alpha");
	print("gamma");
	return 0;
}`,

	"closures": `
function adder(n: i32): (i32) => i32 {
	return function (x: i32): i32 { return x + n; };
}
function main(): i32 {
	var f: (i32) => i32 = adder(10);
	var g: (i32) => i32 = adder(20);
	return f(1) + g(2);
}`,

	"mixed_widths": `
function main(): i32 {
	var a: i64 = 1;
	var b: f64 = 2.5;
	var c: i32 = 3;
	return c;
}`,
}

// buildOptionVariants is the set of emit configurations to check for
// reproducibility. The plain path and the preview-2 CLI-wrap path
// both thread through the same map-indexed emit, so both must be
// deterministic.
var buildOptionVariants = map[string]BuildOptions{
	"plain":     {},
	"synth_cli": {SynthCliRun: true, SynthStart: true},
	"force_mem": {ForceMemorySection: true},
}

// TestBuildDeterministic builds each program several times under each
// option variant and asserts every build is byte-identical to the
// first. A failure means nondeterminism (most likely Go map iteration
// order) has leaked into the module bytes — which would break the
// byte-identical self-host gates and reproducible builds.
func TestBuildDeterministic(t *testing.T) {
	for variant, opts := range buildOptionVariants {
		variant, opts := variant, opts
		t.Run(variant, func(t *testing.T) {
			for name, src := range determinismMatrix {
				name, src := name, src
				t.Run(name, func(t *testing.T) {
					first := mustBuild(t, src, opts)
					if len(first) < 8 || !bytes.Equal(first[:4], []byte{0x00, 0x61, 0x73, 0x6d}) {
						t.Fatalf("output is not a wasm module (missing magic): got % x", first[:min(8, len(first))])
					}

					for i := 0; i < 4; i++ {
						again := mustBuild(t, src, opts)
						if !bytes.Equal(first, again) {
							t.Fatalf("build not deterministic on run %d: %d bytes vs %d bytes, first differing at %d",
								i+2, len(first), len(again), firstDiff(first, again))
						}
					}
				})
			}
		})
	}
}

// mustBuild parses, checks, and builds src under opts, failing the
// test on any error. Uses parser.Parse (not modload) since the matrix
// programs are self-contained and import no stdlib modules.
func mustBuild(t *testing.T, src string, opts BuildOptions) []byte {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := BuildWithOptions(prog, info, opts)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return bin
}

// firstDiff returns the index of the first byte at which a and b
// differ, or the length of the shorter slice if one is a prefix of
// the other. Reported in the determinism failure message to make a
// regression quick to localize.
func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
