package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A call whose generic callee returns an UNBOUNDED type parameter — `fold[T, A,
// I]: A` (core/iter.fold), where `A` is erased from `type_params`, so the
// monomorphiser never substitutes it — used to make the self-host `mono_infer`
// report the bare type variable "A" as the call's type. Binding `var s =
// iter.fold(..)` to "A" then keyed a spurious clone of any trait-bounded generic
// `s` flowed into (`assert_eq[T: Eq + Display](s, 6)` -> `assert_eq__A`), whose
// `A.eq` / `A.to_string` can't resolve — dragging the whole module to the AST
// emitter (e.g. examples/tests/iter_test.fern). mono_infer now reports a bare
// type variable as "unknown" instead, so the other concrete-literal argument
// binds the call at `i32` and the module routes IR.
//
// `gfold` reproduces the exact shape (return type = an unbounded type param,
// with the other type var only in a closure param so the key can't be inferred
// from the call), and `showeq` reproduces assert_eq's `Eq + Display` trait-method
// body — the combination that bailed pre-fix.
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
    var s = gfold(0, (a: i32, x: i32): i32 => { return a + x; });
    return showeq(s, 7);
}`},

	// A generic identity `id[T](x: T): T` whose return mirrors argument 0 records
	// a "name|$arg0" entry in str_ret_fns; infer_expr_width and str-tracking
	// already consult it, but the FLOAT / UNSIGNED value predicates did not — so
	// a numeric result chained directly on the call (`id(2.5) + 0.5`) mis-lowered:
	// f64/f32 as an integer op on the double's bits, u64 as a signed shift. The
	// argref arms added to expr_is_f64 / expr_is_f32 / expr_is_u64 recover it.
	// f64: id(2.5) + 0.5 == 3.0 (float add, not an integer op on the bits).
	{"typevar-f64-arith", `pub function id[T](x: T): T { return x; }
function main(): i32 {
    var r: f64 = id(2.5) + 0.5;
    if (r == 3.0) { return 1; }
    return 0;
}`},
	// f32 (uses the f64 twin): id(2.5 as f32) + 1.0 as f32 == 3.5.
	{"typevar-f32-arith", `pub function id[T](x: T): T { return x; }
function main(): i32 {
    var r: f32 = id(2.5 as f32) + 1.0 as f32;
    if (r == 3.5) { return 1; }
    return 0;
}`},
	// u64 (bit 63 set): id(big) >> 1 needs the UNSIGNED shift, else it diverges.
	{"typevar-u64-shift", `pub function id[T](x: T): T { return x; }
function main(): i32 {
    var a: u64 = 18000000000000000000;
    var r: u64 = id(a) >> 1;
    if (r == 9000000000000000000) { return 1; }
    return 0;
}`},

	// TWO parameters declaring one type variable bound it from whichever
	// argument the return-inference loop reached first, so `second(1, 2^62)`
	// typed the call at the i32 reading of `1` and the wide argument came back
	// truncated — refused here as "cannot assign i32 to i64", silently wrong
	// wherever the destination was not annotated (#8722 part 4). Native settles
	// the same call at i64. Every argument at the shared variable is now read
	// and the readings settle at the wider integer one.
	{"shared-typevar-widest-arg", `pub function second[T](a: T, b: T): T { return b; }
function main(): i32 {
    var x: i64 = second(1, 4611686018427387904);
    if (x == 4611686018427387904) { return 7; }
    if (x == 0) { return 1; }
    return 3;
}`},
	// The CONTAINER spelling: a tuple return reads its element type off the
	// first argument that declares the variable, so the call site's literal
	// arguments are settled at the widest reading too, not only the checker's
	// answer (#8722 part 4).
	{"shared-typevar-tuple-return", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    var q = both(1, 4611686018427387904);
    if (q.1 - q.0 == 4611686018427387903) { return 7; }
    return 3;
}`},
	{"shared-typevar-three-args", `pub function three[T](a: T, b: T, c: T): (T, T, T) { return (a, b, c); }
function main(): i32 {
    var q = three(1, 2, 4611686018427387904);
    if (q.2 - q.0 - q.1 == 4611686018427387901) { return 7; }
    return 3;
}`},
	{"shared-typevar-negative-wide", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    var q = both(0 - 4611686018427387904, 1);
    if (q.1 - q.0 == 4611686018427387905) { return 7; }
    return 3;
}`},
	{"shared-typevar-array-return", `pub function arr[T](a: T, b: T): T[] { return [a, b]; }
