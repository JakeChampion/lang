// Block-expressions: `if`/`match` expression branches accept a
// `{ stmts; tail }` body whose statements run in a fresh child scope and
// whose trailing expression (no `;`) is the branch's value. The
// interpreter is the source of truth; slice 2 lowers them on all three
// compiled backends (wasm / x86-64 / arm64) via the target-agnostic IR.
// The TestBlockExprInterp cases below run on the interpreter and assert
// main()'s exit code; the differential tests further down run real
// programs on every backend and assert each backend's stdout matches the
// interpreter's. See docs/BLOCK-EXPRESSIONS.md.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// blockExprInterpExit runs src on the interpreter and returns (exitCode, stdout,
// stderr) without failing the test — callers assert on the values
// (slice-1 reject paths expect a non-zero exit, the happy paths expect a
// specific code).
func blockExprInterpExit(t *testing.T, src string) (int, string, string) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", p)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

func TestBlockExprInterp(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			// `if`-branch block: a leading statement then a trailing
			// value. The block evaluates to `e + 1` = 6.
			"if-block-tail",
			`function main(): i32 {
				var e = 5;
				var x: i32 = if (e > 0) { var k = e + 1; k } else { 0 };
				return x;
			}`,
			6,
		},
		{
			// The other branch is taken — value-less branch isn't
			// reached, the single-expr `else` value flows.
			"if-block-else-taken",
			`function main(): i32 {
				var e = 0;
				var x: i32 = if (e > 0) { var k = e + 1; k } else { var z = 9; z };
				return x;
			}`,
			9,
		},
		{
			// `match`-arm block-expression: arm 0 runs `var s = tag +
			// 5; s` → 5; the wildcard arm is a bare expr.
			"match-arm-block",
			`function main(): i32 {
				var tag = 0;
				var r: i32 = match (tag) { 0 => { var s = tag + 5; s }, _ => 99 };
				return r;
			}`,
			5,
		},
		{
			// Multiple leading statements; the tail sees them all.
			"multi-leading-statements",
			`function main(): i32 {
				var x: i32 = if (true) { var a = 2; var b = a * 3; var c = b + 1; c } else { 0 };
				return x;
			}`,
			7,
		},
		{
			// Composition: a block-expr `if` nested inside a `match`
			// scrutinee. `match (if (c) { var k = 7; k } else { 0 }) {
			// ... }` → matches 7.
			"compose-through-match",
			`function main(): i32 {
				var c = true;
				var r: i32 = match (if (c) { var k = 7; k } else { 0 }) {
					7 => { var hit = 1; hit },
					_ => 0
				};
				return r;
			}`,
			1,
		},
		{
			// Block locals don't leak: the same name `k` is rebound in
			// a later block-expr without collision, proving each block
			// gets its own scope.
			"locals-confined-to-block",
			`function main(): i32 {
				var a: i32 = if (true) { var k = 10; k } else { 0 };
				var b: i32 = if (true) { var k = 20; k } else { 0 };
				return a + b;
			}`,
			30,
		},
		{
			// #4521: a general value-position block-expression (not an
			// if/match branch) — the RHS of `var`. Leading statements then
			// a trailing value; 3*4 = 12.
			"value-position-var-rhs",
			`function main(): i32 {
				var n: i32 = { var k = 3; var m = 4; k * m };
				return n;
			}`,
			12,
		},
		{
			// #4521: a bare value-position block as a call argument.
			"value-position-call-arg",
			`function id(x: i32): i32 { return x; }
			function main(): i32 {
				return id({ var a = 40; a + 2 });
			}`,
			42,
		},
		{
			// #4521: a single-expr value block `{ e }` is just `e` (the
			// branch-form passthrough), so it stays a plain expression.
			"value-position-single-expr",
			`function main(): i32 {
				var n: i32 = { 7 };
				return n + 1;
			}`,
			8,
		},
		{
			// #4522: a conditional `return` inside a value-position block — the
			// early exit escapes the enclosing function; the else branch (with a
			// reachable tail) yields the block's value. f(5): tail e+1 = 6.
			"cf-conditional-return-tail",
			`function f(e: i32): i32 { var x: i32 = { if (e < 0) { return 99; } e + 1 }; return x; }
			function main(): i32 { return f(5); }`,
			6,
		},
		{
			// #4522: the early-exit path is taken — `return` escapes the block
			// AND the function. f(-1): return 99.
			"cf-conditional-return-taken",
			`function f(e: i32): i32 { var x: i32 = { if (e < 0) { return 99; } e + 1 }; return x; }
			function main(): i32 { return f(-1); }`,
			99,
		},
		{
			// #4522: a `break` inside a value-position block inside a loop
			// escapes to the loop. i=1,2,3 add; break at 4. s = 6.
			"cf-break-in-block",
			`function main(): i32 {
				var s: i32 = 0; var i: i32 = 0;
				while (i < 10) { i = i + 1; var d: i32 = { if (i == 4) { break; } i }; s = s + d; }
				return s;
			}`,
			6,
		},
		{
			// #4522: a `continue` inside a value-position block skips the rest of
			// the loop body. i=3 skipped: s = 1+2+4+5+6 = 18.
			"cf-continue-in-block",
			`function main(): i32 {
				var s: i32 = 0; var i: i32 = 0;
				while (i < 6) { i = i + 1; var d: i32 = { if (i == 3) { continue; } i }; s = s + d; }
				return s;
			}`,
			18,
		},
		{
			// #4522: a NO-TAIL block whose statements always exit — every path
			// `return`s, so the block is `never` (not a void-block E061). f(-1)
			// takes the first return → 1.
			"cf-no-tail-all-return-first",
			`function f(n: i32): i32 { var x: i32 = { if (n < 0) { return 1; } return 2; }; return x; }
			function main(): i32 { return f(-1); }`,
			1,
		},
		{
			// The fall-through return is taken. f(5) → 2.
			"cf-no-tail-all-return-second",
			`function f(n: i32): i32 { var x: i32 = { if (n < 0) { return 1; } return 2; }; return x; }
			function main(): i32 { return f(5); }`,
			2,
		},
		{
			// #4522: an if-EXPRESSION whose two arms both diverge — the whole
			// if is `never`, assignable to i32. f(5) → 2.
			"cf-if-expr-both-arms-diverge",
			`function f(n: i32): i32 { var x: i32 = if (n < 0) { return 1; } else { return 2; }; return x; }
			function main(): i32 { return f(5); }`,
			2,
		},
		{
			// #4522: a match arm that always exits, unified with a value arm.
			// f(0) hits the divergent arm → 100.
			"cf-match-arm-diverges",
			`function f(n: i32): i32 { var x: i32 = match (n) { 0 => { return 100; }, _ => { n * 2 } }; return x; }
			function main(): i32 { return f(0); }`,
			100,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errb := blockExprInterpExit(t, c.src)
			if code != c.want {
				t.Errorf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, c.want, out, errb)
			}
		})
	}
}

