package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var genericRetTypeVarIRCases = []struct {
	name string
	src  string
}{
	{"fold-into-trait-bound", `import "core/cmp";
pub function gfold[T, A](init: A, f: (A, T) => A): A { return init; }
pub function showeq[T: cmp.Eq + cmp.Display](a: T, b: T): i32 {
    if (a == b) { return a.to_string().len(); }
    return b.to_string().len();
}
function main(): i32 {
    let s = gfold(0, (a: i32, x: i32): i32 => { return a + x; });
    return showeq(s, 7);
}`},

	{"typevar-f64-arith", `pub function id[T](x: T): T { return x; }
function main(): i32 {
    let r: f64 = id(2.5) + 0.5;
    if (r == 3.0) { return 1; }
    return 0;
}`},
	{"typevar-f32-arith", `pub function id[T](x: T): T { return x; }
function main(): i32 {
    let r: f32 = id(2.5 as f32) + 1.0 as f32;
    if (r == 3.5) { return 1; }
    return 0;
}`},
	{"typevar-u64-shift", `pub function id[T](x: T): T { return x; }
function main(): i32 {
    let a: u64 = 18000000000000000000;
    let r: u64 = id(a) >> 1;
    if (r == 9000000000000000000) { return 1; }
    return 0;
}`},

	{"shared-typevar-widest-arg", `pub function second[T](a: T, b: T): T { return b; }
function main(): i32 {
    let x: i64 = second(1, 4611686018427387904);
    if (x == 4611686018427387904) { return 7; }
    if (x == 0) { return 1; }
    return 3;
}`},
	{"shared-typevar-tuple-return", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    let q = both(1, 4611686018427387904);
    if (q.1 - q.0 == 4611686018427387903) { return 7; }
    return 3;
}`},
	{"shared-typevar-three-args", `pub function three[T](a: T, b: T, c: T): (T, T, T) { return (a, b, c); }
function main(): i32 {
    let q = three(1, 2, 4611686018427387904);
    if (q.2 - q.0 - q.1 == 4611686018427387901) { return 7; }
    return 3;
}`},
	{"shared-typevar-negative-wide", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    let q = both(0 - 4611686018427387904, 1);
    if (q.1 - q.0 == 4611686018427387905) { return 7; }
    return 3;
}`},
	{"shared-typevar-array-return", `pub function arr[T](a: T, b: T): T[] { return [a, b]; }
function main(): i32 {
    let q = arr(1, 4611686018427387904);
    if (q[1] - q[0] == 4611686018427387903) { return 7; }
    return 3;
}`},
	{"shared-typevar-annotated-tuple", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    let q: (i64, i64) = both(1, 4611686018427387904);
    if (q.1 - q.0 == 4611686018427387903) { return 7; }
    return 3;
}`},
	{"shared-typevar-scrutinee", `pub function pick[T](a: T, b: T): Option[T] { return Some(b); }
function main(): i32 {
    match (pick(1, 4611686018427387904)) {
        Some(v) => { if (v == 4611686018427387904) { return 7; } return 3; },
        None => { return 4; }
    }
}`},
	{"shared-typevar-plain-comparison", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    if (both(1, 4611686018427387904).1 == 4611686018427387904) { return 7; }
    return 3;
}`},
	{"shared-typevar-u64-destination", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    let x: (u64, u64) = both(1, 4611686018427387904);
    if (x.1 / 1000000000000000000 == 4 && x.0 == 1) { return 7; }
    return 3;
}`},
	{"shared-typevar-field-at-destination", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    let z: u64 = both(1, 18000000000000000000).1;
    if (z >> 1 == 9000000000000000000) { return 7; }
    return 3;
}`},
	{"typevar-settles-at-typed-operand", `pub function id[T](x: T): T { return x; }
function main(): i32 {
    let big: u64 = 18000000000000000000;
    if (id(1) < big) { return 7; }
    return 3;
}`},
	{"typevar-shares-a-literal-locals-width", `pub function id[T](x: T): T { return x; }
function main(): i32 {
    let n = 4294967296;
    let k = 1;
    if (k == id(1)) { k = k + n; }
    let w: i64 = k;
    if (w == 4294967297) { return 7; }
    return 3;
}`},
	{"shared-typevar-typed-arg-pins", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    let x: i32 = 1;
    let q = both(x, 2);
    return q.1 + q.0 + 4;
}`},
	{"struct-literal-local-wide-field", `struct Same[T] { a: T, b: T }
function main(): i32 {
    let q = Same { a: 1, b: 4611686018427387904 };
    let r: i64 = q.b;
    let xs = [Same { a: 1, b: 4611686018427387904 }];
    let w = Same[i64] { a: 4294967296, b: 2 };
    if (r == 4611686018427387904 && q.a + 1 == 2 && xs[0].b == r && w.a == 4294967296) { return 7; }
    return 3;
}`},
	{"struct-literal-written-type-variable", `struct T { z: i32 }
struct Box[T] { v: T }
struct Many[T] { xs: T[] }
struct Stack[T] { items: T[] }
function wrap[T](x: T): Box[T] { return Box[T] { v: x }; }
function many[T](x: T): Many[T] { return Many[T] { xs: [x] }; }
function main(): i32 {
    let b = wrap(4294967296);
    let r: i64 = b.v;
    let m = many(4294967296);
    let k: i64 = m.xs[0];
    let st = Stack { items: [1, 4611686018427387904] };
    if (r == 4294967296 && k == r && st.items[1] == 4611686018427387904) { return 7; }
    return 3;
}`},
	{"typed-field-binds-ahead-of-literal", `struct Same[T] { a: T, b: T }
@noinline function pair[T](a: T, b: T): T { return a; }
function main(): i32 {
    let y: i64 = 8589934592;
    let q = Same { a: 3, b: y };
    let r: i64 = pair(1, y);
    if (q.a + q.b == 8589934595 && r == 1) { return 7; }
    return 3;
}`},
	{"struct-literal-array-and-copy-widths", `struct Same[T] { a: T, b: T }
function main(): i32 {
    let y: i64 = 8589934592;
    let xs = [Same { a: 1, b: 2 }, Same { a: 3, b: y }];
    let ws = [Same { a: 3, b: y }, Same { a: 1, b: 2 }];
    let o = Same { a: 1, b: 2 };
    let o2 = o;
    let z: i64 = o2.a * 4294967296;
    if (xs[1].b == y && xs[0].a * 4294967296 == 4294967296 && ws[1].b * 4294967296 == 8589934592 && z == 4294967296) { return 7; }
    return 3;
}`},
	{"struct-literal-local-one-width", `struct Same[T] { a: T, b: T }
struct Stack[T] { items: T[] }
@noinline function take1(x: Same[i64]): i64 { return x.a + x.b + 8589934592; }
@noinline function take_stack(x: Stack[i64]): i64 { return x.items[1] * 4294967296; }
function main(): i32 {
    let q = Same { a: 1, b: 2 };
    let o = Same { a: 5, b: 6 };
    let r: i64 = o.a;
    let st = Stack { items: [1, 2] };
    if (take1(q) == 8589934595 && r * 4294967296 == 21474836480 && take_stack(st) == 8589934592) { return 7; }
    return 3;
}`},
	{"shared-typevar-strings", `pub function second[T](a: T, b: T): T { return b; }
function main(): i32 {
    let s: string = second("ab", "cd");
    if (s == "cd") { return 7; }
    return 3;
}`},
}

func TestSelfHostGenericRetTypeVarIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "grtvlr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range genericRetTypeVarIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "grtv_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "grtv_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
