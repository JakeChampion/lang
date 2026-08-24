package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// interpDriver bundles lexer + parser + interp + a stdin driver (reads
// source via read_all_stdin, evaluates it with interp.eval_module, and
// exits with the program's VInt / VBool result). It's the input the
// self-hosted compiler compiles into a self-hosted INTERPRETER.
const interpDriverMod = "import \"./lexer\";\n" +
	"import \"./parser\";\n" +
	"import \"./interp\";\n" +
	"function main(): i32 {\n" +
	"    var src: string = read_all_stdin();\n" +
	"    var mod: parser.Module = parser.parse_module(lexer.tokenize(src));\n" +
	"    var result: interp.Value = interp.eval_module(mod);\n" +
	"    match (result) {\n" +
	"        interp.VInt(i) => { return i.v; },\n" +
	"        interp.VBool(b) => { if (b.v) { return 1; } return 0; },\n" +
	"        _ => { return 254; }\n" +
	"    }\n" +
	"    return 254;\n" +
	"}\n"

var interpProgs = []struct {
	name string
	src  string
	exit int
}{
	{"return-literal", "function main(): i32 { return 42; }", 42},
	{"arith", "function main(): i32 { return 6 * 7; }", 42},
	// Hex integer literals (#4341): eval_expr used the decimal-only
	// util.digits_to_i32, which stopped at the `x` and returned 0. Now it uses
	// the hex/binary-aware util.lit_to_i32. `0x1F` = 31; `0x10 + 1` = 17 (each
	// operand parsed independently, no fold in the interp).
	{"hex-literal", "function main(): i32 { return 0x1F; }", 31},
	{"hex-arith", "function main(): i32 { return 0x10 + 1; }", 17},
	// Scientific-notation float literals (#4342): the literal reader parsed
	// integer + fraction only and dropped the exponent, so `1e3` evaluated to
	// 1.0. Each check returns 7 iff the exponent is honoured.
	{"sci-float-exp", "function main(): i32 { if (1e3 == 1000.0) { return 7; } return 0; }", 7},
	{"sci-float-frac", "function main(): i32 { if (1.5e2 == 150.0) { return 7; } return 0; }", 7},
	{"sci-float-neg-exp", "function main(): i32 { var b: f64 = 1e-2; if (b > 0.009 && b < 0.011) { return 7; } return 0; }", 7},
	// A float literal too large for f64 is P002 in the front end, not +Inf
	// (#6842): native rejects the program, so the self-host front end plants a
	// parser-side unknown, which the interp surfaces as a VErr → 254. Before
	// the fix this program scored 7 through the self-host and 1 through native.
	// Underflow is the accepted side of the same boundary on both engines.
	{"float-lit-overflow-rejected", "function main(): i32 { var x: f64 = 1e309; if (x > 1.0e308) { return 7; } return 0; }", 254},
	{"float-lit-underflow-accepted", "function main(): i32 { var x: f64 = 1e-400; if (x == 0.0) { return 7; } return 0; }", 7},
	// Postfix chains on a bool literal (#4338): parse_primary's true/false
	// arms returned the bare bool ExprResult without threading it through
	// parse_postfix (unlike the number / string arms), so `true.to_i()` was
	// parsed as just `true` and the `.to_i() + 41` suffix was silently
	// dropped — the interp returned 1 (the bool) instead of 42. Now both
	// bool arms route through parse_postfix, so the method call + add apply.
	{"bool-literal-postfix", "function (b: boolean) to_i(): i32 { if (b) { return 1; } return 0; } function main(): i32 { return true.to_i() + 41; }", 42},
	{"locals", "function main(): i32 { var x: i32 = 10; var y: i32 = 32; return x + y; }", 42},
	{"if", "function main(): i32 { if (5 > 3) { return 1; } return 0; }", 1},
	{"call", "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(19, 23); }", 42},
	// Default parameter values — fill_default_args_module (run in
	// interp.eval_module) completes the omitted trailing argument.
	{"default-one", "function inc(n: i32, by: i32 = 1): i32 { return n + by; } function main(): i32 { return inc(41); }", 42},
	{"default-multi", "function box(w: i32, h: i32 = 2, d: i32 = 3): i32 { return w * 100 + h * 10 + d; } function main(): i32 { return box(1) - 81; }", 42},
	{"float", "function main(): i32 { var f: f64 = 3.5; var g: f64 = 2.5; if (f + g > 5.0) { return 7; } return 0; }", 7},
	// `as` numeric casts — an integer-target
	// cast is identity on an int / truncates a float, and a float-target
	// cast widens an int / is identity on a float.
	{"cast-i64-to-i32", "function main(): i32 { var v: i64 = 9; return v as i32; }", 9},
	{"cast-f64-to-i32", "function main(): i32 { var f: f64 = 3.9; return f as i32; }", 3},
	{"cast-i32-to-f64", "function main(): i32 { var n: i32 = 5; var f: f64 = n as f64; return (f + 0.5) as i32; }", 5},
	{"cast-in-i64-array-sum", "function main(): i32 { var xs: i64[] = [3, 5, 90]; var s: i64 = 0; for v in xs { s = s + v; } return s as i32; }", 98},
	// Non-numeric `as <Type>` ascription (#2669) — `as_i32[]` is a zero-cost
	// identity on the value. eval_unary passes the operand through unchanged,
	// matching the AST/IR emitters.
	{"asc-array-identity", "function main(): i32 { var a = [3, 4] as i32[]; return a[0] + a[1]; }", 7},
	// Range-for `for i in LOW..HIGH`: the parser emits a synthetic
	// __range(LOW, HIGH) for-iter that the IR path lowers (irlower) but the
	// interpreter doesn't understand — parser.desugar_ranges_module (run in
	// interp.eval_module) rewrites it to a counting while-loop so the interp
	// evaluates it. Without that, an undesugared __range iter mis-evaluates
	// (a 254 non-i32 result). Covers continue/break (the increment is at the
	// top of the desugared loop) and empty/reversed (zero iterations).
	{"range-sum", "function main(): i32 { var s = 0; for i in 0..5 { s = s + i; } return s; }", 10},
	{"range-continue", "function main(): i32 { var s = 0; for i in 0..10 { if (i % 2 == 1) { continue; } s = s + i; } return s; }", 20},
	{"range-break", "function main(): i32 { var s = 0; for i in 0..100 { if (i == 5) { break; } s = s + i; } return s; }", 10},
	{"range-empty", "function main(): i32 { var c = 7; for i in 5..5 { c = c + 1; } return c; }", 7},
	{"range-nested", "function main(): i32 { var t = 0; for i in 0..3 { for j in 0..3 { t = t + 1; } } return t; }", 9},
	// A range-for inside a LAMBDA body. desugar_ranges_one recursed through the
	// statement forms and had no expression descent at all, so its `_` arm
	// returned a `var` / `return` / call statement untouched and the lambda body
	// hanging off it was never reached — the `__range` iter survived to the
	// interpreter, which has no such function (#7174). Native answers 6 on all
	// four shapes.
	{"range-in-lambda", "function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { return run(function(): i32 { var s = 0; for i in 0..4 { s = s + i; } return s; }); }", 6},
	{"range-in-lambda-var", "function main(): i32 { var f = function(): i32 { var s = 0; for i in 0..4 { s = s + i; } return s; }; return f(); }", 6},
	{"range-in-lambda-nested", "function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { return run(function(): i32 { return run(function(): i32 { var s = 0; for i in 0..4 { s = s + i; } return s; }); }); }", 6},
	// A LABELED break/continue inside a lambda. resolve_labels_stmt had no
	// expression recursion at all, so a labeled loop in a lambda body never had
	// its tag resolved and the jump degraded to tag 0 — the innermost loop.
	// `break outer` silently broke the wrong loop; `continue outer` re-entered
	// the inner `while (true)` forever, hanging the COMPILED binary (#7199).
	{"labeled-break-in-lambda", "function main(): i32 {\n" +
		"    var f: () => i32 = function (): i32 {\n" +
		"        var n: i32 = 0;\n" +
		"        outer: while (n < 100) {\n" +
		"            inner: while (true) { n = n + 1; break outer; }\n" +
		"            n = n + 100;\n" +
		"        }\n" +
		"        return n;\n" +
		"    };\n" +
		"    return f() + 41; }", 42},
	{"labeled-continue-in-lambda", "function main(): i32 {\n" +
		"    var f: () => i32 = function (): i32 {\n" +
		"        var n: i32 = 0;\n" +
		"        outer: while (n < 3) {\n" +
		"            n = n + 1;\n" +
		"            inner: while (true) { continue outer; }\n" +
		"        }\n" +
		"        return n;\n" +
		"    };\n" +
		"    return f() + 39; }", 42},
	// The label belongs to the lambda's OWN scope, so an identically named loop
	// outside it must not be what the break resolves against.
	{"labeled-break-shadowed-in-lambda", "function main(): i32 {\n" +
		"    var acc: i32 = 0;\n" +
		"    outer: while (acc < 1) {\n" +
		"        acc = acc + 1;\n" +
		"        var f: () => i32 = function (): i32 {\n" +
		"            var n: i32 = 0;\n" +
		"            outer: while (n < 100) { n = n + 1; break outer; }\n" +
		"            return n;\n" +
		"        };\n" +
		"        acc = acc + f();\n" +
		"    }\n" +
		"    return acc + 40; }", 42},
	// `defer` inside a LAMBDA. lower_defers found its actions through
	// dl_expr_kids, whose `_` arm reports no children for an ExprLambda —
	// structurally, because a lambda's children are statements, not expressions.
	// So the defer was never lowered at parse time and the interpreter, which
	// sees no StmtDefer, dropped it (#7174). A lambda is its own scope: the
	// action runs when the LAMBDA returns.
	{"defer-in-lambda", "function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { return run(function(): i32 { var n = 0; defer { n = n + 7; } return n; }); }", 0},
	{"defer-in-lambda-runs", "function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { var out = 0; var r = run(function(): i32 { defer { out = out + 7; } return 1; }); return out + r; }", 8},
	// The enclosing function ALSO defers: the two scopes must not merge, and the
	// lambda's action must run at the lambda's exit, before main's.
	{"defer-in-lambda-and-fn", "function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { var log = 0; defer { log = log * 10; }\n" +
		"    var r = run(function(): i32 { defer { log = log + 3; } return 1; });\n" +
		"    if (log != 3) { return 90; } return r; }", 1},
	// The lambda hangs off a match arm's statement, which the arm walk reaches
	// but the expression descent has to finish.
	{"range-in-lambda-match-arm", "enum E { A(i32) }\n" +
		"function run(f: () => i32): i32 { return f(); }\n" +
		"function main(): i32 { var e: E = E.A(1); match (e) { E.A(v) => { return run(function(): i32 { var s = 0; for i in 0..4 { s = s + i; } return s; }); }, _ => { return 0; } } return 0; }", 6},
	// Generic trait declaration header `trait Name[T]` with a default
	// method (#4340): parse_trait_decl walked name -> `:` supertraits ->
	// `{` and never consumed the `[T]` type-param list, so `[T] { … }`
	// spilled back into parse_module as a stray array literal + orphan
	// block and the default method `greet` was lost — dispatch of
	// `p.greet()` then failed (interp 254). Now the header consumes `[T]`,
	// so the default method is synthesised onto `impl Greet[i32] for P`
	// and `p.greet()` resolves to 42.
	{"generic-trait-default-method", "trait Greet[T] { function greet(self: Self): i32 { return 42; } } struct P { x: i32 } impl Greet[i32] for P {} function main(): i32 { var p = P { x: 1 }; return p.greet(); }", 42},
	// Leading-colon slice `a[:hi]` (#4339 item 3): parse_postfix's `[` arm read
	// the `:` as the start of erased type args (parse_expr stalls on it) and
	// dropped the slice; now it's a low-implicitly-0 slice. `[10,20,30][:2]`
	// => elements 0,1 => 30.
	{"slice-open-low", "function main(): i32 { var a = [10, 20, 30]; var b = a[:2]; return b[0] + b[1]; }", 30},
	// C-style `for` with a non-`var` init (#4339 item 2): the self-host arm
	// gated only on a `var` init, so an expression init (`i = 0`) or an empty
	// init (`;`) fell into the `(k,v) in m` map arm and shredded. Now a
	// top-level `;` in the header marks a C-for and the init may be empty /
	// `var` / expression. Both loops sum 0..3 => 6.
	{"cfor-expr-init", "function main(): i32 { var i = 0; var s = 0; for (i = 0; i < 4; i = i + 1) { s = s + i; } return s; }", 6},
	{"cfor-empty-init", "function main(): i32 { var i = 0; var s = 0; for (; i < 4; i = i + 1) { s = s + i; } return s; }", 6},
	// Vertical-tab / form-feed whitespace (#4339 item 5): is_space accepted only
	// space/tab/LF/CR, so a form feed (\f, 0x0C) between tokens lexed to TokError
	// and tokenize STOPPED, truncating the program. Now \v (0x0B) and \f (0x0C)
	// are skipped like native's unicode.IsSpace. The \f sits between `{` and
	// `return`; the program still returns 42.
	{"lexer-formfeed-ws", "function main(): i32 {\freturn 42; }", 42},
	// Bitwise / shift ops (#4348 item 7): eval_binary had no `& | ^ << >>`
	// arm, so every bit-op program evaluated to VErr (exit 254). Now they
	// apply the host i32 operator, matching native.
	{"bit-and", "function main(): i32 { return 5 & 3; }", 1},
	{"bit-or", "function main(): i32 { return 5 | 2; }", 7},
	{"bit-xor", "function main(): i32 { return 6 ^ 3; }", 5},
	{"bit-shl", "function main(): i32 { return 1 << 4; }", 16},
	{"bit-shr", "function main(): i32 { return 256 >> 2; }", 64},
	// Total division semantics (#4348 item 6): integer `x / 0 == 0` and
	// `x % 0 == x` (docs/INTEGER-SEMANTICS.md), not a runtime error (VErr,
	// which exits 254 after error-swallowing).
	{"div-by-zero-total", "function main(): i32 { return 7 / 0; }", 0},
	{"mod-by-zero-total", "function main(): i32 { return 7 % 0; }", 7},
	// Float division by zero is IEEE, not an error: `1.0/0.0` is +Inf and
	// `0.0/0.0` is NaN (self-comparison false). Each returns 7 iff honoured.
	{"fdiv-inf", "function main(): i32 { var x: f64 = 1.0 / 0.0; if (x > 1000000000.0) { return 7; } return 0; }", 7},
	{"fdiv-nan", "function main(): i32 { var y: f64 = 0.0 / 0.0; if (y != y) { return 7; } return 0; }", 7},
	// Short-circuit && / || (#4348 item 5): eval_binary evaluated both operands
	// eagerly, so a guarded out-of-bounds RHS still ran. With i=5 and a len-3
	// array, `i < 3 && a[i] == 1` must not evaluate `a[5]` (OOB → VErr → 254);
	// short-circuiting keeps the condition false so the program returns 42.
	// The `||` mirror short-circuits on a true LHS.
	{"and-short-circuit", "function main(): i32 { var a = [1, 2, 3]; var i = 5; if (i < 3 && a[i] == 1) { return 0; } return 42; }", 42},
	{"or-short-circuit", "function main(): i32 { var a = [1, 2, 3]; var i = 5; if (i >= 3 || a[i] == 1) { return 42; } return 0; }", 42},
	// Unconsumed runtime errors are no longer swallowed (#4348 item 4): a
	// var-initializer (or expression-statement) that raises a runtime error
	// aborts the function instead of binding the VErr and continuing. Native
	// exits 1 on the out-of-bounds `a[5]`; the interp surfaces the VErr, which
	// the driver maps to 254 — the point is that it is NOT swallowed to 7.
	{"oob-var-init-not-swallowed", "function main(): i32 { var a = [1, 2, 3]; var x = a[5]; return 7; }", 254},
	// Braceless single-statement control-flow bodies (#4337): native accepts a
	// single statement as an if/else/while/loop/for body (`if (c) x = 1;`). The
	// self-host required `{` and mis-parsed a braceless body into an
	// unknown-stmt with the real statement hoisted to run unconditionally. Now
	// those body sites accept a braceless statement (wrapped in a 1-stmt block).
	{"braceless-if-else", "function main(): i32 { var x = 0; if (x == 0) x = 40; else x = 1; return x; }", 40},
	{"braceless-while", "function main(): i32 { var x = 0; while (x < 42) x = x + 1; return x; }", 42},
	{"braceless-for", "function main(): i32 { var s = 0; for i in 0..5 s = s + i; return s; }", 10},
	// Chained assignment `x = y = e` (#4339 item 1): assignment is statement-
	// only in the self-host, so the RHS parse_expr couldn't consume the inner
	// `=` and `= e` was left as junk — only the first link ran (silent wrong
	// result). Now a bare-ident chain desugars to right-to-left assigns
	// (`y = e; x = y;`), so every target receives the value.
	{"chained-assign-2", "function main(): i32 { var x = 0; var y = 0; x = y = 20; return x + y; }", 40},
	{"chained-assign-3", "function main(): i32 { var x = 0; var y = 0; var z = 0; x = y = z = 14; return x + y + z; }", 42},
	// (#4339 item 4 — if-let source struct-lit suppression — is pinned in
	// ifLetIRCases/"labeled-then-block": the interp value path can't host it
	// because native if-let wants BARE patterns while this interp evaluates
	// only QUALIFIED payload variants — an empty intersection for a
	// payload-binding if-let.)
	// Callback-passing `use x <- call();` monadic bind (#4335): the rest of the
	// block becomes a callback lambda appended as the call's last argument, and
	// the block returns the call's result — `use r <- apply(41); return r + 1;`
	// desugars to `return apply(41, (r) => { return r + 1; });`. Needs a `use`
	// arm in parse_block/parse_stmt. apply invokes the callback with 41, so
	// r + 1 == 42.
	{"use-monadic-bind", "function apply(n: i32, cb: (i32) => i32): i32 { return cb(n); } function main(): i32 { use r <- apply(41); return r + 1; }", 42},
	// `as u8` truncates to the low 8 bits at runtime (#4348 item 3, cast slice):
	// the interp treated the cast as identity, so a u8 cast didn't wrap. Now
	// `(255 + 1) as u8 == 0` (256 & 255) and an in-range value is unchanged.
	{"cast-u8-wrap", "function main(): i32 { var x = 255; return ((x + 1) as u8) as i32; }", 0},
	{"cast-u8-inrange", "function main(): i32 { var x = 200; return (x as u8) as i32; }", 200},

	// Builtin `.len()` / `.append()` methods: the interp special-cased the
	// bare `len(x)` function but not the method forms the native compiler
	// recognises without imports — `string.len()`, `array.len()`, and the
	// immutable `array.append(x)`. `.len()` on both string and array, and
	// append leaves the original array untouched (copy-loop, so the fresh
	// buffer can't alias the shared receiver binding).
	{"str-len-method", "function main(): i32 { return \"hello\".len() as i32; }", 5},
	{"arr-len-method", "function main(): i32 { var a: i32[] = [1, 2, 3]; return a.len() as i32; }", 3},
	{"arr-len-empty", "function main(): i32 { var a: i32[] = []; return a.len() as i32; }", 0},
	{"arr-append-len", "function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = a.append(9); return b.len() as i32; }", 3},
	{"arr-append-value", "function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = a.append(9); return b[2]; }", 9},
	{"arr-append-immutable", "function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = a.append(9); return a.len() as i32 * 10 + b.len() as i32; }", 23},

	// Top-level `const` references: the parser desugars `const N = expr;`
	// into a zero-arg function `N()` and the native compiler lowers a bare
	// `N` reference to a call. The interp's ExprIdent handler only did an
	// env lookup, so a bare const reference errored as an undefined
	// identifier. Now a bare ident with no local binding that names a
	// zero-arg, non-method function evaluates as a nullary call.
	{"const-ref", "const N: i32 = 42; function main(): i32 { return N; }", 42},
	{"const-in-expr", "const N: i32 = 10; function main(): i32 { return N + N * 2; }", 30},
	{"const-two", "const A: i32 = 3; const B: i32 = 4; function main(): i32 { return A * B; }", 12},
	{"const-in-callee", "const K: i32 = 5; function helper(): i32 { return K * 2; } function main(): i32 { return helper(); }", 10},
	{"const-string", "const S: string = \"hello\"; function main(): i32 { var s = S; return s.len() as i32; }", 5},

	// Nullary enum variants: `E.Red` constructs a payload-less variant
	// value (a zero-field struct tagged with the variant name), and a
	// qualified variant pattern `E.Red` matches it by its final path
	// segment — a bare enum-type object otherwise resolves to no value and
	// the field access errors. Payload variants (`E.Some(x)`) are not
	// covered here.
	{"enum-nullary-match", "enum E { A, B } function main(): i32 { var e = E.B; match (e) { E.A => { return 1; }, E.B => { return 2; } } }", 2},
	{"enum-nullary-first", "enum E { A, B } function main(): i32 { var e = E.A; match (e) { E.A => { return 1; }, E.B => { return 2; } } }", 1},
	{"enum-three-way", "enum Color { Red, Green, Blue } function main(): i32 { var c = Color.Green; match (c) { Color.Red => { return 1; }, Color.Green => { return 2; }, Color.Blue => { return 3; } } }", 2},
	{"enum-wildcard-arm", "enum E { A, B, C } function main(): i32 { var e = E.C; match (e) { E.A => { return 1; }, _ => { return 9; } } }", 9},
	{"enum-return-from-fn", "enum St { On, Off } function flip(s: St): St { match (s) { St.On => { return St.Off; }, St.Off => { return St.On; } } } function main(): i32 { var r = flip(St.On); match (r) { St.On => { return 1; }, St.Off => { return 0; } } }", 0},
	{"enum-in-array", "enum E { A, B } function main(): i32 { var a: E[] = [E.A, E.B, E.A]; var n = 0; for x in a { match (x) { E.A => { n = n + 1; }, E.B => { n = n + 10; } } } return n; }", 12},

	// Payload enum variants: `E.Some(x)` constructs a variant value carrying
	// the payload as the parser's synthesised `__ev` field, and a `V(a)`
	// pattern binds each payload field positionally (the `__ev` marker
	// distinguishes an enum payload — bind the field — from a tagged-struct /
	// union variant like `Circle(c)`, which binds the whole struct). The
	// interp errored on the `E.Some(x)` call (bare enum-type object) and
	// never constructed the value.
	{"enum-payload-bind", "enum E { A(i32), B } function main(): i32 { var e = E.A(7); match (e) { E.A(n) => { return n; }, E.B => { return 0; } } }", 7},
	{"enum-payload-other-arm", "enum E { A(i32), B } function main(): i32 { var e = E.B; match (e) { E.A(n) => { return n; }, E.B => { return 99; } } }", 99},
	{"enum-option-unwrap", "enum Opt { Some(i32), None } function unwrap(o: Opt): i32 { match (o) { Opt.Some(v) => { return v; }, Opt.None => { return 0; } } } function main(): i32 { return unwrap(Opt.Some(42)) + unwrap(Opt.None); }", 42},
	{"enum-result-err", "enum R { Ok(i32), Err(i32) } function main(): i32 { var r = R.Err(3); match (r) { R.Ok(v) => { return v; }, R.Err(e) => { return 100 + e; } } }", 103},
	{"enum-payload-ignore", "enum E { A(i32), B } function main(): i32 { var e = E.A(5); match (e) { E.A(_) => { return 1; }, E.B => { return 2; } } }", 1},
	{"enum-payload-in-array", "enum E { N(i32) } function main(): i32 { var a: E[] = [E.N(1), E.N(2), E.N(3)]; var s = 0; for x in a { match (x) { E.N(v) => { s = s + v; } } } return s; }", 6},

	// Methods declared on an enum type dispatch on its variant values. An
	// enum desugars to variant structs but not to a union alias, so the
	// interp's union-receiver method dispatch (is_variant_of_alias_interp,
	// which consults only the alias table) couldn't see that a variant
	// belongs to the enum — `Sh.Sq(4).area()` failed to resolve. eval_module
	// now folds each enum into the alias table as a synthetic union.
	{"enum-method-payload", "enum Sh { Circle(i32), Sq(i32) } function (s: Sh) area(): i32 { match (s) { Sh.Circle(r) => { return r * r * 3; }, Sh.Sq(w) => { return w * w; } } } function main(): i32 { var s = Sh.Sq(4); return s.area(); }", 16},
	{"enum-method-nullary", "enum Dir { N, S, E, W } function (d: Dir) dx(): i32 { match (d) { Dir.E => { return 1; }, Dir.W => { return 0 - 1; }, _ => { return 0; } } } function main(): i32 { var d = Dir.E; return d.dx(); }", 1},
	{"enum-method-with-arg", "enum Opt { Some(i32), None } function (o: Opt) unwrap_or(dflt: i32): i32 { match (o) { Opt.Some(x) => { return x; }, Opt.None => { return dflt; } } } function main(): i32 { return Opt.Some(7).unwrap_or(0) + Opt.None.unwrap_or(5); }", 12},

	// i64 values beyond i32 range. The interp's VInt is a 32-bit slot, so an
	// i64 literal / arithmetic result that exceeds i32 would truncate
	// (`5000000000` wraps). A second integer variant, VInt64, holds
	// wide values as two i32 halves (a raw i64 union payload trips a
	// native-backend drop fault; two i32 fields drop cleanly). A declared
	// i64 type at a var/param binding promotes a compact value to VInt64 so
	// arithmetic takes the 64-bit path even when operands fit i32
	// (100000 * 100000 must not overflow), and an i64 operation always
	// yields a wide result so a running accumulator keeps its width; `as
	// i32` narrows back. Division/mod of a negative-low-word value exercises
	// the unpack-mask path (an inline `& 4294967295` i64 literal is
	// mis-emitted by the native backend as a sign-extended 32-bit immediate,
	// so the mask is held in a local).
	{"i64-literal-div", "function main(): i32 { var x: i64 = 5000000000; return (x / 1000000000) as i32; }", 5},
	{"i64-mul-fits-operands", "function main(): i32 { var x: i64 = 100000; var y: i64 = x * 100000; return (y / 1000000000) as i32; }", 10},
	{"i64-accumulator", "function main(): i32 { var s: i64 = 0; var i: i32 = 0; while (i < 5) { s = s + 1000000000; i = i + 1; } return (s / 1000000000) as i32; }", 5},
	{"i64-negative-div", "function main(): i32 { var x: i64 = 8000000000; return (x / 1000000000) as i32; }", 8},
	{"i64-mod", "function main(): i32 { var x: i64 = 5000000007; return (x % 1000000000) as i32; }", 7},
	{"i64-param-promote", "function scale(n: i64): i64 { return n * 1000000; } function main(): i32 { return (scale(5000) / 1000000000) as i32; }", 5},
	{"i64-array-sum", "function main(): i32 { var xs: i64[] = [5000000000, 3000000000]; var s: i64 = 0; for v in xs { s = s + v; } return (s / 1000000000) as i32; }", 8},
	{"i64-compare", "function main(): i32 { var x: i64 = 5000000000; if (x > 4000000000) { return 1; } return 0; }", 1},
	// i32 arithmetic is unchanged — overflow still wraps via the 32-bit path.
	{"i32-overflow-unchanged", "function main(): i32 { var x: i32 = 100000; return x * 100000; }", 0},
	// Typed u32 / f32 carriers (#4348 final slice). VUint tags a u32 bit
	// pattern so div / rem / compares / `>>` run UNSIGNED (logical shift,
	// count masked &31 — docs/INTEGER-SEMANTICS.md) rather than signed
	// through VInt. VFloat32 tags an f32-precision value (stored as
	// the already-rounded f64, the #4366 model): arithmetic computes at f64
	// then rounds the result to single precision, sticky through chains,
	// rather than running at full f64. Each src pinned natively.
	{"u32-shr-logical", "function main(): i32 { var x: u32 = 4294967288 as u32; var y = x >> 2; if (y == (1073741822 as u32)) { return 42; } return 7; }", 42},
	{"u32-div-unsigned", "function main(): i32 { var x: u32 = 4294967290 as u32; var y: u32 = 5 as u32; if (x / y > (100 as u32)) { return 42; } return 7; }", 42},
	{"u32-cmp-unsigned", "function main(): i32 { var x: u32 = 4294967290 as u32; if (x > (5 as u32)) { return 42; } return 7; }", 42},
	{"f32-round-2p24", "function main(): i32 { var a: f32 = 16777216.0 as f32; var b = a + (1.0 as f32); if ((b as f64) > 16777215.5 && (b as f64) < 16777216.5) { return 42; } return 7; }", 42},
	// Sticky chain: 2^24 + 1.5 rounds up to 2^24+2 at f32; +1 more lands
	// midway and ties-to-even up to 2^24+4 — only true single-precision
	// rounding at EVERY step (incl. the mixed f32 + f64-literal add)
	// produces 16777220.
	{"f32-sticky-chain", "function main(): i32 { var a: f32 = 16777216.0; var b = a + 1.5; var c = b + (1.0 as f32); if ((c as f64) == 16777220.0) { return 42; } return 7; }", 42},
	// defer runs at SCOPE EXIT, not at its source position (#4348 item 1):
	// eval_module now runs lower_defers_module itself, so the stdin driver —
	// which feeds the raw module straight in — no longer executes the deferred
	// action eagerly. Eager execution sets r=101 BEFORE the if, so the
	// timing-sensitive shape returns 101 instead of native's 2.
	{"defer-scope-exit", "function main(): i32 { var r = 2; defer { r = 101; } if (r == 2) { return 2; } return r; }", 2},
	// The lowering's synthesized cleanup guards (`if (__dfa0) …`) condition on
	// i32 flags; the interp normalises an int cond to its non-zero truth. A
	// mid-body read after the defer still sees the pre-defer value.
	{"defer-not-eager-mid-body", "function main(): i32 { var r = 1; defer { r = 100; } var x = r + 1; if (x == 2) { return 2; } return 3; }", 2},
	// Labeled break unwinds MULTIPLE loop levels (#4348 item 2): the
	// resolve_labels-baked depth now rides the sig encoding (2+2d) and each
	// loop arm peels one level. Pre-fix the depth was ignored, `break outer`
	// broke only the inner loop, and `outer: while (true)` hung forever.
	{"labeled-break-two-level", "function main(): i32 { var s = 0; outer: while (true) { var i = 0; while (i < 10) { s = s + 1; if (s >= 11) { break outer; } i = i + 1; } } return s; }", 11},
	// Labeled continue re-enters the OUTER loop (depth 3+2d), skipping the
	// inner loop's remaining iterations: 3 outer iterations * 2 inner steps.
	{"labeled-continue-two-level", "function main(): i32 { var s = 0; outer: for i in [1, 2, 3] { var j = 0; while (j < 5) { s = s + 1; if (j == 1) { continue outer; } j = j + 1; } } return s; }", 6},
	// Unlabeled break/continue still bind to the innermost loop (depth 0 —
	// the encoding's base case, unchanged).
	{"unlabeled-break-innermost", "function main(): i32 { var s = 0; var i = 0; while (i < 3) { var j = 0; while (j < 10) { if (j == 2) { break; } s = s + 1; j = j + 1; } i = i + 1; } return s; }", 6},
	// Wrapping i32 arithmetic (#5622) through the COMPILED interpreter:
	// `+ - * <<` are two's-complement modular at the operand width
	// (docs/INTEGER-SEMANTICS.md), including on a value read out of a
	// VInt enum payload. Each returns 7 iff the result wrapped and 5 iff
	// it stayed wide in a 64-bit register; `not-greater` is the sharpest
	// of them, since an unnarrowed 2147483648 compares ABOVE i32::MAX.
	{"i32-wrap-add-negative", "function main(): i32 { var a: i32 = 2147483647; if ((a + 1) < 0) { return 7; } return 5; }", 7},
	{"i32-wrap-add-eq-min", "function main(): i32 { var a: i32 = 2147483647; if ((a + 1) == (0 - 2147483647 - 1)) { return 7; } return 5; }", 7},
	{"i32-wrap-add-via-locals", "function main(): i32 { var a: i32 = 2147483647; var b: i32 = a + 1; var c: i32 = 0 - 2147483647 - 1; if (b == c) { return 7; } return 5; }", 7},
	{"i32-wrap-add-literals", "function main(): i32 { if ((2147483647 + 1) == (0 - 2147483647 - 1)) { return 7; } return 5; }", 7},
	{"i32-wrap-add-not-greater", "function main(): i32 { var a: i32 = 2147483647; if ((a + 1) > a) { return 5; } return 7; }", 7},
	{"i32-wrap-sub", "function main(): i32 { var a: i32 = 0 - 2147483647 - 1; if ((a - 1) == 2147483647) { return 7; } return 5; }", 7},
	{"i32-wrap-mul", "function main(): i32 { var a: i32 = 100000; if ((a * 100000) == 1410065408) { return 7; } return 5; }", 7},
	{"i32-wrap-shl", "function main(): i32 { var a: i32 = 1; if ((a << 31) == (0 - 2147483647 - 1)) { return 7; } return 5; }", 7},
	// Saturating operators (#5542) in the SELF-HOST tree-walking
	// interpreter: interp.fern computes the i32 forms exactly in a host
	// i64 and clamps, and the i64 forms with the same pre-check /
	// round-trip shapes irlower emits. These are the interp-side sibling
	// of internal/e2e/saturating_arith_test.go.
	{"sat-add-hi", "function main(): i32 { var a: i32 = 2147483647; if ((a +| 1) == a) { return 7; } return 0; }", 7},
	{"sat-sub-lo", "function main(): i32 { var a: i32 = 0 - 2147483647 - 1; if ((a -| 1) == a) { return 7; } return 0; }", 7},
	{"sat-mul-hi", "function main(): i32 { if ((100000 *| 100000) == 2147483647) { return 7; } return 0; }", 7},
	{"sat-mul-lo", "function main(): i32 { if (((0 - 100000) *| 100000) == (0 - 2147483647 - 1)) { return 7; } return 0; }", 7},
	{"sat-plain", "function main(): i32 { return 40 +| 2; }", 42},
	// Shift member: clamps high, clamps low, passes through, and masks the
	// count exactly as the wrapping shift does.
	{"sat-shl-hi", "function main(): i32 { var a: i32 = 1; if ((a <<| 31) == 2147483647) { return 7; } return 0; }", 7},
	{"sat-shl-lo", "function main(): i32 { var a: i32 = 0 - 2; if ((a <<| 31) == (0 - 2147483647 - 1)) { return 7; } return 0; }", 7},
	// -1 << 31 is exactly i32::MIN, so it must NOT clamp by accident: the
	// round-trip has to accept it.
	{"sat-shl-exact-min", "function main(): i32 { var a: i32 = 0 - 1; if ((a <<| 31) == (0 - 2147483647 - 1)) { return 7; } return 0; }", 7},
	{"sat-shl-plain", "function main(): i32 { return 21 <<| 1; }", 42},
	{"sat-shl-mask", "function main(): i32 { return 42 <<| 32; }", 42},
	// `slice_unchecked(s, a, b)` (#5634): the byte-slice builtin the compiler's
	// own sources now call. call_func evaluates it like the ExprSlice arm;
	// before the case existed it fell through to "undefined function" → 254.
	{"slice-unchecked-builtin", "function main(): i32 { var s: string = \"hello world\"; var n: i32 = s.len(); return slice_unchecked(s, 6, n).len(); }", 5},
	{"slice-unchecked-oob", "function main(): i32 { var s: string = \"hi\"; return slice_unchecked(s, 0, 3).len(); }", 254},
}