function main(): i32 {
    var q = arr(1, 4611686018427387904);
    if (q[1] - q[0] == 4611686018427387903) { return 7; }
    return 3;
}`},
	{"shared-typevar-annotated-tuple", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    var q: (i64, i64) = both(1, 4611686018427387904);
    if (q.1 - q.0 == 4611686018427387903) { return 7; }
    return 3;
}`},
	// The other positions that read a literal-bound variable (#10176): a
	// scrutinee and a plain comparison take the widest reading as the
	// unannotated `var` does, and a destination settles the variable at its
	// own width — `u64`, which no literal reading gives, and through a field
	// read of the result.
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
    var x: (u64, u64) = both(1, 4611686018427387904);
    if (x.1 / 1000000000000000000 == 4 && x.0 == 1) { return 7; }
    return 3;
}`},
	{"shared-typevar-field-at-destination", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    var z: u64 = both(1, 18000000000000000000).1;
    if (z >> 1 == 9000000000000000000) { return 7; }
    return 3;
}`},
	{"typevar-settles-at-typed-operand", `pub function id[T](x: T): T { return x; }
function main(): i32 {
    var big: u64 = 18000000000000000000;
    if (id(1) < big) { return 7; }
    return 3;
}`},
	{"typevar-shares-a-literal-locals-width", `pub function id[T](x: T): T { return x; }
function main(): i32 {
    var n = 4294967296;
    var k = 1;
    if (k == id(1)) { k = k + n; }
    var w: i64 = k;
    if (w == 4294967297) { return 7; }
    return 3;
}`},
	// A typed argument pins the variable: only a variable bound by literals
	// alone takes the widest reading.
	{"shared-typevar-typed-arg-pins", `pub function both[T](a: T, b: T): (T, T) { return (a, b); }
function main(): i32 {
    var x: i32 = 1;
    var q = both(x, 2);
    return q.1 + q.0 + 4;
}`},
	// A generic struct literal whose type argument only literals bind takes
	// i64 when one of them has no i32 reading (#10453), so neither field of
	// the local is truncated — inside an array literal too. A written
	// instantiation is what the literal is, and is not widened.
	{"struct-literal-local-wide-field", `struct Same[T] { a: T, b: T }
function main(): i32 {
    var q = Same { a: 1, b: 4611686018427387904 };
    var r: i64 = q.b;
    var xs = [Same { a: 1, b: 4611686018427387904 }];
    var w = Same[i64] { a: 4294967296, b: 2 };
    if (r == 4611686018427387904 && q.a + 1 == 2 && xs[0].b == r && w.a == 4294967296) { return 7; }
    return 3;
}`},
	// A written instantiation that spells the enclosing function's type
	// variable is that variable, never a declared type of the same name, and
	// a field holding the parameter inside an array widens it too (#10453).
	{"struct-literal-written-type-variable", `struct T { z: i32 }
struct Box[T] { v: T }
struct Many[T] { xs: T[] }
struct Stack[T] { items: T[] }
function wrap[T](x: T): Box[T] { return Box[T] { v: x }; }
function many[T](x: T): Many[T] { return Many[T] { xs: [x] }; }
function main(): i32 {
    var b = wrap(4294967296);
    var r: i64 = b.v;
    var m = many(4294967296);
    var k: i64 = m.xs[0];
    var st = Stack { items: [1, 4611686018427387904] };
    if (r == 4294967296 && k == r && st.items[1] == 4611686018427387904) { return 7; }
    return 3;
}`},
	// A typed i64 binds T ahead of the untyped literal written before it,
	// in a struct literal and a generic call (#10453).
	{"typed-field-binds-ahead-of-literal", `struct Same[T] { a: T, b: T }
@noinline function pair[T](a: T, b: T): T { return a; }
function main(): i32 {
    var y: i64 = 8589934592;
    var q = Same { a: 3, b: y };
    var r: i64 = pair(1, y);
    if (q.a + q.b == 8589934595 && r == 1) { return 7; }
    return 3;
}`},
	// The widening reads INTEGERS only: wider_int keeps the first reading when
	// either side is not one, so a shared variable bound by two strings types
	// exactly as it did.
	{"shared-typevar-strings", `pub function second[T](a: T, b: T): T { return b; }
function main(): i32 {
    var s: string = second("ab", "cd");
    if (s == "cd") { return 7; }
    return 3;
}`},
}

// TestSelfHostGenericRetTypeVarIR — a generic call returning an (erased)
// unbounded type param no longer keys a spurious bare-type-variable clone, so a
// program that funnels its result into a trait-bounded generic routes the IR
// path and runs correctly. Cross-checked against the interpreter oracle.
func TestSelfHostGenericRetTypeVarIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "grtvlr")
	root, err := filepath.Abs("../../internal/stdlib")
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
