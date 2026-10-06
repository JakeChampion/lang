package e2ecompiler

import "testing"

// blockExprIRCases exercise multi-statement BLOCK-EXPRESSION value branches
// through the self-host IR path on x86-64 + wasm (block-expressions slice 1 in
// the self-hosted compiler — see docs/BLOCK-EXPRESSIONS.md).
//
// The gap this closes: an `if`/`match` EXPRESSION value branch was parsed with
// a single `parse_expr`, so a multi-statement branch (`{ let k = e + 1; k }`)
// mis-parsed (the `let` landed in expression position) and bailed the module to
// the AST emitter. The parser now parses each if/match value branch as a
// block-with-tail (parse_branch_body): leading `;`-terminated statements run
// before a trailing expression — written WITHOUT a `;` — that is the branch's
// value. The result is a `Stmt[]` ending in `s_return(tail)`, which the lowering
// lowers (leading statements + the value-producing
// terminal). A lone trailing expression with no leading statements stays
// `[s_return(expr)]`, byte-identical to the single-expr branch.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (wasmtime exit-code truncation, cf. #2908).
var blockExprIRCases = []struct {
	name string
	main string
}{
	// The canonical example: a multi-statement if-EXPRESSION then-branch.
	// f(5): k = 6 -> 6.
	{"if-then-block", `function f(e: i32): i32 { let x: i32 = if (e > 0) { let k = e + 1; k } else { 0 }; return x; }
function main(): i32 { return f(5); }`},
	// A multi-statement ELSE branch (the then-branch is a single expr).
	// e = 0 -> else: j = 3 + 4 = 7.
	{"if-else-block", `function f(e: i32): i32 { let x: i32 = if (e > 0) { e } else { let j = 3 + 4; j }; return x; }
function main(): i32 { return f(0); }`},
	// Multiple leading statements in the then-branch. a=2, b=a*5=10, a+b=12.
	{"if-then-multi-stmt", `function main(): i32 { let x: i32 = if (true) { let a = 2; let b = a * 5; a + b } else { 0 }; return x; }`},
	// A while-loop statement in a branch, then the tail.
	// s = 1+2+3 = 6, tail s.
	{"if-then-while", `function main(): i32 { let x: i32 = if (true) { let s = 0; let i = 1; while (i <= 3) { s = s + i; i = i + 1; } s } else { 0 }; return x; }`},
	// A multi-statement MATCH arm body (enum scrutinee). f(A(20)): n=20, d=n+1=21 -> 42.
	{"match-arm-block", `enum E { A(i32), B } function f(e: E): i32 { let x: i32 = match (e) { A(n) => { let d = n + 1; d }, B => 9 }; return x; }
function main(): i32 { return f(A(20)) + f(B); }`},
	// A multi-statement LITERAL-match arm body (i32 scrutinee). tag=0: s=10+5=15.
	{"litmatch-arm-block", `function f(tag: i32): i32 { let x: i32 = match (tag) { 0 => { let s = 10 + 5; s }, _ => 99 }; return x; }
function main(): i32 { return f(0); }`},
	// A NESTED if-expression as the trailing tail of a branch block.
	// c=3: a=6, a>5 -> a=6.
	{"nested-if-tail", `function f(c: i32): i32 { let x: i32 = if (c > 0) { let a = c * 2; if (a > 5) { a } else { 1 } } else { 0 }; return x; }
function main(): i32 { return f(3); }`},
	// Else-if CHAIN unchanged (single-expr branches): n=5 -> middle arm 20.
	{"else-if-chain", `function f(n: i32): i32 { let x: i32 = if (n > 10) { 30 } else if (n > 0) { 20 } else { 10 }; return x; }
function main(): i32 { return f(5); }`},
	// Single-expr if-expression unchanged (regression guard, stays "ir").
	{"single-expr-regress", `function f(e: i32): i32 { let x: i32 = if (e > 0) { e + 1 } else { 0 }; return x; }
function main(): i32 { return f(5); }`},
	// String-tail block branch: a multi-statement branch yielding a string,
	// then .len(). s = "hi" -> 2.
	{"if-then-string-tail", `function f(): i32 { let s: string = if (true) { let p = "hi"; p } else { "x" }; return s.len(); }
function main(): i32 { return f(); }`},
	// #4521: a general value-position block-expression (the RHS of `let`, not
	// an if/match branch) — desugared to an immediately-invoked lambda in the
	// self-host parser, so it stays on the IR path. k*m = 12.
	{"value-position-var-rhs", `function main(): i32 { let n: i32 = { let k = 3; let m = 4; k * m }; return n; }`},
	// #4521: a bare value-position block as a call argument. 40+2 = 42.
	{"value-position-call-arg", `function id(x: i32): i32 { return x; } function main(): i32 { return id({ let a = 40; a + 2 }); }`},
	// #4521: a single-expr value block `{ e }` stays the bare expr. 7+1 = 8.
	{"value-position-single-expr", `function main(): i32 { let n: i32 = { 7 }; return n + 1; }`},
	// #4521: a string-tail value-position block, then .len(). "foobar" -> 6.
	{"value-position-string-tail", `function main(): i32 { let s: string = { let a = "foo"; let b = "bar"; a + b }; return s.len(); }`},
	// #4522: a conditional `return` inside a value-position block — the
	// else-LESS `if` parses as a control-flow STATEMENT (branch_stmt_start),
	// lowers INLINE (lower_value_block), and the reachable tail yields the
	// block's value. f(5): early exit not taken, tail e+1 = 6.
	{"cf-conditional-return-tail", `function f(e: i32): i32 { let x: i32 = { if (e < 0) { return 99; } e + 1 }; return x; }
function main(): i32 { return f(5); }`},
	// #4522: the early-exit path is taken — the inline `return` escapes the
	// block AND the enclosing function. f(-1): return 99.
	{"cf-conditional-return-taken", `function f(e: i32): i32 { let x: i32 = { if (e < 0) { return 99; } e + 1 }; return x; }
function main(): i32 { return f(-1); }`},
	// #4522: a `break` inside a value-position block inside a loop escapes to
	// the loop (the inline lowering emits a real op_br). i=1,2,3 add; break at
	// 4. s = 6.
	{"cf-break-in-block", `function main(): i32 {
	let s: i32 = 0; let i: i32 = 0;
	while (i < 10) { i = i + 1; let d: i32 = { if (i == 4) { break; } i }; s = s + d; }
	return s;
}`},
	// #4522: a `continue` inside a value-position block skips the rest of the
	// loop body. i=3 skipped: s = 1+2+4+5+6 = 18.
	{"cf-continue-in-block", `function main(): i32 {
	let s: i32 = 0; let i: i32 = 0;
	while (i < 6) { i = i + 1; let d: i32 = { if (i == 3) { continue; } i }; s = s + d; }
	return s;
}`},
}

// TestSelfHostBlockExprIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostBlockExprIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range blockExprIRCases {
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