// Slice 2: block-expressions COMPILE + run correctly on all three native
// backends (wasm / x86-64 / arm64), checked differentially against the
// interpreter. This was the slice-1 reject test — flipped, like the `as?`
// downcast codegen PR flipped its reject test, to assert correct
// compilation + execution rather than a clean reject. `backendsAgree`
// (defined in dyn_trait_compiled_test.go) runs src on the interpreter and
// every compiled backend and asserts each compiled stdout matches the
// interpreter's, and that the interpreter's matches the expected string.

// An `if`-expression branch block whose value flows into a `var`, printed
// so each backend's stdout is differentially compared. The taken branch
// runs a leading `var k = e + 1;` and yields `k`; e=5 → 6.
func TestBlockExprCompiledIfBranch(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	var e = 5;
	var x: i32 = if (e > 0) { var k = e + 1; k } else { 0 };
	print("x=" + x.to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "x=6"))
}

// #4521: a general value-position block-expression (the RHS of a `var`, not
// an if/match branch) compiles + runs identically on every native backend.
// Leading statements then a trailing value; k*m = 12, and a string-tail block
// to exercise the RC-survives-exit-sweep path in value position too.
func TestBlockExprCompiledValuePosition(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	var n: i32 = { var k = 3; var m = 4; k * m };
	var s: string = { var a = "foo"; var b = "bar"; a + b };
	print("n=" + n.to_string() + " s=" + s);
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "n=12 s=foobar"))
}

