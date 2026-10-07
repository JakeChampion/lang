package e2ecompiler

import "testing"

// structUpdateIRCases pin functional struct-update expressions
// (`T { ...base, field: v }`) on the self-host IR path on x86-64 + wasm: the
// overrides are lowered and the remaining declared fields copied from the base,
// in declaration order. Two other tests (self_host_struct_update_test.go,
// self_host_functional_update_test.go) also exercise struct-update; these
// cases check each exit code against the interp oracle, mirroring
// self_host_block_expr_ir_test.go.
//
// Every struct is all-i32 or i32+string, and every result <= 126
// (wasmtime exit-code truncation, cf. #2908).
var structUpdateIRCases = []struct {
	name string
	main string
}{
	// Single field override; the other two fields copy from the base.
	// q = {1, 20, 3} -> 24.
	{"single-override", `struct P { x: i32, y: i32, z: i32 }
function main(): i32 { let p: P = P { x: 1, y: 2, z: 3 }; let q: P = P { ...p, y: 20 }; return q.x + q.y + q.z; }`},
	// A STRING field copies through the update unchanged while an i32 field is
	// overridden — exercises the leaksafe non-i32 copy path. t.name == "hi" -> 9.
	{"string-field-copy", `struct S { name: string, n: i32 }
function main(): i32 { let s: S = S { name: "hi", n: 3 }; let t: S = S { ...s, n: 9 }; if (t.name == "hi") { return t.n; } return 0; }`},
	// Update in return position with a NON-ident base computation (p.b + 100),
	// spilling the base once. q = {5, 106} -> 111.
	{"update-in-return", `struct P { a: i32, b: i32 }
function bump(p: P): P { return P { ...p, b: p.b + 100 }; }
function main(): i32 { let p: P = P { a: 5, b: 6 }; let q: P = bump(p); return q.a + q.b; }`},
	// Functional update threaded through a loop (the immutable-counter idiom).
	// inc 5 times from 0 -> 5.
	{"functional-loop", `struct C { n: i32 }
function inc(c: C): C { return C { ...c, n: c.n + 1 }; }
function main(): i32 { let c: C = C { n: 0 }; let i: i32 = 0; while (i < 5) { c = inc(c); i = i + 1; } return c.n; }`},
}

// TestSelfHostStructUpdateIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostStructUpdateIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range structUpdateIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