// interpDriverFiles is the in-memory module set for interpDriverMod: its own
// source plus the TRANSITIVE import closure of the three modules it imports.
//
// Derived rather than listed. The list used to be spelled out as
// {util, lexer, parser, interp}, which went stale the moment `interp.fern`
// gained an import — the compile then failed with a missing module rather than
// anything about the change that caused it.
func interpDriverFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{"main.fern": interpDriverMod}
	for _, root := range []string{"lexer.fern", "parser.fern", "interp.fern"} {
		for _, p := range selfHostImportClosure(t, "../../examples/self_host", root) {
			base := filepath.Base(p)
			if _, ok := files[base]; ok {
				continue
			}
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			files[base] = string(src)
		}
	}
	return files
}

// TestSelfHostInterpDriverX86_64 is the keystone of the inference
// overhaul: the self-hosted compiler compiles the self-hosted
// INTERPRETER (interp.fern, whose Value union has VInt/VString/VFloat
// all with field `v`). The resulting binary
// evaluates programs and exits with their result.
func TestSelfHostInterpDriverX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	// The interp "driver" is just a program importing ./lexer + ./parser +
	// ./interp, compiled by the file-based asm driver (no bundle_run).
	files := interpDriverFiles(t)
	interpAsm, progDir := compileFilesModload(t, runner, driverBin, files)
	if len(interpAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the interp driver")
	}
	interpBin := buildBin(t, gcc, progDir, "interp", interpAsm)

	for _, tc := range interpProgs {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(interpBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], interpBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("interp(%q) exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostInterpDriverArm64 — CI-gated arm64 counterpart.
func TestSelfHostInterpDriverArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)
	files := interpDriverFiles(t)
	interpAsm, progDir := compileFilesModload(t, x86runner, driverBin, files, "-target", "arm64-linux")
	interpBin := buildBin(t, arm64gcc, progDir, "interp", interpAsm)

	for _, tc := range interpProgs {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runArm64Bin(qemu, interpBin)
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("interp(%q) exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