// #4522: control-flow (return / break / continue) inside a value-position
// block-expression compiles + runs identically on every native backend. A
// conditional `return` escapes the function; a `break` / `continue` inside a
// block in a loop reaches the loop. The block's tail is produced only on the
// fall-through path.
func TestBlockExprCompiledControlFlow(t *testing.T) {
	src := `import "std/i32";
function f(e: i32): i32 { var x: i32 = { if (e < 0) { return 99; } e + 1 }; return x; }
function main(): i32 {
	var s: i32 = 0; var i: i32 = 0;
	while (i < 10) { i = i + 1; var d: i32 = { if (i == 4) { break; } i }; s = s + d; }
	print("a=" + f(5).to_string() + " b=" + f(-1).to_string() + " s=" + s.to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "a=6 b=99 s=6"))
}

// #4522: a NO-TAIL value-position block whose statements ALWAYS exit
// early has type `never` (bottom) — it never produces a value, so the
// enclosing store is unreachable and no tail is lowered. Exercises the
// general-block, if-expr-both-arms, and match-arm forms differentially
// across every native backend. f: both returns; g: if-expr both arms
// diverge; h: divergent match arm vs value arm.
func TestBlockExprCompiledControlFlowNoTail(t *testing.T) {
	src := `import "std/i32";
function f(n: i32): i32 { var x: i32 = { if (n < 0) { return 1; } return 2; }; return x; }
function g(n: i32): i32 { var x: i32 = if (n < 0) { return 10; } else { return 20; }; return x; }
function h(n: i32): i32 { var x: i32 = match (n) { 0 => { return 100; }, _ => { n * 2 } }; return x; }
function main(): i32 {
	print("f-1=" + f(-1).to_string() + " f5=" + f(5).to_string() +
		" g-1=" + g(-1).to_string() + " g5=" + g(5).to_string() +
		" h0=" + h(0).to_string() + " h3=" + h(3).to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "f-1=1 f5=2 g-1=10 g5=20 h0=100 h3=6"))
}

// A `match`-arm block-expression: arm 0 runs `var v = t + 10; v * 2`.
// t=0 → v=10 → 20.
func TestBlockExprCompiledMatchArm(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	var t = 0;
	var r: i32 = match (t) { 0 => { var v = t + 10; v * 2 }, _ => 0 };
	print("r=" + r.to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "r=20"))
}

// A STRING-producing block tail: `{ var s = a + b; s }`. The block-local
// `s` is a heap value; it's the block's result, so it must survive the
// function-exit dec sweep (its reference flows out to the printed
// result) — no double-free / leak-induced wrongness. Differential across
// all backends confirms RC correctness end to end.
func TestBlockExprCompiledStringTail(t *testing.T) {
	src := `function main(): i32 {
	var a = "foo";
	var b = "bar";
	var s: string = if (true) { var joined = a + b; joined } else { "" };
	print("s=" + s);
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "s=foobar"))
}

// Nested: a block-expr whose tail is itself an `if`-expression. The outer
// block runs `var base = 3;` then yields `if (base > 0) { base * 7 } else
// { -1 }` → 21.
func TestBlockExprCompiledNestedIfTail(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	var x: i32 = if (true) {
		var base = 3;
		if (base > 0) { base * 7 } else { 0 - 1 }
	} else { 0 };
	print("x=" + x.to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "x=21"))
}

// Nested: a block-expr whose tail is itself a `match`-expression, and
// whose tail-match has a block arm too. Exercises composition of the new
// lowering with the existing match-expr lowering.
func TestBlockExprCompiledNestedMatchTail(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	var sel = 1;
	var r: i32 = if (true) {
		var bump = 100;
		match (sel) {
			1 => { var hit = bump + 5; hit },
			_ => 0
		}
	} else { 0 };
	print("r=" + r.to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "r=105"))
}

// Block-locals don't leak and each block gets its own scope: the same
// name `k` is rebound in two sibling block-exprs without collision, and
// both contribute to the result (10 + 20 = 30). Confirms the
// shadowrename frame + per-slot allocation works through codegen.
func TestBlockExprCompiledLocalsConfined(t *testing.T) {
	src := `import "std/i32";
function main(): i32 {
	var a: i32 = if (true) { var k = 10; k } else { 0 };
	var b: i32 = if (true) { var k = 20; k } else { 0 };
	print("sum=" + (a + b).to_string());
	return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "sum=30"))
}
