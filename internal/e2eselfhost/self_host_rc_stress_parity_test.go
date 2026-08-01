package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcStressParity is an end-to-end codegen gate for the self-host
// x86-64 IR backend on reference-counting / reuse-stressing programs (goal 2).
// Unlike the rcPlan DIFFERENTIAL harness — which diffs the Perceus decision
// TABLES (analysis mirrors) — this compiles each program through the real
// self-host pipeline (asm_ir_run -> assemble -> link -> run) and checks its
// runtime exit code, so it catches an actual miscompile rather than a table
// divergence. It guards the codegen the self-host convergence depends on.
//
// The corpus came out of a focused real-miscompile hunt: each shape stresses a
// distinct RC path (enum rebuilt every loop iteration, closures capturing owned
// arrays, nested owned-payload matches, struct/tuple reuse in loops, owned
// recursion accumulators, the ? operator, own-param donors, for-in owned
// bodies, conditional owned merges). The self-host produced the identical
// result to native on every one — the hunt found no divergence, and this pins
// it. Each `main` returns its i32 result, so the exit code is the observable;
// `want` is the value the native `-interp` oracle produces (validated at
// authoring time — the same golden-exit contract as TestSelfHostParity).
var rcStressProgs = []struct {
	name string
	want int
	src  string
}{
	// enum rebuilt every loop iteration (FBIP-style map, exercises enum reuse)
	{"enum-loop-rebuild", 22, `enum E { A(i32), B(i32) }
function step(e: E): E { return match (e) { A(n) => B(n + 1), B(n) => A(n * 2) }; }
function f(): i32 { var e = A(1); var i = 0; while (i < 6) { e = step(e); i = i + 1; } return match (e) { A(n) => n, B(n) => n }; }
function main(): i32 { return f(); }`},
	// nested closure capturing an owned array, called in a loop
	{"closure-capture-loop", 60, `function f(): i32 { var xs = [10, 20, 30]; var pick = (i: i32) => xs[i]; var s = 0; var i = 0; while (i < 3) { s = s + pick(i); i = i + 1; } return s; }
function main(): i32 { return f(); }`},
	// deep nested match over owned enum payloads
	{"nested-match-owned", 12, `enum T { Leaf(i32), Node(i32, i32) }
function sum(t: T): i32 { return match (t) { Leaf(n) => n, Node(a, b) => a + b }; }
function f(): i32 { var a = Node(3, 4); var b = Leaf(5); var c = Node(sum(a), sum(b)); return sum(c); }
function main(): i32 { return f(); }`},
	// struct field reuse in a loop (donor/recipient every iteration)
	{"struct-reuse-loop", 40, `struct P { x: i32, y: i32 }
function f(): i32 { var s = 0; var i = 0; while (i < 4) { var a = P { x: i, y: i + 1 }; var t = a.x + a.y; var b = P { x: i * 2, y: 3 }; s = s + t + b.x + b.y; i = i + 1; } return s; }
function main(): i32 { return f(); }`},
	// tuple rebuilt (fibonacci-style swap) accumulation
	{"tuple-swap-loop", 55, `function f(): i32 { var t: (i32, i32) = (1, 1); var i = 0; while (i < 8) { t = (t.1, t.0 + t.1); i = i + 1; } return t.1; }
function main(): i32 { return f(); }`},
	// owned string grows via concat accumulation
	{"string-accum", 10, `function f(): i32 { var s = ""; var i = 0; while (i < 5) { s = s + "ab"; i = i + 1; } return s.len(); }
function main(): i32 { return f(); }`},
	// array of arrays with .with mutation on a live receiver
	{"arr-of-arr-with", 14, `function f(): i32 { var m = [[1, 2], [3, 4]]; var r = m[0][0] + m[1][1]; var n = m.with(0, [9, 9]); return r + n[0][0]; }
function main(): i32 { return f(); }`},
	// non-tail recursion with an owned array accumulator
	{"rec-owned-acc", 15, `function build(n: i32): i32[] { if (n <= 0) { return [0]; } var rest = build(n - 1); return rest.with(0, rest[0] + n); }
function f(): i32 { var a = build(5); return a[0]; }
function main(): i32 { return f(); }`},
	// the ? operator threading an owned Option through a helper
	{"option-q-chain", 70, `function first(xs: i32[]): Option[i32] { if (xs.len() > 0) { return Some(xs[0]); } return None; }
function f(xs: i32[]): Option[i32] { var v = first(xs)?; return Some(v * 10); }
function main(): i32 { var xs = [7, 8]; return match (f(xs)) { Some(n) => n, None => 0 }; }`},
	// own-param donor: a fresh construction consumed by an owned parameter
	{"ownparam-donor", 7, `struct H { id: i32, items: i32[] }
function bump(own d: H): H { return H { id: d.id + 1, items: d.items }; }
function f(): i32 { var g = bump(H { id: 1, items: [5, 6] }); return g.id + g.items[0]; }
function main(): i32 { return f(); }`},
	// for-in over an array with owned body locals
	{"forin-owned-body", 18, `function f(): i32 { var xs = [1, 2, 3]; var s = 0; for x in xs { var tmp = [x, x * 2]; s = s + tmp[0] + tmp[1]; } return s; }
function main(): i32 { return f(); }`},
	// match yielding a fresh owned enum, itself consumed by a second match
	{"match-return-enum-2x", 4, `enum E { A(i32[]), B(i32) }
function f(): i32 { var e = A([1, 2, 3]); var g = match (e) { A(xs) => B(xs[0] + xs[2]), B(n) => B(n) }; return match (g) { A(xs) => xs[0], B(n) => n }; }
function main(): i32 { return f(); }`},
	// conditional owned reassignment on divergent branches then a merged use
	{"cond-owned-merge", 23, `function f(c: i32): i32 { var a = [1, 2]; var b = [3, 4]; if (c > 0) { a = a.with(0, 9); } else { b = b.with(1, 9); } return a[0] + b[1]; }
function main(): i32 { return f(1) + f(0); }`},
}

func TestSelfHostRcStressParity(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range rcStressProgs {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "x86-64")
			if len(asm) == 0 {
				t.Fatalf("self-host emitted 0 bytes")
			}
			progBin := buildBin(t, x86gcc, dir, "prog_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(x86runner[0], append(append([]string{}, x86runner[1:]...), progBin)...)
			}
			_, got := runBin(cmd, "")
			if got != tc.want {
				t.Errorf("self-host exit = %d, want %d (native interp oracle)", got, tc.want)
			}
		})
	}
}
