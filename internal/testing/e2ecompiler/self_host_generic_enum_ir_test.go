package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type genericEnumIRCase struct {
	name     string
	src      string
	expected int
}

var genericEnumIRCases = []genericEnumIRCase{
	{"i32_payload", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    let o: Opt[i32] = Sm(5);
    match (o) { Sm(n) => { return n; }, Nn => { return 0; } }
}`, 5},
	{"string_payload_method", `enum Box[T] { V(T) }
function main(): i32 {
    let b: Box[string] = V("hi");
    match (b) { V(s) => { return s.len(); } }
}`, 2},
	{"unit_variant", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    let o: Opt[i32] = Nn;
    match (o) { Sm(n) => { return n; }, Nn => { return 9; } }
}`, 9},
	{"unit_variant_call_arg", `enum Opt[T] { Sm(T), Nn }
function get(o: Opt[i32]): i32 { match (o) { Sm(v) => { return v; }, Nn => { return 42; } } }
function main(): i32 { return get(Nn); }`, 42},
	{"unit_variant_call_arg_mixed", `enum Opt[T] { Sm(T), Nn }
function combine(a: Opt[i32], b: Opt[i32]): i32 {
    let x: i32 = 0;
    match (a) { Sm(v) => { x = v; }, Nn => { x = 1; } }
    match (b) { Sm(w) => { x = x + w; }, Nn => { x = x + 2; } }
    return x;
}
function main(): i32 { return combine(Nn, Sm(40)); }`, 41},
	{"construction_only", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    let o: Opt[i32] = Sm(5);
    return 1;
}`, 1},
	{"two_instantiations", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    let a: Opt[i32] = Sm(7);
    let b: Opt[string] = Sm("hey");
    let x: i32 = 0;
    match (a) { Sm(n) => { x = n; }, Nn => { } }
    let y: i32 = 0;
    match (b) { Sm(s) => { y = s.len(); }, Nn => { } }
    return x + y;
}`, 10},
	{"call_scrutinee", `enum Opt[T] { Sm(T), Nn }
function wrap(v: i32): Opt[i32] { return Sm(v); }
function main(): i32 {
    match (wrap(4)) { Sm(x) => { return x + 1; }, Nn => { return 0; } }
}`, 5},
	{"array_iter", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    let xs: Opt[i32][] = [Sm(1), Sm(2), Nn];
    let s: i32 = 0;
    for o in xs { match (o) { Sm(x) => { s = s + x; }, Nn => { } } }
    return s;
}`, 3},
	{"index_scrutinee", `enum Opt[T] { Sm(T), Nn }
function main(): i32 {
    let xs: Opt[i32][] = [Sm(9), Nn];
    match (xs[0]) { Sm(x) => { return x; }, Nn => { return 0; } }
}`, 9},
	{"string_array_method", `enum Box[T] { V(T) }
function main(): i32 {
    let xs: Box[string][] = [V("ab"), V("cde")];
    let n: i32 = 0;
    for b in xs { match (b) { V(s) => { n = n + s.len(); } } }
    return n;
}`, 5},
	{"multiparam_i32", `enum Pair[K, V] { P(K, V) }
function main(): i32 {
    let p: Pair[i32, i32] = P(3, 4);
    match (p) { P(a, b) => { return a + b; } }
}`, 7},
	{"multiparam_mixed", `enum Pair[K, V] { P(K, V), Z }
function main(): i32 {
    let p: Pair[i32, string] = P(5, "hi");
    match (p) { P(a, b) => { return a + b.len(); }, Z => { return 0; } }
}`, 7},
	{"method_per_instantiation", `enum Opt[T] { Sm(T), Nn }
function (o: Opt[T]) or_else(d: T): T { match (o) { Sm(x) => { return x; }, Nn => { return d; } } }
function main(): i32 {
    let a: Opt[i32] = Sm(5);
    let b: Opt[i32] = Nn;
    let c: Opt[string] = Sm("abc");
    return a.or_else(0) + b.or_else(7) + c.or_else("").len();
}`, 15},
	{"method_own_type_param", `enum Box[T, E] { Full(T), Blank(E) }
function (b: Box[T, E]) pair[U](other: Box[U, E]): Box[U, E] { return other; }
function take(b: Box[i32, string]): i32 { match (b) { Full(n) => { return n; }, Blank(s) => { return s.len(); } } }
function main(): i32 {
    let b: Box[i32, string] = Full(5);
    return take(b.pair(Full(9))) + take(b.pair(Full(2)));
}`, 11},
	{"method_builds_variant", `enum Tree[K] { Tip, Bin(Tree[K], K, i32) }
function (t: Tree[K]) size(): i32 { match (t) { Tip => { return 0; }, Bin(_, _, n) => { return n; } } }
function (t: Tree[K]) put(k: K): Tree[K] { return Bin(t, k, t.size() + 1); }
function main(): i32 {
    let a: Tree[i32] = Tip;
    let b: Tree[string] = Tip;
    return a.put(1).size() + b.put("x").put("y").size();
}`, 3},
	{"method_bare_variant_to_generic", `enum Tree[K] { Tip, Bin(Tree[K], K, i32) }
function (t: Tree[K]) size(): i32 { match (t) { Tip => { return 0; }, Bin(_, _, n) => { return n; } } }
function leaf[K](l: Tree[K], k: K): Tree[K] { return Bin(l, k, l.size() + 1); }
function (t: Tree[K]) grow(k: K): Tree[K] { return leaf(Tip, k); }
function main(): i32 {
    let a: Tree[i32] = Tip;
    return a.grow(7).size();
}`, 1},
	{"method_matches_generic_call", `enum Tree[K] { Tip, Bin(Tree[K], K, i32) }
function find[K](t: Tree[K], k: K): Tree[K] { return t; }
function (t: Tree[K]) has(k: K): i32 { match (find(t, k)) { Tip => { return 0; }, Bin(_, _, n) => { return n; } } }
function main(): i32 {
    let a: Tree[i32] = Bin(Tip, 1, 4);
    return a.has(1);
}`, 4},
}

func TestSelfHostGenericEnumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range genericEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("driver produced no asm")
			}
			progBin := buildBin(t, gcc, dir, "generic_enum_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("generic-enum %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostGenericEnumWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range genericEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.expected {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.expected, stderr)
				}
			}
		})
	}
}
