package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostSSARoundTrip exercises the self-hosted SSA layer
// (examples/self_host/ssa.fern): the ssa_run driver parses a program,
// lowers `main` to SSA via build_func, evaluates it with the SSA
// interpreter, and returns the result as its exit code. Each case asserts
// AST → SSA → eval reproduces the program's value — proving the IR + the
// AST→SSA builder are semantics-preserving. The subset covers straight-line
// i32 (params/locals/arith/cmp/bitwise/calls), if/else (CFG + merge phi),
// and while loops (loop-header phi + back-edge). Constructs outside the
// subset (e.g. float literals) make build_func bail (exit 200).
//
// Slice 4 adds the optimisation passes: every program is also run with
// -opt and must evaluate to the same value (copy-propagation +
// constant-folding + DCE are semantics-preserving), and a shrinks-ir
// sub-test asserts the passes collapse foldable programs to far fewer
// instructions via the driver's -count mode.
//
// The driver is built natively via the Go x86-64 backend and fed each
// program on stdin; its exit code is the SSA-computed result.
func TestSelfHostSSARoundTrip(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ssa_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ssa_run.fern", "ssa_run")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"const", "function main(): i32 { return 42; }", 42},
		// Hex integer literals (#4341): the SSA builder folded a number literal's
		// text into the const_int imm via the decimal-only util.digits_to_i32,
		// which stopped at the `x` and emitted imm 0. Now via util.lit_to_i32.
		{"hex-const", "function main(): i32 { return 0x1F; }", 31},
		{"hex-arith", "function main(): i32 { return 0x10 + 1; }", 17},
		{"arith-precedence", "function main(): i32 { return 2 + 3 * 4; }", 14},
		{"parens", "function main(): i32 { return (1 + 2) * 3; }", 9},
		{"locals", "function main(): i32 { var x = 2 + 3 * 4; var y = x - 5; return y * 2; }", 18},
		{"reassign", "function main(): i32 { var x = 5; x = x + 3; return x; }", 8},
		{"nested-locals", "function main(): i32 { var a = 3; var b = a * a; var c = b + a; return c; }", 12},
		{"modulo", "function main(): i32 { return 23 % 5; }", 3},
		{"division", "function main(): i32 { return 84 / 2; }", 42},
		{"comparison", "function main(): i32 { return 5 < 10; }", 1},
		{"bitwise", "function main(): i32 { return (6 & 3) | 8; }", 10},
		{"shift", "function main(): i32 { return 1 << 4; }", 16},
		// Control flow: if / else lower to a CFG with phi at the merge.
		{"if-taken", "function main(): i32 { var x = 1; if (5 < 10) { x = 7; } return x; }", 7},
		{"if-not-taken", "function main(): i32 { var x = 1; if (10 < 5) { x = 7; } return x; }", 1},
		{"if-else-then", "function main(): i32 { var x = 0; if (true) { x = 3; } else { x = 9; } return x; }", 3},
		{"if-else-else", "function main(): i32 { var x = 0; if (false) { x = 3; } else { x = 9; } return x; }", 9},
		{"phi-plus", "function main(): i32 { var x = 0; if (true) { x = 10; } else { x = 20; } return x + 1; }", 11},
		{"early-return", "function main(): i32 { var x = 5; if (x > 3) { return 100; } return x; }", 100},
		{"no-early-return", "function main(): i32 { var x = 2; if (x > 3) { return 100; } return x; }", 2},
		{"nested-if", "function main(): i32 { var x = 5; if (x > 0) { if (x > 3) { x = 100; } else { x = 50; } } return x; }", 100},
		{"two-var-phi", "function main(): i32 { var a = 1; var b = 2; if (false) { a = 10; b = 20; } else { a = 30; } return a + b; }", 32},
		// Constant conditions: branch_simplify folds brif→br and prunes the
		// dead arm; results must be unchanged with and without -opt.
		{"const-cond-true", "function main(): i32 { var x = 0; if (true) { x = 10; } else { x = 20; } return x + 1; }", 11},
		{"const-cond-false", "function main(): i32 { var x = 0; if (false) { x = 10; } else { x = 20; } return x + 1; }", 21},
		{"const-cond-no-else", "function main(): i32 { var x = 5; if (false) { x = 99; } return x; }", 5},
		{"const-cond-nested", "function main(): i32 { var x = 1; if (true) { if (false) { x = 2; } else { x = 3; } } return x; }", 3},
		// While loops: loop-header phi + back-edge.
		{"while-count", "function main(): i32 { var i = 0; while (i < 3) { i = i + 1; } return i; }", 3},
		{"while-sum", "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }", 15},
		{"while-factorial", "function main(): i32 { var i = 1; var f = 1; while (i <= 5) { f = f * i; i = i + 1; } return f; }", 120},
		{"while-zero-iters", "function main(): i32 { var i = 10; while (i < 5) { i = i + 100; } return i; }", 10},
		{"while-invariant-read", "function main(): i32 { var n = 7; var i = 0; var s = 0; while (i < n) { s = s + n; i = i + 1; } return s; }", 49},
		{"if-in-loop", "function main(): i32 { var i = 0; var c = 0; while (i < 10) { if (i > 4) { c = c + 1; } i = i + 1; } return c; }", 5},
		{"nested-loop", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 3) { t = t + 1; j = j + 1; } i = i + 1; } return t; }", 9},
		// CSE: duplicated subexpressions over non-constant (loop-phi) values
		// must still evaluate correctly once de-duplicated.
		{"cse-dup-loop", "function main(): i32 { var i = 4; var t = 0; while (i > 0) { t = (i * i) + (i * i); i = i - 1; } return t; }", 2},
		{"cse-commutative-loop", "function main(): i32 { var i = 3; var t = 0; while (i > 0) { t = t + ((i + 1) + (1 + i)); i = i - 1; } return t; }", 18},
		// break / continue: extra edges into the loop exit / header.
		{"break-early", "function main(): i32 { var i = 0; while (i < 100) { if (i == 5) { break; } i = i + 1; } return i; }", 5},
		{"continue-skip", "function main(): i32 { var i = 0; var s = 0; while (i < 10) { i = i + 1; if (i == 5) { continue; } s = s + i; } return s; }", 50},
		{"break-with-value", "function main(): i32 { var i = 0; var found = 0; while (i < 20) { if (i * i > 30) { found = i; break; } i = i + 1; } return found; }", 6},
		{"continue-and-break", "function main(): i32 { var i = 0; var s = 0; while (i < 100) { i = i + 1; if (i == 3) { continue; } if (i == 7) { break; } s = s + i; } return s; }", 18},
		{"nested-break-inner", "function main(): i32 { var i = 0; var t = 0; while (i < 3) { var j = 0; while (j < 10) { if (j == 2) { break; } t = t + 1; j = j + 1; } i = i + 1; } return t; }", 6},
		// Algebraic identities on non-constant (loop) values — must not
		// change results when simplified.
		{"alg-mul-one", "function main(): i32 { var i = 0; var s = 0; while (i < 5) { s = s + (i * 1); i = i + 1; } return s; }", 10},
		{"alg-add-zero", "function main(): i32 { var i = 0; var s = 0; while (i < 5) { s = (s + 0) + i; i = i + 1; } return s; }", 10},
		{"alg-mul-zero", "function main(): i32 { var i = 0; var s = 0; while (i < 5) { s = s + (i * 0); i = i + 1; } return s; }", 0},
		{"alg-or-shift-zero", "function main(): i32 { var i = 0; var s = 0; while (i < 5) { s = (s | 0) + (i << 0); i = i + 1; } return s; }", 10},
		// Heap arrays (read-only): literal construction + index load, via the
		// interpreter's heap model. (The backends don't lower these yet, so
		// -opt must still preserve semantics.)
		{"arr-index", "function main(): i32 { var a = [10, 20, 30]; return a[1]; }", 20},
		{"arr-sum-ends", "function main(): i32 { var a = [10, 20, 30]; return a[0] + a[2]; }", 40},
		{"arr-computed-index", "function main(): i32 { var a = [3, 7, 11, 15]; var i = 2; return a[i]; }", 11},
		{"arr-loop-sum", "function main(): i32 { var a = [5, 10, 15, 20, 25]; var i = 0; var s = 0; while (i < 5) { s = s + a[i]; i = i + 1; } return s; }", 75},
		{"arr-expr-elements", "function main(): i32 { var x = 4; var a = [x, x * 2, x + 100]; return a[1] + a[2]; }", 112},
		{"arr-two", "function main(): i32 { var a = [1, 2]; var b = [100, 200]; return a[1] + b[0]; }", 102},
		// .len() reads the array's length prefix.
		{"arr-len", "function main(): i32 { var a = [10, 20, 30]; return a.len(); }", 3},
		{"arr-index-plus-len", "function main(): i32 { var a = [10, 20, 30]; return a[2] + a.len(); }", 33},
		{"arr-len-loop", "function main(): i32 { var a = [4, 8, 12, 16]; var i = 0; var s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }", 40},
		// Regression: two identical `i + 1` subexpressions (the a[i] index and
		// the loop increment) collapse under CSE — the loop-header phi's
		// back-edge operand must be rewritten to the survivor (cross-block).
		{"cse-dup-increment", "function main(): i32 { var i = 0; var s = 0; while (i < 5) { s = s + (i + 1); i = i + 1; } return s; }", 15},
		// `for x in arr { body }` desugars to a counted while over the array
		// (build_for), reusing build_while — the loop binds x to each element.
		{"for-sum", "function main(): i32 { var a = [5, 10, 15]; var s = 0; for x in a { s = s + x; } return s; }", 30},
		{"for-element-bind", "function main(): i32 { var a = [3, 7]; var last = 0; for x in a { last = x; } return last; }", 7},
		{"for-empty-body", "function main(): i32 { var a = [5, 10]; for x in a { } return a.len(); }", 2},
		{"for-single-elem", "function main(): i32 { var a = [42]; var n = 0; for x in a { n = n + 1; } return n; }", 1},
		{"for-count", "function main(): i32 { var a = [9, 9, 9, 9]; var n = 0; for x in a { n = n + 1; } return n; }", 4},
		{"for-len-invariant", "function main(): i32 { var a = [2, 4, 6]; var s = 0; for x in a { s = s + a.len(); } return s; }", 9},
		// A nested for-loop: the OUTER loop must phi a variable written only
		// inside the inner loop (collect_assigned recurses into StmtFor).
		{"for-nested", "function main(): i32 { var rows = [1, 2, 3]; var cols = [10, 20]; var t = 0; for r in rows { for c in cols { t = t + r * c; } } return t; }", 180},
		// break / continue inside a for-loop. continue jumps to the while
		// header, so the index advance lives at the TOP of the body (sentinel
		// start) — a bottom advance would be skipped and spin forever.
		{"for-break", "function main(): i32 { var a = [1, 2, 3, 4, 5]; var s = 0; for x in a { if (x > 3) { break; } s = s + x; } return s; }", 6},
		{"for-continue", "function main(): i32 { var a = [1, 2, 3, 4]; var s = 0; for x in a { if (x == 2) { continue; } s = s + x; } return s; }", 8},
		// for over a string iterates its bytes.
		{"for-string-bytes", "function main(): i32 { var t = 0; for b in \"AB\" { t = t + b; } return t; }", 131},
		// for over a struct's array field.
		{"for-struct-array-field", "struct Box { tag: i32, data: i32[] } function main(): i32 { var b = Box { tag: 100, data: [1, 2, 3] }; var s = 0; for x in b.data { s = s + x; } return s; }", 6},
		// Indexed assignment `arr[i] = v` — the parser desugars it to the
		// builtin __set_index(arr, idx, val), lowered to a store_elem at
		// idx+1 (the foundation the map series builds on: an i32[] buffer
		// mutated in place).
		{"set-index", "function main(): i32 { var a = [10, 20, 30]; a[1] = 99; return a[0] + a[1] + a[2]; }", 139},
		{"set-index-fill", "function main(): i32 { var a = [0, 0, 0, 0, 0]; var i = 0; while (i < 5) { a[i] = i * i; i = i + 1; } return a[0] + a[1] + a[2] + a[3] + a[4]; }", 30},
		{"set-index-computed-rhs", "function main(): i32 { var a = [1, 2, 3]; a[0] = a[0] + 10; a[2] = a[1] * 5; return a[0] + a[2]; }", 21},
		{"set-index-swap", "function main(): i32 { var a = [7, 3]; var t = a[0]; a[0] = a[1]; a[1] = t; return a[0] * 10 + a[1]; }", 37},
		{"set-index-compound", "function main(): i32 { var a = [10, 20, 30]; a[0] += 5; a[1] -= 4; a[2] *= 2; return a[0] + a[1] + a[2]; }", 91},
		{"set-index-in-for", "function main(): i32 { var a = [5, 5, 5, 5]; for x in a { a[0] = a[0] + 1; } return a[0]; }", 9},
		{"set-index-running-sum", "function main(): i32 { var a = [0, 0, 0, 0]; var i = 0; var run = 0; while (i < 4) { run = run + (i + 1); a[i] = run; i = i + 1; } return a[3]; }", 10},
		// Typed-array element access: indexing a struct / string array recovers
		// the element type (type_of_expr's ExprIndex arm), so `a[i].field`
		// resolves a field offset and `a[i] + …` / `a[i] == …` dispatch as
		// string ops rather than i32.
		{"array-of-struct-field", "struct P { x: i32, y: i32 } function main(): i32 { var a = [P { x: 1, y: 2 }, P { x: 10, y: 20 }]; return a[0].x + a[1].y; }", 21},
		{"array-of-struct-iter", "struct P { x: i32, y: i32 } function main(): i32 { var a = [P { x: 1, y: 2 }, P { x: 3, y: 4 }, P { x: 5, y: 6 }]; var t = 0; for p in a { t = t + p.x + p.y; } return t; }", 21},
		{"array-of-struct-string-field", "struct Named { id: i32, label: string } function main(): i32 { var a = [Named { id: 1, label: \"hello\" }, Named { id: 2, label: \"hi\" }]; return a[0].label.len() + a[1].label.len() + a[1].id; }", 9},
		{"string-array-concat", "function main(): i32 { var a = [\"foo\", \"bar\"]; var c = a[0] + a[1]; return c.len(); }", 6},
		{"string-array-eq", "function main(): i32 { var a = [\"add\", \"sub\"]; if (a[1] == \"sub\") { return 7; } return 0; }", 7},
		// Tuples: a fixed-arity heap box (element i at offset i); `t.0` / `t.1`
		// are positional indexes (a numeric field name is unambiguously a
		// tuple index, since struct fields are never all-digits).
		{"tuple-pair", "function main(): i32 { var t = (3, 4); return t.0 + t.1; }", 7},
		{"tuple-triple", "function main(): i32 { var t = (10, 20, 30); return t.0 * 100 + t.1 * 10 + t.2; }", 1230 % 256},
		{"tuple-in-loop", "function main(): i32 { var s = 0; var i = 0; while (i < 3) { var t = (i, i * i); s = s + t.0 + t.1; i = i + 1; } return s; }", 8},
		{"tuple-string-elem", "function main(): i32 { var t = (42, \"hello\"); return t.0 + t.1.len(); }", 47},
		{"tuple-two", "function main(): i32 { var a = (1, 2); var b = (3, 4); return a.0 + a.1 + b.0 + b.1; }", 10},
		// Tuple destructuring `var (a, b) = t` binds each name to the tuple's
		// positional element (the parser joins the names as "a,b").
		{"tuple-destructure", "function main(): i32 { var (a, b) = (5, 6); return a + b; }", 11},
		{"tuple-destructure-var", "function main(): i32 { var t = (3, 9); var (x, y) = t; return x + y + t.0; }", 15},
		// __new_array(n): the dynamic-allocation primitive — a length-prefixed
		// i32 array of n zeroed elements at a RUNTIME size (the alloc op carries
		// its size in args[0] rather than the imm). Filled / read via the
		// indexed-assignment + index paths. The foundation push / slice build on.
		{"new-array-fixed", "function main(): i32 { var b = __new_array(3); b[0] = 10; b[1] = 20; b[2] = 30; return b[0] + b[1] + b[2] + b.len(); }", 63},
		{"new-array-dynamic", "function main(): i32 { var n = 5; var b = __new_array(n); var i = 0; while (i < n) { b[i] = i * i; i = i + 1; } var s = 0; var j = 0; while (j < b.len()) { s = s + b[j]; j = j + 1; } return s; }", 30},
		// Strings: a byte sequence lowered to the same length-prefixed array.
		{"str-len", "function main(): i32 { var s = \"hello\"; return s.len(); }", 5},
		{"str-index", "function main(): i32 { var s = \"ABC\"; return s[0] as i32; }", 65},
		{"str-index-2", "function main(): i32 { var s = \"hi\"; return s[1] as i32; }", 105},
		{"str-byte-sum", "function main(): i32 { var s = \"AAA\"; var i = 0; var t = 0; while (i < s.len()) { t = t + (s[i] as i32); i = i + 1; } return t; }", 195},
		{"str-empty", "function main(): i32 { var s = \"\"; return s.len(); }", 0},
		// Structs: heap-allocated, fields at declaration-order word offsets.
		{"struct-field", "struct Point { x: i32, y: i32 } function main(): i32 { var p = Point { x: 7, y: 9 }; return p.x; }", 7},
		{"struct-sum", "struct Point { x: i32, y: i32 } function main(): i32 { var p = Point { x: 7, y: 9 }; return p.x + p.y; }", 16},
		{"struct-field-order", "struct R { a: i32, b: i32, c: i32 } function main(): i32 { var r = R { c: 30, a: 10, b: 20 }; return r.b; }", 20},
		// Pointer fields (string / array) — 64-bit word slots.
		{"struct-string-field", "struct Named { id: i32, label: string } function main(): i32 { var n = Named { id: 5, label: \"hello\" }; return n.label.len(); }", 5},
		{"struct-array-field", "struct Box { tag: i32, data: i32[] } function main(): i32 { var b = Box { tag: 1, data: [10, 20, 30] }; return b.data[1] + b.tag; }", 21},
		// enums (unions of struct variants) + match: dispatch on the variant
		// tag, bind the payload, access its fields.
		{"match-variant-a", "struct A { x: i32 } struct B { x: i32 } type T = A | B; function main(): i32 { var v = A { x: 7 }; var out = 0; match (v) { A(a) => { out = a.x; }, B(b) => { out = b.x + 1; } } return out; }", 7},
		{"match-variant-b", "struct A { x: i32 } struct B { x: i32 } type T = A | B; function main(): i32 { var v = B { x: 7 }; var out = 0; match (v) { A(a) => { out = a.x; }, B(b) => { out = b.x + 1; } } return out; }", 8},
		{"match-wildcard", "struct A { x: i32 } struct B { x: i32 } type T = A | B; function main(): i32 { var v = B { x: 7 }; var out = 0; match (v) { A(a) => { out = 1; }, _ => { out = 99; } } return out; }", 99},
		// All-paths-return: every branch returns, leaving a dead merge.
		{"all-return-if", "function main(): i32 { var x = 7; if (x > 3) { return 100; } else { return 200; } }", 100},
		{"all-return-nested", "function main(): i32 { var n = 0; if (n < 0) { return 1; } else { if (n == 0) { return 42; } else { return 3; } } }", 42},
		// Regression: a no-`else` if whose body modifies a variable, taking
		// the FALL-THROUGH path — the merge phi's fall-through operand must
		// flow correctly through the backends' phi-deconstruction.
		{"no-else-fallthrough", "function main(): i32 { var x = 5; if (x > 100) { x = 1; } return x; }", 5},
		{"no-else-string-fallthrough", "function main(): i32 { var s = \"ab\"; if (s.len() > 100) { s = \"zzz\"; } return s.len(); }", 2},
		// String equality (`==` / `!=` compare content, not pointers).
		{"streq-same", "function main(): i32 { var a = \"hello\"; var b = \"hello\"; if (a == b) { return 1; } return 0; }", 1},
		{"streq-diff", "function main(): i32 { var a = \"hello\"; var b = \"world\"; if (a == b) { return 1; } return 0; }", 0},
		{"streq-difflen", "function main(): i32 { var a = \"hi\"; var b = \"hii\"; if (a == b) { return 1; } return 0; }", 0},
		{"streq-literal", "function main(): i32 { var s = \"abc\"; if (s == \"abc\") { return 9; } return 0; }", 9},
		{"strne", "function main(): i32 { var a = \"x\"; var b = \"y\"; if (a != b) { return 1; } return 0; }", 1},
		{"streq-after-concat", "function main(): i32 { var a = \"foo\"; var b = \"bar\"; if (a + b == \"foobar\") { return 5; } return 0; }", 5},
		// String concatenation (`+` on strings → a new heap string).
		{"concat-len", "function main(): i32 { var a = \"foo\"; var b = \"bar\"; var c = a + b; return c.len(); }", 6},
		{"concat-index", "function main(): i32 { var a = \"X\"; var b = \"YZ\"; var c = a + b; return c[2] as i32; }", 90},
		{"concat-chained", "function main(): i32 { var s = \"a\" + \"bc\" + \"def\"; return s.len(); }", 6},
		// (Struct spread `T { ...base, f: v }` builds through SSA and runs
		// correctly on all backends — see the TestSelfHostSSAEmit* suites; it's
		// omitted here because this driver's i32 SSA *interpreter* doesn't model
		// the heap-copy spread, only the native/wasm emitters do.)
		// A `match` on a non-enum (int-literal) scrutinee now lowers through
		// SSA: the parser desugars it to an if/else-if chain (the same shape
		// `switch` produces), so it rides the existing if / `==` lowering and
		// the round-trip evaluator computes its value directly.
		{"literal-match", "function main(): i32 { var n = 2; match (n) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 0; } } }", 20},
		// Still outside the subset → build_func bails (200). (Floats, struct
		// spread, and int-literal match now build through SSA; an enum match —
		// whose variant values the i32 round-trip evaluator can't model — is the
		// out-of-subset case here.)
		{"enum-match-bails", "enum Color { Red, Green } function main(): i32 { var c: Color = Green; match (c) { Red => { return 1; }, Green => { return 2; }, _ => { return 0; } } }", 200},
	}

	run := func(t *testing.T, src string, args ...string) int {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Stdin = strings.NewReader(src)
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("ssa_run did not exit normally for %q (args %v)", src, args)
		}
		return cmd.ProcessState.ExitCode()
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Raw SSA eval matches the program's value …
			if got := run(t, tc.src); got != tc.want {
				t.Errorf("SSA eval of %q = %d, want %d", tc.src, got, tc.want)
			}
			// … and the optimiser is semantics-preserving: -opt evals the
			// same. (For out-of-subset bails the result is the same 200.)
			if got := run(t, tc.src, "-opt"); got != tc.want {
				t.Errorf("optimised SSA eval of %q = %d, want %d", tc.src, got, tc.want)
			}
		})
	}

	// The optimiser must actually shrink the IR: copy-propagation +
	// constant-folding + DCE collapse these to far fewer instructions.
	// `-count` returns the post-(opt) instruction total as the exit code.
	t.Run("shrinks-ir", func(t *testing.T) {
		shrink := []struct {
			name    string
			src     string
			wantOpt int // exact instruction count after optimisation
		}{
			// 2 + 3*4 folds to a single const_int; everything else is dead.
			{"fold-arith", "function main(): i32 { return 2 + 3 * 4; }", 1},
			// Whole chain folds to one const; the unused var is DCE'd.
			{"fold-chain", "function main(): i32 { var a = 2; var b = a + 3; var c = b * 10; return c; }", 1},
			// Dead `var b = 99 * 99` removed; `a` folds to a const.
			{"dce-unused", "function main(): i32 { var a = 1 + 2; var b = 99 * 99; return a; }", 1},
			// Constant `if` collapses entirely: dead arm pruned, phi→copy
			// propagated, x+1 folded — down to a single const_int.
			{"branch-fold", "function main(): i32 { var x = 0; if (true) { x = 10; } else { x = 20; } return x + 1; }", 1},
			// `x * 0` on a loop variable collapses to a constant 0, dropping
			// the whole accumulator chain (algebraic simplification + DCE).
			{"alg-mul-zero", "function main(): i32 { var i = 0; var s = 0; while (i < 5) { s = s + (i * 0); i = i + 1; } return s; }", 7},
		}
		for _, sc := range shrink {
			t.Run(sc.name, func(t *testing.T) {
				raw := run(t, sc.src, "-count")
				opt := run(t, sc.src, "-opt", "-count")
				if opt != sc.wantOpt {
					t.Errorf("%q: optimised inst count = %d, want %d", sc.src, opt, sc.wantOpt)
				}
				if opt >= raw {
					t.Errorf("%q: optimiser did not shrink IR (raw=%d opt=%d)", sc.src, raw, opt)
				}
			})
		}
	})

	// merge_blocks threads the empty br-only blocks that branch-folding
	// leaves behind into their successors: a fully-foldable if collapses to
	// a single block, while genuine control flow (a loop) keeps its blocks.
	// `-blocks` returns the post-(opt) block count as the exit code.
	t.Run("merges-blocks", func(t *testing.T) {
		const collapse = "function main(): i32 { var x = 0; if (true) { x = 10; } else { x = 20; } return x + 1; }"
		const nested = "function main(): i32 { var x = 1; if (true) { if (false) { x = 2; } else { x = 3; } } return x; }"
		const loop = "function main(): i32 { var i = 1; var s = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }"

		if b := run(t, collapse, "-opt", "-blocks"); b != 1 {
			t.Errorf("constant if: optimised block count = %d, want 1", b)
		}
		if b := run(t, nested, "-opt", "-blocks"); b != 1 {
			t.Errorf("nested constant if: optimised block count = %d, want 1", b)
		}
		// The loop's control flow is real: blocks must be preserved, not
		// over-merged (the header has two predecessors).
		rawB := run(t, loop, "-blocks")
		optB := run(t, loop, "-opt", "-blocks")
		if optB != rawB || optB < 2 {
			t.Errorf("loop blocks: raw=%d opt=%d — control flow should be preserved (>=2, unchanged)", rawB, optB)
		}
	})

	// cse collapses duplicate pure subexpressions within a block. The two
	// `i * i` in the loop body (i is a non-constant loop phi, so they don't
	// const-fold) become a single multiply — pinning the exact post-opt
	// instruction count guards that CSE fired.
	t.Run("cse", func(t *testing.T) {
		const dup = "function main(): i32 { var i = 4; var t = 0; while (i > 0) { t = (i * i) + (i * i); i = i - 1; } return t; }"
		raw := run(t, dup, "-count")
		opt := run(t, dup, "-opt", "-count")
		if opt >= raw {
			t.Errorf("cse: optimiser did not shrink IR (raw=%d opt=%d)", raw, opt)
		}
		if opt != 10 {
			t.Errorf("cse: optimised inst count = %d, want 10 (the duplicate i*i collapsed to one)", opt)
		}
	})
}
