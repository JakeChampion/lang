package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostSSAEmitArm64 exercises the self-hosted SSA → arm64 backend
// (examples/self_host/ssa_arm64.fern): the ssa_emit_run driver, given
// `-target arm64`, lowers each function to SSA, optimises it, and prints
// AArch64 assembly. This test assembles that output with `gcc -static
// -nostdlib` and runs it (natively on arm64, else under qemu-aarch64),
// asserting the process exit code equals the program's value — arm64
// parity for the SSA backend on the project's DEFAULT target.
//
// The driver itself is built + run via the x86-64 host toolchain (it only
// produces text); the arm64 toolchain assembles and runs the emitted code.
func TestSelfHostSSAEmitArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	gcc, qemu := arm64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "ssa.fern", "ssa_x86.fern", "ssa_arm64.fern", "ssa_emit_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	bin := buildSelfHostBin(t, x86gcc, dir, "ssa_emit_run.fern", "ssa_emit_run")

	// runDriver pipes src into the (x86-64) driver, respecting an exec
	// runner when the host isn't x86-64, and returns its stdout (asm).
	runDriver := func(t *testing.T, src string, args ...string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(x86runner) == 0 {
			cmd = exec.Command(bin, args...)
		} else {
			cmd = exec.Command(x86runner[0], append(append(x86runner[1:], bin), args...)...)
		}
		cmd.Stdin = strings.NewReader(src)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("emit driver failed for %q: %v", src, err)
		}
		return out
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", "function main(): i32 { return 42; }", 42},
		{"arith", "function main(): i32 { return 2 + 3 * 4; }", 14},
		{"locals", "function main(): i32 { var x = 10; var y = x - 3; return y * 2; }", 14},
		{"division", "function main(): i32 { return 84 / 2; }", 42},
		{"modulo", "function main(): i32 { return 23 % 5; }", 3},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }", 10},
		{"shift", "function main(): i32 { return 1 << 4; }", 16},
		{"negative-const", "function main(): i32 { var x = 0 - 7; return x + 100; }", 93},
		{"comparison", "function main(): i32 { return 7 > 3; }", 1},
		{"if-else", "function main(): i32 { var x = 0; if (5 < 10) { x = 7; } else { x = 9; } return x; }", 7},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }", 100},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(3, 4); }", 7},
		{"recursion", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }", 120},
		// break / continue lower to extra loop edges.
		{"break", "function main(): i32 { var i = 0; while (i < 100) { if (i == 5) { break; } i = i + 1; } return i; }", 5},
		{"continue", "function main(): i32 { var i = 0; var s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }", 50},
		// Heap arrays: alloc + element load/store with pointer-width values.
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[1]; }", 20},
		{"arr-computed-index", "function main(): i32 { var a = [3, 7, 11, 15]; var i = 2; return a[i]; }", 11},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }", 75},
		{"arr-two", "function main(): i32 { var a = [1, 2]; var b = [100, 200]; return a[1] + b[0]; }", 102},
		{"arr-len-loop", "function main(): i32 { var a = [4, 8, 12, 16]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }", 40},
		// for-in loops (build_for desugar → counted while): element bind,
		// empty body, nested (inner-only write), break / continue, string
		// bytes, and a for over an array param.
		{"for-sum", "function main(): i32 { var a = [5, 10, 15]; var s = 0; for x in a { s = s + x; } return s; }", 30},
		{"for-empty-body", "function main(): i32 { var a = [5, 10]; for x in a { } return a.len(); }", 2},
		{"for-nested", "function main(): i32 { var rows = [1, 2, 3]; var cols = [10, 20]; var t = 0; for r in rows { for c in cols { t = t + r * c; } } return t; }", 180},
		{"for-break", "function main(): i32 { var a = [1, 2, 3, 4, 5]; var s = 0; for x in a { if (x > 3) { break; } s = s + x; } return s; }", 6},
		{"for-continue", "function main(): i32 { var a = [1, 2, 3, 4]; var s = 0; for x in a { if (x == 2) { continue; } s = s + x; } return s; }", 8},
		{"for-string-bytes", "function main(): i32 { var t = 0; for b in \"AB\" { t = t + b; } return t; }", 131},
		{"for-param", "function sum(a: i32[]): i32 { var s = 0; for x in a { s = s + x; } return s; } function main(): i32 { var xs = [5, 10, 15, 20]; return sum(xs); }", 50},
		// Indexed assignment `arr[i] = v` (→ __set_index → store_elem):
		// constant index, loop-fill, swap, compound, and cross-call mutation
		// through a shared array param.
		{"set-index", "function main(): i32 { var a = [10, 20, 30]; a[1] = 99; return a[0] + a[1] + a[2]; }", 139},
		{"set-index-fill", "function main(): i32 { var a = [0, 0, 0, 0, 0]; var i = 0; while (i < 5) { a[i] = i * i; i = i + 1; } return a[0] + a[1] + a[2] + a[3] + a[4]; }", 30},
		{"set-index-swap", "function main(): i32 { var a = [7, 3]; var t = a[0]; a[0] = a[1]; a[1] = t; return a[0] * 10 + a[1]; }", 37},
		{"set-index-compound", "function main(): i32 { var a = [10, 20, 30]; a[0] += 5; a[1] -= 4; a[2] *= 2; return a[0] + a[1] + a[2]; }", 91},
		{"set-index-param", "function bump(a: i32[]): i32 { a[0] = a[0] + 100; return 0; } function main(): i32 { var xs = [5, 6, 7]; var z = bump(xs); return xs[0] + z; }", 105},
		// i32-keyed maps (Map literal → map_new_i32().set()… → injected
		// __ssa_map_* association-array helpers): get_or / has / len, set
		// (insert + update), loop-build, miss-default, and maps across calls.
		{"map-literal-get", "function main(): i32 { var m = Map { 1: 40, 2: 50, 3: 60 }; return m.get_or(2, 0) + m.get_or(9, 7) + m.len(); }", 60},
		{"map-has", "function main(): i32 { var m = Map { 5: 1, 7: 1 }; var r = 0; if (m.has(5)) { r = r + 10; } if (m.has(6)) { r = r + 100; } if (m.has(7)) { r = r + 1; } return r; }", 11},
		{"map-set-update", "function main(): i32 { var m = Map { 1: 10 }; m.set(2, 20); m.set(1, 99); return m.get_or(1, 0) + m.get_or(2, 0) + m.len(); }", 121},
		{"map-loop-build", "function main(): i32 { var m = Map { 0: 0 }; var i = 1; while (i <= 5) { m.set(i, i * i); i = i + 1; } return m.get_or(3, 0) + m.get_or(5, 0) + m.len(); }", 40},
		{"map-miss-default", "function main(): i32 { var m = Map { 1: 1 }; return m.get_or(42, 7) + m.len(); }", 8},
		{"map-param-get", "function total(m: Map[i32, i32], a: i32, b: i32): i32 { return m.get_or(a, 0) + m.get_or(b, 0); } function main(): i32 { var m = Map { 1: 11, 2: 22, 3: 33 }; return total(m, 1, 3); }", 44},
		{"map-param-len", "function sz(m: Map[i32, i32]): i32 { return m.len(); } function main(): i32 { var m = Map { 1: 1, 2: 2, 3: 3, 4: 4 }; return sz(m) * 10 + m.get_or(2, 0); }", 42},
		// `for (k, v) in m` iteration (entry walk via __ssa_map_key_at/_val_at):
		// key+value sum, values-only, build-then-iterate, break/continue in the
		// body, and iterating a Map passed across a call.
		{"map-iter-sum", "function main(): i32 { var m = Map { 1: 10, 2: 20 }; var s = 0; for (k, v) in m { s = s + k + v; } return s; }", 33},
		{"map-iter-values", "function main(): i32 { var m = Map { 1: 100, 2: 50, 3: 30 }; var s = 0; for (k, v) in m { s = s + v; } return s; }", 180},
		{"map-iter-built", "function main(): i32 { var m = Map { 0: 0 }; var i = 1; while (i <= 4) { m.set(i, i * 10); i = i + 1; } var sum = 0; for (k, v) in m { sum = sum + v; } return sum; }", 100},
		{"map-iter-break-continue", "function main(): i32 { var m = Map { 1: 5, 2: 6, 3: 7, 4: 8 }; var s = 0; for (k, v) in m { if (k == 2) { continue; } if (k == 4) { break; } s = s + v; } return s; }", 12},
		{"map-iter-param", "function sumv(m: Map[i32, i32]): i32 { var s = 0; for (k, v) in m { s = s + v; } return s; } function main(): i32 { var m = Map { 1: 11, 2: 22, 3: 33 }; return sumv(m); }", 66},
		// Passing arrays to functions: pointer-typed (64-bit) params.
		{"arr-param-sum", "function sum(a: i32[]): i32 { var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; } function main(): i32 { var xs = [5, 10, 15, 20]; return sum(xs); }", 50},
		{"arr-param-two", "function dot2(a: i32[], b: i32[]): i32 { return a[0] * b[0] + a[1] * b[1]; } function main(): i32 { var p = [2, 3]; var q = [10, 20]; return dot2(p, q); }", 80},
		// Strings (byte arrays): byte-sum loop and a string param.
		{"str-byte-sum", "function main(): i32 { var s = \"AAA\"; var i = 0; var t = 0; while (i < s.len()) { t = t + s[i]; i = i + 1; } return t; }", 195},
		{"str-param", "function slen(s: string): i32 { return s.len(); } function main(): i32 { var s = \"wxyz\"; return slen(s); }", 4},
		// Returning pointers (arrays / strings) from functions.
		{"return-array", "function make(): i32[] { return [10, 20, 30]; } function main(): i32 { var a = make(); return a[1]; }", 20},
		{"return-string", "function greet(): string { return \"hello\"; } function main(): i32 { var s = greet(); return s.len(); }", 5},
		{"return-array-piped", "function mk(): i32[] { return [5, 10, 15]; } function sum(a: i32[]): i32 { var i = 0; var t = 0; while (i < a.len()) { t = t + a[i]; i = i + 1; } return t; } function main(): i32 { return sum(mk()); }", 30},
		// Structs: i32 fields and pointer (string / array) fields.
		{"struct-sum", "struct Point { x: i32, y: i32 } function main(): i32 { var p = Point { x: 7, y: 9 }; return p.x + p.y; }", 16},
		{"struct-string-field", "struct Named { id: i32, label: string } function main(): i32 { var n = Named { id: 5, label: \"hello\" }; return n.label.len(); }", 5},
		{"struct-array-field", "struct Box { tag: i32, data: i32[] } function main(): i32 { var b = Box { tag: 1, data: [10, 20, 30] }; return b.data[1] + b.tag; }", 21},
		// Struct params / returns (cross-function struct pointers).
		{"struct-param", "struct Point { x: i32, y: i32 } function dist(p: Point): i32 { return p.x + p.y; } function main(): i32 { var p = Point { x: 3, y: 4 }; return dist(p); }", 7},
		{"struct-return", "struct Point { x: i32, y: i32 } function mk(): Point { return Point { x: 5, y: 6 }; } function main(): i32 { var p: Point = mk(); return p.x + p.y; }", 11},
		{"struct-passthrough", "struct P { a: i32, b: i32 } function id(p: P): P { return p; } function main(): i32 { var q = P { a: 8, b: 9 }; var r: P = id(q); return r.b; }", 9},
		// String equality driving dispatch (content comparison via streq).
		{"streq-dispatch", "function kind(s: string): i32 { if (s == \"add\") { return 1; } if (s == \"sub\") { return 2; } return 0; } function main(): i32 { return kind(\"sub\") + 10 * kind(\"add\"); }", 12},
		// enums + match: a variant-dispatching helper (tag + payload fields).
		{"match-area", "struct Circle { r: i32 } struct Square { side: i32 } type Shape = Circle | Square; function area(sh: Shape): i32 { match (sh) { Circle(c) => { return c.r * c.r * 3; }, Square(s) => { return s.side * s.side; } } return 0; } function main(): i32 { var a: Shape = Circle { r: 4 }; var b: Shape = Square { side: 5 }; return area(a) + area(b); }", 73},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runDriver(t, tc.src, "-target", "arm64")
			asmPath := filepath.Join(dir, "prog.s")
			binPath := filepath.Join(dir, "prog")
			if err := os.WriteFile(asmPath, asm, 0o644); err != nil {
				t.Fatalf("write asm: %v", err)
			}
			if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
				t.Fatalf("gcc failed for %q: %v\n%s\n--- asm ---\n%s", tc.src, err, out, asm)
			}
			cmd := runArm64Bin(qemu, binPath)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("emitted program did not exit normally for %q", tc.src)
			}
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("SSA→arm64 of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}
}
