// arm64 (aarch64) Linux end-to-end tests. The IR layer is
// shared with the wasm backend, but the assembly emit + Linux
// syscall numbers are arm64-specific. Each test SKIPs (rather
// than fails) when the cross-compiler or qemu-aarch64 isn't
// installed.
//
// Tests run the compiled binary under qemu-aarch64, which
// uses the host's Linux kernel via user-mode emulation. On
// real arm64 Linux hosts (Raspberry Pi 4+, AWS Graviton,
// etc.) the same binary runs natively without qemu.
package e2e

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// First arm64 e2e: `function main(): i32 { return 42; }`
// validates the toolchain end-to-end. Compiles via the
// new arm64 backend, links a static -nostdlib ELF with
// aarch64-linux-gnu-gcc, runs under qemu-aarch64, and
// confirms the kernel propagates main's return value
// through `exit_group` to qemu's exit code.
func TestArm64ExitCode(t *testing.T) {
	for _, want := range []int{0, 1, 42, 137, 250} {
		src := "function main(): i32 { return " + intToString(want) + "; }"
		_, code := compileAndRunArm64(t, src)
		if code != want {
			t.Errorf("return %d → exit = %d", want, code)
		}
	}
}

// Issue #4871 (arm64 arm of the x86 TestX86_64NativeMapFieldStructRebindIndirect):
// `var m = s.m.insert(...); return ISet { m: m }`, self-reassigned `s = iset_add(s,
// x)`, aliased the borrowed receiver's in-place Map buffer and corrupted it on the
// second wrap-insert (the issue reports arm64 corruption; here it hung). The
// StructLit clone now covers the var-indirected mutator result, so the shared-IR
// fix holds on arm64 too. Set size is 2.
func TestArm64MapFieldStructRebindIndirect(t *testing.T) {
	src := `
import "core/map";
struct ISet { m: Map[i32, i32] }
function iset_add(s: ISet, x: i32): ISet {
    var m: Map[i32, i32] = s.m.insert(x, 1);
    return ISet { m: m };
}
function main(): i32 {
    var m0: Map[i32, i32] = map_new(4);
    var s: ISet = ISet { m: m0 };
    s = iset_add(s, 10);
    s = iset_add(s, 20);
    s = iset_add(s, 10);
    return s.m.len();
}`
	if _, code := compileAndRunArm64(t, src); code != 2 {
		t.Errorf("indirect IntSet rebind (arm64) → exit = %d, want 2 (issue #4871)", code)
	}
}

// arm64 arithmetic + locals + function calls. Exercises the
// per-op switch's coverage of OpAdd / OpSub / OpMul, OpLoadLocal
// / OpStoreLocal, OpCallDirect for user-defined functions, and
// the AAPCS64 prologue/epilogue with parameter spilling.
func TestArm64Arithmetic(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 { return 2 + 3 * 4; }`, 14},
		{`function main(): i32 { return 100 - 7 * 8; }`, 44},
		{`function main(): i32 { return 100 / 7; }`, 14},
		{`function main(): i32 { return 100 % 7; }`, 2},
		{`function main(): i32 { var x: i32 = 5; var y: i32 = 7; return x * y; }`, 35},
		{`function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add(20, 22); }`, 42},
		{`function fib(n: i32): i32 {
    if (n <= 1) { return n; }
    return fib(n - 1) + fib(n - 2);
}
function main(): i32 { return fib(10); }`, 55},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// arm64 string literals + len(). String literals live in
// .rodata with a 4-byte little-endian length prefix; pointers
// the runtime carries are post-prefix (`.LStr_N` label points
// at .asciz data). `len(s)` reads `[ptr - 4]`.
func TestArm64StringLiteralLen(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 { var s: string = "hello"; return s.len(); }`, 5},
		{`function main(): i32 { return ("").len(); }`, 0},
		{`function main(): i32 { return ("hi\nthere").len(); }`, 8},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// arm64 f64 transcendentals (gcc-linked path). sin/cos/exp/log/pow
// have no hardware instruction on arm64, so they lower to calls into
// polynomial-approximation runtime helpers (range reduction + Horner
// minimax/Taylor), pulled in via the .rodata coefficient table.
// Tolerance contract — a few ulp, not bit-exact with the interp's Go
// math. Mirrors TestX86_64Transcendentals.
func TestArm64Transcendentals(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"sin_0", "function main(): i32 { return __sin_f64(0.0) as i32; }", 0},
		{"cos_0", "function main(): i32 { return __cos_f64(0.0) as i32; }", 1},
		{"sin_halfpi", "function main(): i32 { var r: f64 = __sin_f64(1.5707963267948966); if (r > 0.999 && r < 1.001) { return 7; } return 0; }", 7},
		{"cos_pi", "function main(): i32 { var r: f64 = __cos_f64(3.141592653589793); if (r > 0.0 - 1.001 && r < 0.0 - 0.999) { return 7; } return 0; }", 7},
		{"exp_0", "function main(): i32 { return __exp_f64(0.0) as i32; }", 1},
		{"exp_2", "function main(): i32 { return __exp_f64(2.0) as i32; }", 7},
		{"log_10", "function main(): i32 { return __log_f64(10.0) as i32; }", 2},
		{"exp_log_roundtrip", "function main(): i32 { var r: f64 = __log_f64(__exp_f64(3.0)); if (r > 2.999 && r < 3.001) { return 7; } return 0; }", 7},
		{"pow_int", "function main(): i32 { return __pow_f64(2.0, 5.0) as i32; }", 32},
		{"pow_3_2", "function main(): i32 { return __pow_f64(3.0, 2.0) as i32; }", 9},
		{"pow_sqrt", "function main(): i32 { var r: f64 = __pow_f64(2.0, 0.5); if (r > 1.41 && r < 1.42) { return 7; } return 0; }", 7},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, code := compileAndRunArm64(t, c.src)
			if code != c.want {
				t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
			}
		})
	}
}

// arm64 string concat. Pulls in __fern_alloc + __fern_memcpy
// + __fern_strcat — the entire string-runtime stack on the
// arm64 target.
func TestArm64StringConcat(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 {
    var s: string = "hello, " + "world!";
    return s.len();
}`, 13},
		{`function main(): i32 {
    var greeting: string = "good ";
    var name: string = "morning";
    return (greeting + name).len();
}`, 12},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// `type X = A | B | C;` unions on arm64 — the first cut
// desugars to a synthesised enum whose variants each carry
// the named struct as a single positional payload. Verifies
// the full pipeline: parser preserves the UnionDecl through
// modload's combine, checker registers + desugars, monomorph
// is a no-op on the synthesised enum, codegen lowers the
// match as it would for any enum.
func TestArm64Unions(t *testing.T) {
	src := `struct Add { l: i32, r: i32 }
struct Mul { l: i32, r: i32 }
struct Lit { v: i32 }

type Expr = Add | Mul | Lit;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Mul(m) => { return m.l * m.r; },
        Lit(l) => { return l.v; },
    }
}

function main(): i32 {
    var lhs: Expr = Add(Add { l: 2, r: 3 });
    var rhs: Expr = Lit(Lit { v: 4 });
    var prod: Expr = Mul(Mul { l: eval(lhs), r: eval(rhs) });
    return eval(prod);
}`
	_, code := compileAndRunArm64(t, src)
	if code != 20 {
		t.Errorf("got %d, want 20 ((2+3)*4)", code)
	}
}

// Parser-in-lang v4: identifier references — adds `Var { name }`
// as a primary expression alongside numeric literals. Closes the
// last gap before parsing real arithmetic programs that mention
// variables (`x + 1`, not just `3 + 1`).
//
// Tokenizer additions: TokIdent recognition (alpha + alphanumeric
// continuation), shared with the lexer-in-lang v6 surface.
// Parser additions: `parse_factor` branches on the kind tag —
// int → Num, ident → Var, paren → recurse, anything else → Err.
//
// Eval grows an environment, modelled as parallel `string[]` /
// `i32[]` arrays. Lookup is linear scan from the most recent
// binding so shadowing works for free (last-write-wins on
// the same name). Real-world compilers use a Map for this; the
// list-of-pairs shape keeps the test independent of Map runtime
// gaps in the script-mode interp.
//
// Error coverage: undefined variable surfaces a Result Err with
// the offending name in the message, mirroring how the real
// checker emits "unknown identifier x" diagnostics.
func TestArm64ParserV4WithVars(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num    { value: i32 }
struct Var    { name: string }
struct BinOp  { op: i32, left: Expr, right: Expr }
type Expr = Num | Var | BinOp;

struct ParseError { message: string, pos: i32 }
struct EvalError  { message: string }

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) {
                i = i + 1;
            }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function peek_kind(toks: Token[], pos: i32): i32 {
    match (toks[pos]) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function peek_punct(toks: Token[], pos: i32): i32 {
    match (toks[pos]) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}
function peek_int(toks: Token[], pos: i32): i32 {
    match (toks[pos]) { TokInt(t) => { return t.value; }, _ => { return 0; } }
}
function peek_ident(toks: Token[], pos: i32): string {
    match (toks[pos]) { TokIdent(t) => { return t.name; }, _ => { return ""; } }
}

function parse_factor(toks: Token[], cur: Cell[i32]): Result[Expr, ParseError] {
    var pos: i32 = cur.get();
    var k: i32 = peek_kind(toks, pos);
    if (k == 0) {
        var iv: i32 = peek_int(toks, pos);
        cur.set(pos + 1);
        var n: Expr = Num { value: iv };
        return Ok(n);
    }
    if (k == 1) {
        var name: string = peek_ident(toks, pos);
        cur.set(pos + 1);
        var ve: Expr = Var { name: name };
        return Ok(ve);
    }
    if (k == 3) {
        return Err(ParseError { message: "unexpected end of input", pos: pos });
    }
    var p: i32 = peek_punct(toks, pos);
    if (p != 40) {
        return Err(ParseError { message: "expected number, ident, or paren", pos: pos });
    }
    cur.set(pos + 1);
    var inner_r: Result[Expr, ParseError] = parse_expr(toks, cur);
    match (inner_r) {
        Err(e) => { return Err(e); },
        Ok(inner_e) => {
            var ep: i32 = cur.get();
            if (peek_kind(toks, ep) != 2 || peek_punct(toks, ep) != 41) {
                return Err(ParseError { message: "missing close paren", pos: ep });
            }
            cur.set(ep + 1);
            return Ok(inner_e);
        },
    }
}

function parse_term(toks: Token[], cur: Cell[i32]): Result[Expr, ParseError] {
    var lhs_r: Result[Expr, ParseError] = parse_factor(toks, cur);
    match (lhs_r) {
        Err(e) => { return Err(e); },
        Ok(lhs_e) => {
            var lhs: Expr = lhs_e;
            while (true) {
                var pos: i32 = cur.get();
                if (peek_kind(toks, pos) != 2) { return Ok(lhs); }
                var op: i32 = peek_punct(toks, pos);
                if (op != 42 && op != 47) { return Ok(lhs); }
                cur.set(pos + 1);
                var rhs_r: Result[Expr, ParseError] = parse_factor(toks, cur);
                match (rhs_r) {
                    Err(e) => { return Err(e); },
                    Ok(rhs_e) => {
                        lhs = BinOp { op: op, left: lhs, right: rhs_e };
                    },
                }
            }
            return Ok(lhs);
        },
    }
}

function parse_expr(toks: Token[], cur: Cell[i32]): Result[Expr, ParseError] {
    var lhs_r: Result[Expr, ParseError] = parse_term(toks, cur);
    match (lhs_r) {
        Err(e) => { return Err(e); },
        Ok(lhs_e) => {
            var lhs: Expr = lhs_e;
            while (true) {
                var pos: i32 = cur.get();
                if (peek_kind(toks, pos) != 2) { return Ok(lhs); }
                var op: i32 = peek_punct(toks, pos);
                if (op != 43 && op != 45) { return Ok(lhs); }
                cur.set(pos + 1);
                var rhs_r: Result[Expr, ParseError] = parse_term(toks, cur);
                match (rhs_r) {
                    Err(e) => { return Err(e); },
                    Ok(rhs_e) => {
                        lhs = BinOp { op: op, left: lhs, right: rhs_e };
                    },
                }
            }
            return Ok(lhs);
        },
    }
}

function lookup(names: string[], values: i32[], name: string): Result[i32, EvalError] {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) { return Ok(values[i]); }
        i = i - 1;
    }
    return Err(EvalError { message: "unknown identifier: " + name });
}

function eval_expr(e: Expr, names: string[], values: i32[]): Result[i32, EvalError] {
    match (e) {
        Num(n) => { return Ok(n.value); },
        Var(v) => { return lookup(names, values, v.name); },
        BinOp(b) => {
            var lr: Result[i32, EvalError] = eval_expr(b.left, names, values);
            match (lr) {
                Err(le) => { return Err(le); },
                Ok(l) => {
                    var rr: Result[i32, EvalError] = eval_expr(b.right, names, values);
                    match (rr) {
                        Err(re) => { return Err(re); },
                        Ok(r) => {
                            if (b.op == 43) { return Ok(l + r); }
                            if (b.op == 45) { return Ok(l - r); }
                            if (b.op == 42) { return Ok(l * r); }
                            return Ok(l / r);
                        },
                    }
                },
            }
        },
    }
}

// Compose parse + eval into one helper so the success arm only
// names one local — keeps main below the wasm sibling-scope
// duplicate-locals threshold without renumbering every binding.
// Returns -1 on parse error so the caller can distinguish from a
// real eval result (a parse error during a self-host test is a
// test bug, not a thing to surface).
function parse_and_eval(src: string, names: string[], values: i32[]): Result[i32, EvalError] {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var pr: Result[Expr, ParseError] = parse_expr(toks, cur);
    match (pr) {
        Err(pe) => { return Err(EvalError { message: "parse failed: " + pe.message }); },
        Ok(e) => { return eval_expr(e, names, values); },
    }
}

function main(): i32 {
    var names: string[] = ["x", "y", "z"];
    var values: i32[] = [3, 4, 10];

    // Bare variable reference: x = 3.
    var r1: Result[i32, EvalError] = parse_and_eval("x", names, values);
    match (r1) {
        Err(_) => { return 1; },
        Ok(n1) => { if (n1 != 3) { return 2; } },
    }

    // Variable + literal with precedence: x + y * 2 = 3 + 8 = 11.
    var r2: Result[i32, EvalError] = parse_and_eval("x + y * 2", names, values);
    match (r2) {
        Err(_) => { return 3; },
        Ok(n2) => { if (n2 != 11) { return 4; } },
    }

    // Parenthesised expression with vars: (x + y) * z = 70.
    var r3: Result[i32, EvalError] = parse_and_eval("(x + y) * z", names, values);
    match (r3) {
        Err(_) => { return 5; },
        Ok(n3) => { if (n3 != 70) { return 6; } },
    }

    // Undefined variable surfaces a clean Eval error.
    var r4: Result[i32, EvalError] = parse_and_eval("missing + 1", names, values);
    match (r4) {
        Ok(_) => { return 7; },
        Err(ee) => { if (!ee.message.contains("missing")) { return 8; } },
    }

    // Shadowing: the latest binding wins.
    var snames: string[] = ["x", "x"];
    var svalues: i32[] = [1, 99];
    var r5: Result[i32, EvalError] = parse_and_eval("x", snames, svalues);
    match (r5) {
        Err(_) => { return 9; },
        Ok(n5) => { if (n5 != 99) { return 10; } },
    }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (parser v4 with vars)", code)
	}
}

// Parser-in-lang v3: error handling via `Result[Expr, ParseError]`.
// Replaces the bare-Expr returns from PR #402 with explicit
// Result propagation through `parse_factor` / `parse_term` /
// `parse_expr`. The struct-typed Err payload (`ParseError {
// message, pos }`) exercises a pair-form-return path that
// previously had a layout bug — see this PR's prelude / IR
// fix for "mixed-width Result variants now route through
// heap-box rebox".
//
// Errors covered:
//   - Empty input: "unexpected end of input"
//   - Unclosed paren: "missing close paren"
//   - Missing operand: "expected number or paren"
//
// Successes still parse correctly under the new Result-shaped
// control flow.
func TestArm64ParserV3WithResult(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokPunct | TokEof;

struct Num   { value: i32 }
struct BinOp { op: i32, left: Expr, right: Expr }
type Expr = Num | BinOp;

struct ParseError { message: string, pos: i32 }

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function peek_kind(toks: Token[], pos: i32): i32 {
    match (toks[pos]) {
        TokInt(_) => { return 0; },
        TokPunct(_) => { return 1; },
        TokEof(_) => { return 2; },
    }
}
function peek_punct(toks: Token[], pos: i32): i32 {
    match (toks[pos]) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}
function peek_int(toks: Token[], pos: i32): i32 {
    match (toks[pos]) { TokInt(t) => { return t.value; }, _ => { return 0; } }
}

function parse_factor(toks: Token[], cur: Cell[i32]): Result[Expr, ParseError] {
    var pos: i32 = cur.get();
    var k: i32 = peek_kind(toks, pos);
    if (k == 0) {
        var v: i32 = peek_int(toks, pos);
        cur.set(pos + 1);
        var n: Expr = Num { value: v };
        return Ok(n);
    }
    if (k == 2) {
        return Err(ParseError { message: "unexpected end of input", pos: pos });
    }
    var p: i32 = peek_punct(toks, pos);
    if (p != 40) {
        return Err(ParseError { message: "expected number or paren", pos: pos });
    }
    cur.set(pos + 1);
    var inner_r: Result[Expr, ParseError] = parse_expr(toks, cur);
    match (inner_r) {
        Err(e) => { return Err(e); },
        Ok(inner_e) => {
            var ep: i32 = cur.get();
            if (peek_kind(toks, ep) != 1 || peek_punct(toks, ep) != 41) {
                return Err(ParseError { message: "missing close paren", pos: ep });
            }
            cur.set(ep + 1);
            return Ok(inner_e);
        },
    }
}

function parse_term(toks: Token[], cur: Cell[i32]): Result[Expr, ParseError] {
    var lhs_r: Result[Expr, ParseError] = parse_factor(toks, cur);
    match (lhs_r) {
        Err(e) => { return Err(e); },
        Ok(lhs_e) => {
            var lhs: Expr = lhs_e;
            while (true) {
                var pos: i32 = cur.get();
                if (peek_kind(toks, pos) != 1) { return Ok(lhs); }
                var op: i32 = peek_punct(toks, pos);
                if (op != 42 && op != 47) { return Ok(lhs); }
                cur.set(pos + 1);
                var rhs_r: Result[Expr, ParseError] = parse_factor(toks, cur);
                match (rhs_r) {
                    Err(e) => { return Err(e); },
                    Ok(rhs_e) => {
                        lhs = BinOp { op: op, left: lhs, right: rhs_e };
                    },
                }
            }
            return Ok(lhs);
        },
    }
}

function parse_expr(toks: Token[], cur: Cell[i32]): Result[Expr, ParseError] {
    var lhs_r: Result[Expr, ParseError] = parse_term(toks, cur);
    match (lhs_r) {
        Err(e) => { return Err(e); },
        Ok(lhs_e) => {
            var lhs: Expr = lhs_e;
            while (true) {
                var pos: i32 = cur.get();
                if (peek_kind(toks, pos) != 1) { return Ok(lhs); }
                var op: i32 = peek_punct(toks, pos);
                if (op != 43 && op != 45) { return Ok(lhs); }
                cur.set(pos + 1);
                var rhs_r: Result[Expr, ParseError] = parse_term(toks, cur);
                match (rhs_r) {
                    Err(e) => { return Err(e); },
                    Ok(rhs_e) => {
                        lhs = BinOp { op: op, left: lhs, right: rhs_e };
                    },
                }
            }
            return Ok(lhs);
        },
    }
}

function eval_expr(e: Expr): i32 {
    match (e) {
        Num(n) => { return n.value; },
        BinOp(b) => {
            var l: i32 = eval_expr(b.left);
            var r: i32 = eval_expr(b.right);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            return l / r;
        },
    }
}

function interp(src: string): Result[i32, ParseError] {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var ast_r: Result[Expr, ParseError] = parse_expr(toks, cur);
    match (ast_r) {
        Err(e) => { return Err(e); },
        Ok(ast) => { return Ok(eval_expr(ast)); },
    }
}

function main(): i32 {
    var ok1: Result[i32, ParseError] = interp("1 + 2 * 3");
    match (ok1) {
        Ok(v) => { if (v != 7) { return 1; } },
        Err(_) => { return 2; },
    }
    var ok2: Result[i32, ParseError] = interp("(1 + 2) * 3");
    match (ok2) {
        Ok(v) => { if (v != 9) { return 3; } },
        Err(_) => { return 4; },
    }
    var err1: Result[i32, ParseError] = interp("");
    match (err1) {
        Ok(_) => { return 5; },
        Err(e) => { if (!e.message.contains("unexpected end")) { return 6; } },
    }
    var err2: Result[i32, ParseError] = interp("(1 + 2");
    match (err2) {
        Ok(_) => { return 7; },
        Err(e) => { if (!e.message.contains("close paren")) { return 8; } },
    }
    var err3: Result[i32, ParseError] = interp("1 + +");
    match (err3) {
        Ok(_) => { return 9; },
        Err(e) => { if (!e.message.contains("expected number")) { return 10; } },
    }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (parser v3 with Result)", code)
	}
}

// Regression for the void-call phantom-push codegen bug.
// `arr.append(v)` inlines an internal `__memcpy` call which is
// VOID-RETURNING in lang's type system. The native backends
// (arm64 + x86_64) previously pushed rax/x0 unconditionally
// after every `bl`/`call`, leaving a phantom slot from the
// runtime helper's stale return register. The phantom slot
// got consumed by the surrounding OpStore — in this case the
// struct-lit field initialiser — corrupting the field address
// and crashing on the subsequent store. Fixed by gating the
// post-call push on the callee's return type (void → no push).
// This shape (struct lit containing arr.append(...)) is the
// minimal trigger.
func TestArm64StructLitWithArrayPush(t *testing.T) {
	src := `struct State { vals: i32[] }

function main(): i32 {
    var s: State = State { vals: [10] };
    s = State { vals: s.vals.append(42) };
    return s.vals[0] + s.vals[1];
}`
	_, code := compileAndRunArm64(t, src)
	if code != 52 {
		t.Errorf("got %d, want 52 (10 + 42)", code)
	}
}

// Stmt-interp v4 — `else` branches in if-statements. v3 (#426)
// shipped bare if-then; v4 adds the optional else arm + the
// `else if` chain pattern via right-associative parsing.
//
// Grammar:
//
//	if_stmt ::= "if" "(" expr ")" "{" stmt* "}"
//	          | "if" "(" expr ")" "{" stmt* "}" "else" "{" stmt* "}"
//	          | "if" "(" expr ")" "{" stmt* "}" "else" if_stmt
//
// The third arm — `else if` — desugars at parse time into a
// nested IfSt as the else-branch's only statement. That gives
// us C-style if/else-if/else chains for free.
//
// Eval: if cond → run thn body; else → run els body. State
// threads the same way the if-then version did.
//
// Tests:
//  1. Bare if-then (v3 sanity, unchanged).
//  2. if/else dispatch — select between two return values.
//  3. abs(x) via if/else — return -x or x.
//  4. max(a, b) via if/else (using a comparison in cond).
//  5. else-if chain — classify a number into three buckets.
//  6. Nested if/else inside a function — recursive function
//     that branches.
func TestArm64StmtInterpElse(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num    { value: i32 }
struct Var    { name: string }
struct BinOp  { op: i32, left: Expr, right: Expr }
struct Call   { name: string, args: Expr[] }
type Expr = Num | Var | BinOp | Call;

struct VarDecl  { name: string, value: Expr }
struct Assign   { name: string, value: Expr }
struct Return   { value: Expr }
struct WhileSt  { cond: Expr, body: Stmt[] }
struct IfSt     { cond: Expr, thn: Stmt[], els: Stmt[] }
struct ExprSt   { value: Expr }
type Stmt = VarDecl | Assign | Return | WhileSt | IfSt | ExprSt;

struct FnDef { name: string, params: string[], body: Stmt[] }

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else if (b == 61 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1001 });
            i = i + 2;
        } else if (b == 33 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1002 });
            i = i + 2;
        } else if (b == 60 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1004 });
            i = i + 2;
        } else if (b == 62 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1006 });
            i = i + 2;
        } else if (b == 60) {
            toks = toks.append(TokPunct { ch: 1003 });
            i = i + 1;
        } else if (b == 62) {
            toks = toks.append(TokPunct { ch: 1005 });
            i = i + 1;
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function is_comp_op(op: i32): boolean {
    return op == 1001 || op == 1002 || op == 1003 ||
           op == 1004 || op == 1005 || op == 1006;
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        var name: string = tok_ident_name(toks[pos]);
        cur.set(pos + 1);
        if (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 40) {
            cur.set(cur.get() + 1);
            var args: Expr[] = [];
            if (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 41) {
                cur.set(cur.get() + 1);
                return Call { name: name, args: args };
            }
            args = args.append(parse_expr(toks, cur));
            while (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 44) {
                cur.set(cur.get() + 1);
                args = args.append(parse_expr(toks, cur));
            }
            cur.set(cur.get() + 1);
            return Call { name: name, args: args };
        }
        return Var { name: name };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_expr(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_expr(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_arith(toks, cur);
    var pos: i32 = cur.get();
    if (tok_kind(toks[pos]) != 2) { return lhs; }
    var op: i32 = tok_punct_ch(toks[pos]);
    if (!is_comp_op(op)) { return lhs; }
    cur.set(pos + 1);
    var rhs: Expr = parse_arith(toks, cur);
    return BinOp { op: op, left: lhs, right: rhs };
}

function expect_kw(toks: Token[], pos: i32, kw: string): boolean {
    return tok_kind(toks[pos]) == 1 && tok_ident_name(toks[pos]) == kw;
}

function parse_stmt(toks: Token[], cur: Cell[i32]): Stmt {
    var name: string = "";
    var value: Expr = Num { value: 0 };
    var cond: Expr = Num { value: 0 };
    var body: Stmt[] = [];
    if (expect_kw(toks, cur.get(), "var")) {
        cur.set(cur.get() + 1);
        name = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);
        value = parse_expr(toks, cur);
        cur.set(cur.get() + 1);
        return VarDecl { name: name, value: value };
    }
    if (expect_kw(toks, cur.get(), "return")) {
        cur.set(cur.get() + 1);
        value = parse_expr(toks, cur);
        cur.set(cur.get() + 1);
        return Return { value: value };
    }
    if (expect_kw(toks, cur.get(), "while")) {
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);
        cond = parse_expr(toks, cur);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);
        body = [];
        while (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 125) {
            body = body.append(parse_stmt(toks, cur));
        }
        cur.set(cur.get() + 1);
        return WhileSt { cond: cond, body: body };
    }
    if (expect_kw(toks, cur.get(), "if")) {
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // (
        cond = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // )
        cur.set(cur.get() + 1);   // {
        var thn: Stmt[] = [];
        while (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 125) {
            thn = thn.append(parse_stmt(toks, cur));
        }
        cur.set(cur.get() + 1);   // }
        // Optional else / else if.
        var els: Stmt[] = [];
        if (expect_kw(toks, cur.get(), "else")) {
            cur.set(cur.get() + 1);
            if (expect_kw(toks, cur.get(), "if")) {
                // else if — recursively parse the nested if as
                // the sole stmt of the else body.
                els = els.append(parse_stmt(toks, cur));
            } else {
                cur.set(cur.get() + 1);   // {
                while (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 125) {
                    els = els.append(parse_stmt(toks, cur));
                }
                cur.set(cur.get() + 1);   // }
            }
        }
        return IfSt { cond: cond, thn: thn, els: els };
    }
    if (tok_kind(toks[cur.get()]) == 1 && tok_kind(toks[cur.get() + 1]) == 2 && tok_punct_ch(toks[cur.get() + 1]) == 61) {
        name = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);
        value = parse_expr(toks, cur);
        cur.set(cur.get() + 1);
        return Assign { name: name, value: value };
    }
    value = parse_expr(toks, cur);
    cur.set(cur.get() + 1);
    return ExprSt { value: value };
}

struct Program { fns: FnDef[], main_stmts: Stmt[] }

function parse_program(src: string): Program {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var fns: FnDef[] = [];
    while (expect_kw(toks, cur.get(), "fn")) {
        cur.set(cur.get() + 1);
        var name: string = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);
        var params: string[] = [];
        if (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 41) {
            params = params.append(tok_ident_name(toks[cur.get()]));
            cur.set(cur.get() + 1);
            while (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 44) {
                cur.set(cur.get() + 1);
                params = params.append(tok_ident_name(toks[cur.get()]));
                cur.set(cur.get() + 1);
            }
        }
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);
        var body: Stmt[] = [];
        while (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 125) {
            body = body.append(parse_stmt(toks, cur));
        }
        cur.set(cur.get() + 1);
        fns = fns.append(FnDef { name: name, params: params, body: body });
    }
    var main_stmts: Stmt[] = [];
    while (tok_kind(toks[cur.get()]) != 3) {
        main_stmts = main_stmts.append(parse_stmt(toks, cur));
    }
    return Program { fns: fns, main_stmts: main_stmts };
}

function bool_to_i32(b: boolean): i32 { if (b) { return 1; } return 0; }

function find_fn(fns: FnDef[], name: string): i32 {
    var i: i32 = 0;
    while (i < fns.len()) {
        if (fns[i].name == name) { return i; }
        i = i + 1;
    }
    return -1;
}

function env_lookup(names: string[], values: i32[], name: string): i32 {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) { return values[i]; }
        i = i - 1;
    }
    return 0;
}

function env_assign(names: string[], values: i32[], name: string, v: i32): i32[] {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) {
            var out: i32[] = [];
            var j: i32 = 0;
            while (j < values.len()) {
                if (j == i) { out = out.append(v); }
                else { out = out.append(values[j]); }
                j = j + 1;
            }
            return out;
        }
        i = i - 1;
    }
    return values;
}

function eval_expr(e: Expr, names: string[], values: i32[], fns: FnDef[]): i32 {
    match (e) {
        Num(n) => { return n.value; },
        Var(v) => { return env_lookup(names, values, v.name); },
        BinOp(b) => {
            var l: i32 = eval_expr(b.left, names, values, fns);
            var r: i32 = eval_expr(b.right, names, values, fns);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            if (b.op == 47) { return l / r; }
            if (b.op == 1001) { return bool_to_i32(l == r); }
            if (b.op == 1002) { return bool_to_i32(l != r); }
            if (b.op == 1003) { return bool_to_i32(l < r); }
            if (b.op == 1004) { return bool_to_i32(l <= r); }
            if (b.op == 1005) { return bool_to_i32(l > r); }
            return bool_to_i32(l >= r);
        },
        Call(c) => {
            var idx: i32 = find_fn(fns, c.name);
            var fresh_n: string[] = [];
            var fresh_v: i32[] = [];
            var i: i32 = 0;
            while (i < c.args.len()) {
                fresh_n = fresh_n.append(fns[idx].params[i]);
                fresh_v = fresh_v.append(eval_expr(c.args[i], names, values, fns));
                i = i + 1;
            }
            var inner: StepState = run_block(fns[idx].body, fresh_n, fresh_v, fns);
            return inner.result;
        },
    }
}

struct StepState {
    names: string[],
    values: i32[],
    done: boolean,
    result: i32,
}

function eval_stmt(s: Stmt, state: StepState, fns: FnDef[]): StepState {
    var v: i32 = 0;
    var i: i32 = 0;
    match (s) {
        VarDecl(vd) => {
            v = eval_expr(vd.value, state.names, state.values, fns);
            return StepState {
                names: state.names.append(vd.name),
                values: state.values.append(v),
                done: state.done,
                result: state.result,
            };
        },
        Assign(a) => {
            v = eval_expr(a.value, state.names, state.values, fns);
            return StepState {
                names: state.names,
                values: env_assign(state.names, state.values, a.name, v),
                done: state.done,
                result: state.result,
            };
        },
        Return(r) => {
            v = eval_expr(r.value, state.names, state.values, fns);
            return StepState {
                names: state.names,
                values: state.values,
                done: true,
                result: v,
            };
        },
        WhileSt(w) => {
            while (!state.done && eval_expr(w.cond, state.names, state.values, fns) != 0) {
                i = 0;
                while (i < w.body.len() && !state.done) {
                    state = eval_stmt(w.body[i], state, fns);
                    i = i + 1;
                }
            }
            return state;
        },
        IfSt(it) => {
            if (eval_expr(it.cond, state.names, state.values, fns) != 0) {
                i = 0;
                while (i < it.thn.len() && !state.done) {
                    state = eval_stmt(it.thn[i], state, fns);
                    i = i + 1;
                }
            } else {
                i = 0;
                while (i < it.els.len() && !state.done) {
                    state = eval_stmt(it.els[i], state, fns);
                    i = i + 1;
                }
            }
            return state;
        },
        ExprSt(es) => {
            v = eval_expr(es.value, state.names, state.values, fns);
            return state;
        },
    }
}

function run_block(stmts: Stmt[], names: string[], values: i32[], fns: FnDef[]): StepState {
    var state: StepState = StepState {
        names: names,
        values: values,
        done: false,
        result: 0,
    };
    var i: i32 = 0;
    while (i < stmts.len() && !state.done) {
        state = eval_stmt(stmts[i], state, fns);
        i = i + 1;
    }
    return state;
}

function run(src: string): i32 {
    var p: Program = parse_program(src);
    var empty_n: string[] = [];
    var empty_v: i32[] = [];
    return run_block(p.main_stmts, empty_n, empty_v, p.fns).result;
}

function main(): i32 {
    // if/else dispatch — different return per branch.
    if (run("if (1) { return 10; } else { return 20; }") != 10) { return 1; }
    if (run("if (0) { return 10; } else { return 20; }") != 20) { return 2; }

    // abs via if/else inside a function.
    if (run("fn abs(x) { if (x < 0) { return 0 - x; } else { return x; } } return abs(7);") != 7) { return 3; }
    if (run("fn abs(x) { if (x < 0) { return 0 - x; } else { return x; } } return abs(0 - 7);") != 7) { return 4; }

    // max(a, b) via if/else.
    if (run("fn max(a, b) { if (a > b) { return a; } else { return b; } } return max(3, 9);") != 9) { return 5; }
    if (run("fn max(a, b) { if (a > b) { return a; } else { return b; } } return max(15, 4);") != 15) { return 6; }

    // else if chain — bucket a value into {<0, 0, >0}.
    // Returns -1 / 0 / 1 respectively. Comparison literal
    // written as 0 - 1 since the toy grammar has no unary minus.
    if (run("fn sign(x) { if (x < 0) { return 0 - 1; } else if (x == 0) { return 0; } else { return 1; } } return sign(0 - 5);") != 0 - 1) { return 7; }
    if (run("fn sign(x) { if (x < 0) { return 0 - 1; } else if (x == 0) { return 0; } else { return 1; } } return sign(0);") != 0) { return 8; }
    if (run("fn sign(x) { if (x < 0) { return 0 - 1; } else if (x == 0) { return 0; } else { return 1; } } return sign(5);") != 1) { return 9; }

    // Nested if/else inside recursion — fib via the textbook
    // recurrence. fib(0)=0, fib(1)=1, fib(n)=fib(n-1)+fib(n-2).
    if (run("fn fib(n) { if (n == 0) { return 0; } else if (n == 1) { return 1; } else { return fib(n - 1) + fib(n - 2); } } return fib(10);") != 55) { return 10; }

    // Empty else body — explicitly testing the els = [] path.
    if (run("var x = 5; if (x > 0) { x = 100; } else { } return x;") != 100) { return 11; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stmt-interp v4 else)", code)
	}
}

// Stmt-interp v3 — function declarations + calls. The natural
// conclusion of the stmt-interp arc. v1 (#424) shipped var /
// assign / return; v2 (#425) added while + comparisons. v3
// closes the gap to "real procedural language" — top-level
// functions, multi-arg calls, recursion, mutual recursion. The
// in-lang interpreter can now host iterative_factorial,
// recursive_factorial, fibonacci, gcd, etc. — the same shape
// every introductory CS textbook ends on.
//
// Grammar additions:
//
//	program ::= fn_def* stmt*
//	fn_def  ::= "fn" name "(" param ("," param)* ")" "{" stmt* "}"
//	expr    ::= ... | name "(" expr ("," expr)* ")"
//
// Call disambiguates at parse time on single-token lookahead
// (matching interp v6/v7's split between Var and Call).
// Function bodies are full statement lists — they can declare
// locals, mutate them via assignment, loop with while, and
// return.
//
// Recursion works because the function table is built before
// main runs. Mutual recursion works for the same reason.
//
// Tests:
//  1. Simple zero-arg function (`fn answer() { return 42; }`).
//  2. Single-arg function (`fn double(x) { return x * 2; }`).
//  3. Recursive factorial via if-then return — proves call
//     machinery handles deep recursion.
//  4. Iterative factorial — function body with while loop +
//     local var + return.
//  5. Multi-arg gcd via Euclidean algorithm — two-arg call,
//     recursive.
//  6. Mutual recursion (is_even / is_odd) — confirms the
//     function table is closed-over.
//  7. Function compose at top-level — call from main flow.
func TestArm64StmtInterpFunctions(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num    { value: i32 }
struct Var    { name: string }
struct BinOp  { op: i32, left: Expr, right: Expr }
struct Call   { name: string, args: Expr[] }
type Expr = Num | Var | BinOp | Call;

struct VarDecl  { name: string, value: Expr }
struct Assign   { name: string, value: Expr }
struct Return   { value: Expr }
struct WhileSt  { cond: Expr, body: Stmt[] }
struct IfSt     { cond: Expr, body: Stmt[] }
struct ExprSt   { value: Expr }
type Stmt = VarDecl | Assign | Return | WhileSt | IfSt | ExprSt;

struct FnDef { name: string, params: string[], body: Stmt[] }

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else if (b == 61 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1001 });   // ==
            i = i + 2;
        } else if (b == 33 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1002 });   // !=
            i = i + 2;
        } else if (b == 60 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1004 });   // <=
            i = i + 2;
        } else if (b == 62 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1006 });   // >=
            i = i + 2;
        } else if (b == 60) {
            toks = toks.append(TokPunct { ch: 1003 });
            i = i + 1;
        } else if (b == 62) {
            toks = toks.append(TokPunct { ch: 1005 });
            i = i + 1;
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function is_comp_op(op: i32): boolean {
    return op == 1001 || op == 1002 || op == 1003 ||
           op == 1004 || op == 1005 || op == 1006;
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        var name: string = tok_ident_name(toks[pos]);
        cur.set(pos + 1);
        // Call vs Var disambiguation on '(' lookahead.
        if (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 40) {
            cur.set(cur.get() + 1);   // skip '('
            var args: Expr[] = [];
            if (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 41) {
                cur.set(cur.get() + 1);
                return Call { name: name, args: args };
            }
            args = args.append(parse_expr(toks, cur));
            while (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 44) {
                cur.set(cur.get() + 1);   // skip ','
                args = args.append(parse_expr(toks, cur));
            }
            cur.set(cur.get() + 1);   // skip ')'
            return Call { name: name, args: args };
        }
        return Var { name: name };
    }
    cur.set(pos + 1);   // skip '('
    var inner: Expr = parse_expr(toks, cur);
    cur.set(cur.get() + 1);   // skip ')'
    return inner;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_expr(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_arith(toks, cur);
    var pos: i32 = cur.get();
    if (tok_kind(toks[pos]) != 2) { return lhs; }
    var op: i32 = tok_punct_ch(toks[pos]);
    if (!is_comp_op(op)) { return lhs; }
    cur.set(pos + 1);
    var rhs: Expr = parse_arith(toks, cur);
    return BinOp { op: op, left: lhs, right: rhs };
}

function expect_kw(toks: Token[], pos: i32, kw: string): boolean {
    return tok_kind(toks[pos]) == 1 && tok_ident_name(toks[pos]) == kw;
}

function parse_stmt(toks: Token[], cur: Cell[i32]): Stmt {
    var name: string = "";
    var value: Expr = Num { value: 0 };
    if (expect_kw(toks, cur.get(), "var")) {
        cur.set(cur.get() + 1);
        name = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip '='
        value = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip ';'
        return VarDecl { name: name, value: value };
    }
    if (expect_kw(toks, cur.get(), "return")) {
        cur.set(cur.get() + 1);
        value = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip ';'
        return Return { value: value };
    }
    // Hoist cond / body across the while / if arms — wasm
    // rejects sibling-scope duplicate locals.
    var cond: Expr = Num { value: 0 };
    var body: Stmt[] = [];
    if (expect_kw(toks, cur.get(), "while")) {
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip '('
        cond = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip ')'
        cur.set(cur.get() + 1);   // skip '{'
        body = [];
        while (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 125) {
            body = body.append(parse_stmt(toks, cur));
        }
        cur.set(cur.get() + 1);   // skip '}'
        return WhileSt { cond: cond, body: body };
    }
    if (expect_kw(toks, cur.get(), "if")) {
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip '('
        cond = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip ')'
        cur.set(cur.get() + 1);   // skip '{'
        body = [];
        while (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 125) {
            body = body.append(parse_stmt(toks, cur));
        }
        cur.set(cur.get() + 1);   // skip '}'
        return IfSt { cond: cond, body: body };
    }
    // Lookahead: ident '=' is assignment; ident '(' is an
    // expression-statement (call). Anything else here is
    // bare expression-as-statement (covers stray calls).
    if (tok_kind(toks[cur.get()]) == 1 && tok_kind(toks[cur.get() + 1]) == 2 && tok_punct_ch(toks[cur.get() + 1]) == 61) {
        name = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip '='
        value = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip ';'
        return Assign { name: name, value: value };
    }
    value = parse_expr(toks, cur);
    cur.set(cur.get() + 1);   // skip ';'
    return ExprSt { value: value };
}

struct Program { fns: FnDef[], main_stmts: Stmt[] }

function parse_program(src: string): Program {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var fns: FnDef[] = [];
    while (expect_kw(toks, cur.get(), "fn")) {
        cur.set(cur.get() + 1);
        var name: string = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip '('
        var params: string[] = [];
        if (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 41) {
            params = params.append(tok_ident_name(toks[cur.get()]));
            cur.set(cur.get() + 1);
            while (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 44) {
                cur.set(cur.get() + 1);   // skip ','
                params = params.append(tok_ident_name(toks[cur.get()]));
                cur.set(cur.get() + 1);
            }
        }
        cur.set(cur.get() + 1);   // skip ')'
        cur.set(cur.get() + 1);   // skip '{'
        var body: Stmt[] = [];
        while (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 125) {
            body = body.append(parse_stmt(toks, cur));
        }
        cur.set(cur.get() + 1);   // skip '}'
        fns = fns.append(FnDef { name: name, params: params, body: body });
    }
    var main_stmts: Stmt[] = [];
    while (tok_kind(toks[cur.get()]) != 3) {
        main_stmts = main_stmts.append(parse_stmt(toks, cur));
    }
    return Program { fns: fns, main_stmts: main_stmts };
}

function bool_to_i32(b: boolean): i32 { if (b) { return 1; } return 0; }

function find_fn(fns: FnDef[], name: string): i32 {
    var i: i32 = 0;
    while (i < fns.len()) {
        if (fns[i].name == name) { return i; }
        i = i + 1;
    }
    return -1;
}

function env_lookup(names: string[], values: i32[], name: string): i32 {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) { return values[i]; }
        i = i - 1;
    }
    return 0;
}

function env_assign(names: string[], values: i32[], name: string, v: i32): i32[] {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) {
            var out: i32[] = [];
            var j: i32 = 0;
            while (j < values.len()) {
                if (j == i) { out = out.append(v); }
                else { out = out.append(values[j]); }
                j = j + 1;
            }
            return out;
        }
        i = i - 1;
    }
    return values;
}

function eval_expr(e: Expr, names: string[], values: i32[], fns: FnDef[]): i32 {
    match (e) {
        Num(n) => { return n.value; },
        Var(v) => { return env_lookup(names, values, v.name); },
        BinOp(b) => {
            var l: i32 = eval_expr(b.left, names, values, fns);
            var r: i32 = eval_expr(b.right, names, values, fns);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            if (b.op == 47) { return l / r; }
            if (b.op == 1001) { return bool_to_i32(l == r); }
            if (b.op == 1002) { return bool_to_i32(l != r); }
            if (b.op == 1003) { return bool_to_i32(l < r); }
            if (b.op == 1004) { return bool_to_i32(l <= r); }
            if (b.op == 1005) { return bool_to_i32(l > r); }
            return bool_to_i32(l >= r);
        },
        Call(c) => {
            var idx: i32 = find_fn(fns, c.name);
            var fresh_n: string[] = [];
            var fresh_v: i32[] = [];
            var i: i32 = 0;
            while (i < c.args.len()) {
                fresh_n = fresh_n.append(fns[idx].params[i]);
                fresh_v = fresh_v.append(eval_expr(c.args[i], names, values, fns));
                i = i + 1;
            }
            // Run the function body as a statement block with
            // the fresh env. The result is whichever Return
            // fires first; if none fires the function falls off
            // the end and the result is whatever state.result
            // started at (0).
            var inner: StepState = run_block(fns[idx].body, fresh_n, fresh_v, fns);
            return inner.result;
        },
    }
}

struct StepState {
    names: string[],
    values: i32[],
    done: boolean,
    result: i32,
}

function eval_stmt(s: Stmt, state: StepState, fns: FnDef[]): StepState {
    var v: i32 = 0;
    var i: i32 = 0;   // hoisted: sibling-scope dups in WhileSt / IfSt
    match (s) {
        VarDecl(vd) => {
            v = eval_expr(vd.value, state.names, state.values, fns);
            return StepState {
                names: state.names.append(vd.name),
                values: state.values.append(v),
                done: state.done,
                result: state.result,
            };
        },
        Assign(a) => {
            v = eval_expr(a.value, state.names, state.values, fns);
            return StepState {
                names: state.names,
                values: env_assign(state.names, state.values, a.name, v),
                done: state.done,
                result: state.result,
            };
        },
        Return(r) => {
            v = eval_expr(r.value, state.names, state.values, fns);
            return StepState {
                names: state.names,
                values: state.values,
                done: true,
                result: v,
            };
        },
        WhileSt(w) => {
            while (!state.done && eval_expr(w.cond, state.names, state.values, fns) != 0) {
                i = 0;
                while (i < w.body.len() && !state.done) {
                    state = eval_stmt(w.body[i], state, fns);
                    i = i + 1;
                }
            }
            return state;
        },
        IfSt(it) => {
            if (eval_expr(it.cond, state.names, state.values, fns) != 0) {
                i = 0;
                while (i < it.body.len() && !state.done) {
                    state = eval_stmt(it.body[i], state, fns);
                    i = i + 1;
                }
            }
            return state;
        },
        ExprSt(es) => {
            // Evaluate for side effects (i.e. calls) — discard result.
            v = eval_expr(es.value, state.names, state.values, fns);
            return state;
        },
    }
}

function run_block(stmts: Stmt[], names: string[], values: i32[], fns: FnDef[]): StepState {
    var state: StepState = StepState {
        names: names,
        values: values,
        done: false,
        result: 0,
    };
    var i: i32 = 0;
    while (i < stmts.len() && !state.done) {
        state = eval_stmt(stmts[i], state, fns);
        i = i + 1;
    }
    return state;
}

function run(src: string): i32 {
    var p: Program = parse_program(src);
    var empty_n: string[] = [];
    var empty_v: i32[] = [];
    return run_block(p.main_stmts, empty_n, empty_v, p.fns).result;
}

function main(): i32 {
    // Zero-arg function.
    if (run("fn answer() { return 42; } return answer();") != 42) { return 1; }

    // Single-arg function.
    if (run("fn double(x) { return x * 2; } return double(21);") != 42) { return 2; }

    // Multi-arg function.
    if (run("fn add(a, b) { return a + b; } return add(3, 4);") != 7) { return 3; }

    // Recursive factorial.
    if (run("fn fact(n) { if (n == 0) { return 1; } return n * fact(n - 1); } return fact(5);") != 120) { return 4; }

    // Iterative factorial — function body uses while + locals.
    if (run("fn fact(n) { var f = 1; while (n > 0) { f = f * n; n = n - 1; } return f; } return fact(6);") != 720) { return 6; }

    // gcd via Euclidean — two-arg recursion.
    if (run("fn gcd(a, b) { if (b == 0) { return a; } return gcd(b, a - a / b * b); } return gcd(48, 18);") != 6) { return 7; }

    // Mutual recursion. is_even(0)=1; is_even(n)=is_odd(n-1).
    // is_odd(0)=0; is_odd(n)=is_even(n-1).
    if (run("fn is_even(n) { if (n == 0) { return 1; } return is_odd(n - 1); } fn is_odd(n) { if (n == 0) { return 0; } return is_even(n - 1); } return is_even(10);") != 1) { return 8; }

    // Top-level main flow can mix calls with statements.
    if (run("fn square(x) { return x * x; } var a = 5; var b = square(a); return b + 1;") != 26) { return 9; }

    // Composition — outer call's arg is itself a call.
    if (run("fn dbl(x) { return x + x; } fn sqr(x) { return x * x; } return dbl(sqr(3));") != 18) { return 10; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stmt-interp v3 functions)", code)
	}
}

// Stmt-interp v2 — while loops + comparison operators. v1
// (#424) shipped var / assign / return / linear flow. v2 adds
// the missing pieces that make the toy language actually
// Turing-complete at the statement level: a loop construct
// (so iterative algorithms work), and comparison operators
// (so loops can have meaningful exit conditions).
//
// Grammar additions:
//
//	stmt ::= ... existing
//	       | "while" "(" expr ")" "{" stmt* "}"
//	expr ::= comp
//	comp ::= add (("=="|"!="|"<"|"<="|">"|">=") add)?
//
// Comparisons are NON-CHAINING: `a < b < c` is rejected by
// the single-relation grammar (the parser produces left-assoc
// arith inside each side but treats the relation as a flat
// binary). Comparison results are i32 0 / 1 — same shape as
// interp v4 (no boolean type at the toy-AST level).
//
// While semantics: re-evaluate the cond at the top of every
// iteration, exit when zero or when a return fires inside the
// body. State threads through every iteration the same way it
// does between top-level statements: `state = eval_stmt(s,
// state)` per statement, propagating the done flag.
//
// Tests:
//  1. Bare while with single body stmt — counter 1..N.
//  2. While + var + assign — sum 1..10 = 55.
//  3. Nested while — multiplication table sum.
//  4. Early return inside loop body — short-circuits.
//  5. False initial cond — body never runs.
//  6. Comparisons in isolation (return a < b).
//  7. Factorial via iterative loop — closes the
//     recursion-free Turing-completeness gap.
//
// Comparison opcodes (lexed as two-char punct, parser folds
// into one op token):
//
//	== → 1001, != → 1002, < → 1003, <= → 1004, > → 1005, >= → 1006
//
// (Outside the 0..127 ASCII range so they don't collide with
// existing single-char arith ops.)
func TestArm64StmtInterpWhile(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num    { value: i32 }
struct Var    { name: string }
struct BinOp  { op: i32, left: Expr, right: Expr }
type Expr = Num | Var | BinOp;

struct VarDecl  { name: string, value: Expr }
struct Assign   { name: string, value: Expr }
struct Return   { value: Expr }
struct WhileSt  { cond: Expr, body: Stmt[] }
type Stmt = VarDecl | Assign | Return | WhileSt;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else if (b == 61 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1001 });   // ==
            i = i + 2;
        } else if (b == 33 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1002 });   // !=
            i = i + 2;
        } else if (b == 60 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1004 });   // <=
            i = i + 2;
        } else if (b == 62 && i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { ch: 1006 });   // >=
            i = i + 2;
        } else if (b == 60) {
            toks = toks.append(TokPunct { ch: 1003 });   // <
            i = i + 1;
        } else if (b == 62) {
            toks = toks.append(TokPunct { ch: 1005 });   // >
            i = i + 1;
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function is_comp_op(op: i32): boolean {
    return op == 1001 || op == 1002 || op == 1003 ||
           op == 1004 || op == 1005 || op == 1006;
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        cur.set(pos + 1);
        return Var { name: tok_ident_name(toks[pos]) };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_expr(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

// Single comparison layer atop arith. NON-chaining: each comp
// arm takes one arith on each side.
function parse_expr(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_arith(toks, cur);
    var pos: i32 = cur.get();
    if (tok_kind(toks[pos]) != 2) { return lhs; }
    var op: i32 = tok_punct_ch(toks[pos]);
    if (!is_comp_op(op)) { return lhs; }
    cur.set(pos + 1);
    var rhs: Expr = parse_arith(toks, cur);
    return BinOp { op: op, left: lhs, right: rhs };
}

function expect_kw(toks: Token[], pos: i32, kw: string): boolean {
    return tok_kind(toks[pos]) == 1 && tok_ident_name(toks[pos]) == kw;
}

function parse_stmt(toks: Token[], cur: Cell[i32]): Stmt {
    var name: string = "";
    var value: Expr = Num { value: 0 };
    if (expect_kw(toks, cur.get(), "var")) {
        cur.set(cur.get() + 1);
        name = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip '='
        value = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip ';'
        return VarDecl { name: name, value: value };
    }
    if (expect_kw(toks, cur.get(), "return")) {
        cur.set(cur.get() + 1);
        value = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip ';'
        return Return { value: value };
    }
    if (expect_kw(toks, cur.get(), "while")) {
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip '('
        var cond: Expr = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip ')'
        cur.set(cur.get() + 1);   // skip '{'
        var body: Stmt[] = [];
        while (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 125) {
            body = body.append(parse_stmt(toks, cur));
        }
        cur.set(cur.get() + 1);   // skip '}'
        return WhileSt { cond: cond, body: body };
    }
    // Assignment: ident '=' expr ';'
    name = tok_ident_name(toks[cur.get()]);
    cur.set(cur.get() + 1);
    cur.set(cur.get() + 1);   // skip '='
    value = parse_expr(toks, cur);
    cur.set(cur.get() + 1);   // skip ';'
    return Assign { name: name, value: value };
}

function parse_program(src: string): Stmt[] {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var stmts: Stmt[] = [];
    while (tok_kind(toks[cur.get()]) != 3) {
        stmts = stmts.append(parse_stmt(toks, cur));
    }
    return stmts;
}

function bool_to_i32(b: boolean): i32 { if (b) { return 1; } return 0; }

function eval_expr(e: Expr, names: string[], values: i32[]): i32 {
    match (e) {
        Num(n) => { return n.value; },
        Var(v) => {
            var i: i32 = names.len() - 1;
            while (i >= 0) {
                if (names[i] == v.name) { return values[i]; }
                i = i - 1;
            }
            return 0;
        },
        BinOp(b) => {
            var l: i32 = eval_expr(b.left, names, values);
            var r: i32 = eval_expr(b.right, names, values);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            if (b.op == 47) { return l / r; }
            if (b.op == 1001) { return bool_to_i32(l == r); }
            if (b.op == 1002) { return bool_to_i32(l != r); }
            if (b.op == 1003) { return bool_to_i32(l < r); }
            if (b.op == 1004) { return bool_to_i32(l <= r); }
            if (b.op == 1005) { return bool_to_i32(l > r); }
            return bool_to_i32(l >= r);
        },
    }
}

function env_assign(names: string[], values: i32[], name: string, v: i32): i32[] {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) {
            var out: i32[] = [];
            var j: i32 = 0;
            while (j < values.len()) {
                if (j == i) { out = out.append(v); }
                else { out = out.append(values[j]); }
                j = j + 1;
            }
            return out;
        }
        i = i - 1;
    }
    return values;
}

struct StepState {
    names: string[],
    values: i32[],
    done: boolean,
    result: i32,
}

function eval_stmt(s: Stmt, state: StepState): StepState {
    var v: i32 = 0;
    match (s) {
        VarDecl(vd) => {
            v = eval_expr(vd.value, state.names, state.values);
            return StepState {
                names: state.names.append(vd.name),
                values: state.values.append(v),
                done: state.done,
                result: state.result,
            };
        },
        Assign(a) => {
            v = eval_expr(a.value, state.names, state.values);
            return StepState {
                names: state.names,
                values: env_assign(state.names, state.values, a.name, v),
                done: state.done,
                result: state.result,
            };
        },
        Return(r) => {
            v = eval_expr(r.value, state.names, state.values);
            return StepState {
                names: state.names,
                values: state.values,
                done: true,
                result: v,
            };
        },
        WhileSt(w) => {
            // Re-evaluate cond every iteration. Body executes
            // until cond is zero or a Return fires deep inside.
            // State threads through iterations same as top-level
            // (state = eval_stmt(s, state) per stmt).
            while (!state.done && eval_expr(w.cond, state.names, state.values) != 0) {
                var i: i32 = 0;
                while (i < w.body.len() && !state.done) {
                    state = eval_stmt(w.body[i], state);
                    i = i + 1;
                }
            }
            return state;
        },
    }
}

function run(src: string): i32 {
    var stmts: Stmt[] = parse_program(src);
    var state: StepState = StepState {
        names: [],
        values: [],
        done: false,
        result: 0,
    };
    var i: i32 = 0;
    while (i < stmts.len() && !state.done) {
        state = eval_stmt(stmts[i], state);
        i = i + 1;
    }
    return state.result;
}

function main(): i32 {
    // Comparison in isolation: return 3 < 5 → 1.
    if (run("return 3 < 5;") != 1) { return 1; }
    if (run("return 5 < 3;") != 0) { return 2; }
    if (run("return 5 == 5;") != 1) { return 3; }
    if (run("return 5 != 5;") != 0) { return 4; }
    if (run("return 5 <= 5;") != 1) { return 5; }
    if (run("return 5 >= 5;") != 1) { return 6; }

    // Sum 1..10 via a while loop. Tests state threading
    // through iterations + reassignment.
    if (run("var s = 0; var i = 1; while (i <= 10) { s = s + i; i = i + 1; } return s;") != 55) { return 7; }

    // Factorial 5 via a loop — recursion-free Turing-complete
    // sanity. n! where n=5 → 120.
    if (run("var n = 5; var f = 1; while (n > 0) { f = f * n; n = n - 1; } return f;") != 120) { return 8; }

    // False initial cond — body never runs, vars from outside
    // are unchanged.
    if (run("var x = 10; while (x < 0) { x = 999; } return x;") != 10) { return 9; }

    // Early return inside a loop body. Confirms the done flag
    // short-circuits the inner stmt walk + the outer while.
    // The trailing return 99 is dead code — never fires
    // because the while's first iteration returns 0.
    if (run("var i = 0; while (i < 100) { return i; i = i + 1; } return 99;") != 0) { return 10; }

    // Nested while — accumulate i*j for i in 1..3, j in 1..3.
    // Expected: 1 + 2 + 3 + 2 + 4 + 6 + 3 + 6 + 9 = 36.
    if (run("var s = 0; var i = 1; while (i <= 3) { var j = 1; while (j <= 3) { s = s + i * j; j = j + 1; } i = i + 1; } return s;") != 36) { return 11; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stmt-interp v2 while)", code)
	}
}

// Stmt-interp-in-lang: statement-level constructs — `var`
// declarations, assignment, and `return`. A FUNDAMENTALLY new
// shape vs the expression-only spikes. v1..v7 all dealt in
// single expressions; this spike parses a SEQUENCE of
// statements that share a mutable env and produces the return
// value of whichever `return` fires first.
//
// Grammar:
//
//	program ::= stmt* ;
//	stmt    ::= "var" name "=" expr ";"     // declaration
//	          | "return" expr ";"            // exit
//	          | name "=" expr ";"            // reassignment
//	expr    ::= existing arithmetic grammar
//
// The exit-on-return semantics matches every real procedural
// language. eval_block walks the statement list, looks for a
// return, and short-circuits on first hit. Statements before
// the return mutate the env (declarations append, assignments
// rebind the most recent same-named binding); statements
// after a return are unreachable but the test doesn't bother
// detecting that.
//
// Env shape: parallel string[] / i32[] arrays, mutable through
// the in-place assign helper. Lang arrays are functional at
// the language level, so "mutation" means replacing the env
// arrays with fresh copies that differ in one slot — same
// pattern the env_lookup chain has used since interp v5.
//
// Properties tested:
//  1. Bare return — `return 5;` → 5.
//  2. Var + return — `var x = 5; return x;` → 5.
//  3. Reassignment — `var x = 1; x = x + 1; return x;` → 2.
//     Confirms the assign path rebinds the existing slot
//     rather than shadowing.
//  4. Multi-var arithmetic — `var x = 3; var y = 4; return
//     x * 10 + y;` → 34.
//  5. Early return — `var x = 1; return x; var y = 99; return
//     y;` → 1. The second return is dead code; the first
//     fires and the eval stops.
//  6. Forward dependencies — `var x = 10; var y = x + 5;
//     return y;` → 15. Later declarations see earlier ones.
func TestArm64StmtInterpInLang(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num   { value: i32 }
struct Var   { name: string }
struct BinOp { op: i32, left: Expr, right: Expr }
type Expr = Num | Var | BinOp;

struct VarDecl  { name: string, value: Expr }
struct Assign   { name: string, value: Expr }
struct Return   { value: Expr }
type Stmt = VarDecl | Assign | Return;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        cur.set(pos + 1);
        return Var { name: tok_ident_name(toks[pos]) };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_arith(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function expect_kw(toks: Token[], pos: i32, kw: string): boolean {
    return tok_kind(toks[pos]) == 1 && tok_ident_name(toks[pos]) == kw;
}

function parse_stmt(toks: Token[], cur: Cell[i32]): Stmt {
    // Hoist name + value to function scope. The wasm backend
    // names locals by lang identifier; three sibling-scope
    // var-name / var-value declarations would collide. Same
    // workaround the prelude uses.
    var name: string = "";
    var value: Expr = Num { value: 0 };
    if (expect_kw(toks, cur.get(), "var")) {
        cur.set(cur.get() + 1);
        name = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip '='
        value = parse_arith(toks, cur);
        cur.set(cur.get() + 1);   // skip ';'
        return VarDecl { name: name, value: value };
    }
    if (expect_kw(toks, cur.get(), "return")) {
        cur.set(cur.get() + 1);
        value = parse_arith(toks, cur);
        cur.set(cur.get() + 1);   // skip ';'
        return Return { value: value };
    }
    // Assignment: ident '=' expr ';'
    name = tok_ident_name(toks[cur.get()]);
    cur.set(cur.get() + 1);
    cur.set(cur.get() + 1);   // skip '='
    value = parse_arith(toks, cur);
    cur.set(cur.get() + 1);   // skip ';'
    return Assign { name: name, value: value };
}

function parse_program(src: string): Stmt[] {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var stmts: Stmt[] = [];
    while (tok_kind(toks[cur.get()]) != 3) {
        stmts = stmts.append(parse_stmt(toks, cur));
    }
    return stmts;
}

function eval_expr(e: Expr, names: string[], values: i32[]): i32 {
    match (e) {
        Num(n) => { return n.value; },
        Var(v) => {
            var i: i32 = names.len() - 1;
            while (i >= 0) {
                if (names[i] == v.name) { return values[i]; }
                i = i - 1;
            }
            return 0;
        },
        BinOp(b) => {
            var l: i32 = eval_expr(b.left, names, values);
            var r: i32 = eval_expr(b.right, names, values);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            return l / r;
        },
    }
}

// Replace the value of the most recent binding of name with v.
// Returns the new values array. If name isn't bound, leaves
// values unchanged — a real type checker would catch this as
// "unknown identifier", but the spike intentionally stays loose.
function env_assign(names: string[], values: i32[], name: string, v: i32): i32[] {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) {
            // Build a fresh array that differs in slot i.
            var out: i32[] = [];
            var j: i32 = 0;
            while (j < values.len()) {
                if (j == i) { out = out.append(v); }
                else { out = out.append(values[j]); }
                j = j + 1;
            }
            return out;
        }
        i = i - 1;
    }
    return values;
}

// State threaded through the eval loop: env + done flag + result.
// Lang has no tuples-in-args, so the eval function returns a
// fresh struct each step and the caller deconstructs.
struct StepState {
    names: string[],
    values: i32[],
    done: boolean,
    result: i32,
}

function eval_stmt(s: Stmt, state: StepState): StepState {
    // Single hoisted v across the three arms — sibling-scope
    // dups would collide on the wasm emitter.
    var v: i32 = 0;
    match (s) {
        VarDecl(vd) => {
            v = eval_expr(vd.value, state.names, state.values);
            return StepState {
                names: state.names.append(vd.name),
                values: state.values.append(v),
                done: state.done,
                result: state.result,
            };
        },
        Assign(a) => {
            v = eval_expr(a.value, state.names, state.values);
            return StepState {
                names: state.names,
                values: env_assign(state.names, state.values, a.name, v),
                done: state.done,
                result: state.result,
            };
        },
        Return(r) => {
            v = eval_expr(r.value, state.names, state.values);
            return StepState {
                names: state.names,
                values: state.values,
                done: true,
                result: v,
            };
        },
    }
}

function run(src: string): i32 {
    var stmts: Stmt[] = parse_program(src);
    var state: StepState = StepState {
        names: [],
        values: [],
        done: false,
        result: 0,
    };
    var i: i32 = 0;
    while (i < stmts.len() && !state.done) {
        state = eval_stmt(stmts[i], state);
        i = i + 1;
    }
    return state.result;
}

function main(): i32 {
    // Bare return.
    if (run("return 5;") != 5) { return 1; }

    // var + return.
    if (run("var x = 5; return x;") != 5) { return 2; }

    // Reassignment — confirms env_assign rebinds rather than
    // shadows. After x = x + 1, looking up x must return 2.
    if (run("var x = 1; x = x + 1; return x;") != 2) { return 3; }

    // Multi-var arithmetic.
    if (run("var x = 3; var y = 4; return x * 10 + y;") != 34) { return 4; }

    // Early return — the second return is dead code, the
    // first one fires.
    if (run("var x = 1; return x; var y = 99; return y;") != 1) { return 5; }

    // Forward dependency — later var sees earlier one.
    if (run("var x = 10; var y = x + 5; return y;") != 15) { return 6; }

    // Cascading reassignments.
    if (run("var x = 1; x = x * 2; x = x * 2; x = x * 2; return x;") != 8) { return 7; }

    // Two vars, swap via temporary.
    if (run("var a = 10; var b = 20; var t = a; a = b; b = t; return a - b;") != 10) { return 8; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stmt-interp-in-lang)", code)
	}
}

// Stack-VM-in-lang: AST → flat bytecode → stack-machine
// execution. A new SHAPE in the self-host spike series — fold,
// reduce, and print all walk an AST and produce a transformed
// AST or string. compile() walks the AST and produces a FLAT
// LIST OF OPS that a stack VM executes. This is the lowering
// pattern every real compiler uses (lang's IR does the same:
// AST → linear Op[] sequence consumed by codegen).
//
// Bytecode:
//
//	PushConst v   — push integer constant onto the stack
//	Load name     — push the value of named variable
//	Bin op        — pop two, apply op (+, -, *, /), push result
//
// Compile is a post-order walk: emit child ops then the parent
// op. The resulting linear sequence runs left-to-right on the
// VM, building up intermediate values on the stack. Same shape
// as wasm's stack machine, RPN calculators, and lang's own IR
// dispatch loop in the codegen backends.
//
// Properties tested:
//  1. Round-trip semantic equivalence — execute(compile(e), env)
//     == eval(e, env) for the same env, on every shape exercised
//     by the earlier spikes (literals, vars, all four binary
//     operators, nesting, parens).
//  2. Bytecode shape — `1 + 2 * 3` compiles to exactly:
//     [PushConst 1, PushConst 2, PushConst 3, Bin *, Bin +].
//     Five ops, post-order, in that order. The shape proves the
//     compile walk is bottom-up + left-to-right.
//  3. Stack discipline — after a successful execute(), the stack
//     has exactly one element (the result). Tested by always
//     reading the top and asserting on it.
//  4. fold + compile integration — folding the AST FIRST makes
//     the bytecode shorter. `compile(fold("1 + 2 * 3"))` is
//     one op (PushConst 7) instead of five.
func TestArm64StackVMInLang(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num   { value: i32 }
struct Var   { name: string }
struct BinOp { op: i32, left: Expr, right: Expr }
type Expr = Num | Var | BinOp;

struct PushConst { value: i32 }
struct Load      { name: string }
struct Bin       { op: i32 }
type Op = PushConst | Load | Bin;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        cur.set(pos + 1);
        return Var { name: tok_ident_name(toks[pos]) };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_arith(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function parse_src(src: string): Expr {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    return parse_arith(toks, cur);
}

function is_num(e: Expr): boolean {
    match (e) { Num(_) => { return true; }, _ => { return false; } }
}
function num_value(e: Expr): i32 {
    match (e) { Num(n) => { return n.value; }, _ => { return 0; } }
}

function fold(e: Expr): Expr {
    match (e) {
        Num(_) => { return e; },
        Var(_) => { return e; },
        BinOp(b) => {
            var l: Expr = fold(b.left);
            var r: Expr = fold(b.right);
            if (is_num(l) && is_num(r)) {
                var lv: i32 = num_value(l);
                var rv: i32 = num_value(r);
                if (b.op == 43) { return Num { value: lv + rv }; }
                if (b.op == 45) { return Num { value: lv - rv }; }
                if (b.op == 42) { return Num { value: lv * rv }; }
                if (b.op == 47) { return Num { value: lv / rv }; }
            }
            return BinOp { op: b.op, left: l, right: r };
        },
    }
}

// Post-order walk: left subtree → right subtree → parent op.
// Each call appends to and returns the in-flight Op list, so
// the assembled bytecode reads left-to-right same as the
// source expression's evaluation order.
function compile(e: Expr, ops: Op[]): Op[] {
    match (e) {
        Num(n) => {
            return ops.append(PushConst { value: n.value });
        },
        Var(v) => {
            return ops.append(Load { name: v.name });
        },
        BinOp(b) => {
            var ops1: Op[] = compile(b.left, ops);
            var ops2: Op[] = compile(b.right, ops1);
            return ops2.append(Bin { op: b.op });
        },
    }
}

function compile_top(e: Expr): Op[] {
    var ops: Op[] = [];
    return compile(e, ops);
}

function env_lookup(names: string[], values: i32[], name: string): i32 {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) { return values[i]; }
        i = i - 1;
    }
    return 0;
}

// Execute the bytecode against a value stack. Pop two for Bin,
// push the result. After a well-formed program runs, the stack
// has exactly one element — the result.
function execute(ops: Op[], names: string[], values: i32[]): i32 {
    var stack: i32[] = [];
    var i: i32 = 0;
    while (i < ops.len()) {
        match (ops[i]) {
            PushConst(p) => { stack = stack.append(p.value); },
            Load(l) => { stack = stack.append(env_lookup(names, values, l.name)); },
            Bin(b) => {
                var r: i32 = stack[stack.len() - 1];
                var l: i32 = stack[stack.len() - 2];
                var out: i32 = 0;
                if (b.op == 43) { out = l + r; }
                else if (b.op == 45) { out = l - r; }
                else if (b.op == 42) { out = l * r; }
                else { out = l / r; }
                // Pop two and push one — net stack delta -1.
                // Lang arrays are functional, so building a
                // fresh stack of length len-1 by slicing is
                // O(len) but the test loops stay small.
                var ns: i32[] = [];
                var j: i32 = 0;
                while (j < stack.len() - 2) {
                    ns = ns.append(stack[j]);
                    j = j + 1;
                }
                stack = ns.append(out);
            },
        }
        i = i + 1;
    }
    return stack[0];
}

// Direct interpreter — the reference oracle for round-trip
// equivalence. compile + execute must agree with eval on
// every input.
function eval(e: Expr, names: string[], values: i32[]): i32 {
    match (e) {
        Num(n) => { return n.value; },
        Var(v) => { return env_lookup(names, values, v.name); },
        BinOp(b) => {
            var l: i32 = eval(b.left, names, values);
            var r: i32 = eval(b.right, names, values);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            return l / r;
        },
    }
}

function roundtrip(src: string, names: string[], values: i32[]): boolean {
    var e: Expr = parse_src(src);
    return execute(compile_top(e), names, values) == eval(e, names, values);
}

function op_kind(o: Op): i32 {
    match (o) {
        PushConst(_) => { return 0; },
        Load(_)      => { return 1; },
        Bin(_)       => { return 2; },
    }
}

function op_pushconst_value(o: Op): i32 {
    match (o) { PushConst(p) => { return p.value; }, _ => { return -1; } }
}
function op_bin_op(o: Op): i32 {
    match (o) { Bin(b) => { return b.op; }, _ => { return -1; } }
}

function main(): i32 {
    var names: string[] = ["x", "y"];
    var values: i32[] = [10, 20];

    // Bare literal — one PushConst, executes to its value.
    var c1: Op[] = compile_top(parse_src("42"));
    if (c1.len() != 1) { return 1; }
    if (execute(c1, names, values) != 42) { return 2; }

    // Var lookup — one Load op.
    var c2: Op[] = compile_top(parse_src("x"));
    if (c2.len() != 1) { return 3; }
    if (op_kind(c2[0]) != 1) { return 4; }
    if (execute(c2, names, values) != 10) { return 5; }

    // Simple BinOp — three ops: PushConst, PushConst, Bin.
    var c3: Op[] = compile_top(parse_src("1 + 2"));
    if (c3.len() != 3) { return 6; }
    if (op_kind(c3[0]) != 0 || op_pushconst_value(c3[0]) != 1) { return 7; }
    if (op_kind(c3[1]) != 0 || op_pushconst_value(c3[1]) != 2) { return 8; }
    if (op_kind(c3[2]) != 2 || op_bin_op(c3[2]) != 43) { return 9; }
    if (execute(c3, names, values) != 3) { return 10; }

    // Nested — 1 + 2 * 3 compiles to:
    //   PushConst 1
    //   PushConst 2
    //   PushConst 3
    //   Bin *           ← pops 2,3 pushes 6
    //   Bin +           ← pops 1,6 pushes 7
    var c4: Op[] = compile_top(parse_src("1 + 2 * 3"));
    if (c4.len() != 5) { return 11; }
    if (op_kind(c4[3]) != 2 || op_bin_op(c4[3]) != 42) { return 12; }   // *
    if (op_kind(c4[4]) != 2 || op_bin_op(c4[4]) != 43) { return 13; }   // +
    if (execute(c4, names, values) != 7) { return 14; }

    // Round-trip equivalence — every shape from the earlier
    // spikes must agree with the direct interpreter.
    if (!roundtrip("1 + 2 * 3", names, values)) { return 15; }
    if (!roundtrip("(1 + 2) * 3", names, values)) { return 16; }
    if (!roundtrip("x + y", names, values)) { return 17; }
    if (!roundtrip("x * y - 5", names, values)) { return 18; }
    if (!roundtrip("100 / 4 / 5", names, values)) { return 19; }
    if (!roundtrip("(x + 1) * (y - 2)", names, values)) { return 20; }

    // fold + compile integration — folding first produces
    // shorter bytecode. fold("1 + 2 * 3") → Num(7), which
    // compiles to a SINGLE PushConst op.
    var c5: Op[] = compile_top(fold(parse_src("1 + 2 * 3")));
    if (c5.len() != 1) { return 21; }
    if (op_pushconst_value(c5[0]) != 7) { return 22; }

    // Partial fold + compile — "x + 2 * 3" folds to "x + 6",
    // which compiles to three ops (Load + PushConst + Bin)
    // instead of the five the unfolded form would emit.
    var c6: Op[] = compile_top(fold(parse_src("x + 2 * 3")));
    if (c6.len() != 3) { return 23; }
    if (execute(c6, names, values) != 16) { return 24; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stack-vm-in-lang)", code)
	}
}

// Strength-reduce-in-lang: the algebraic-identity complement
// to constfold (#420). Constfold collapses BinOps with two
// literal operands; strength reduction simplifies BinOps where
// ONE operand is a known constant that triggers an identity:
//
//	x + 0   →   x      x - 0   →   x
//	0 + x   →   x      0 * x   →   0
//	x * 1   →   x      x * 0   →   0
//	1 * x   →   x      x / 1   →   x
//
// The real `internal/ir/strength.go` runs these on IR (after
// type-aware short-circuit handling); this spike runs them on
// the toy Expr AST. Same shape, simpler representation.
//
// Properties tested:
//  1. Identity rules fire — `x + 0` collapses to `x`, the
//     enclosing BinOp disappears. count_binop drops by one.
//  2. Absorbing rule for `* 0` — `x * 0` collapses to `0`.
//     Note `x / 0` does NOT collapse — that's a runtime trap,
//     not an algebraic identity.
//  3. Combined with constfold — running fold + reduce in
//     sequence on `(2 + 3) + (x * 1)` collapses to `5 + x`
//     via two passes.
//  4. Idempotence — reduce(reduce(e)) ≡ reduce(e).
//  5. Semantic preservation — eval(reduce(e)) == eval(e) for
//     any e and any env that doesn't divide-by-zero.
func TestArm64StrengthReduceInLang(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num   { value: i32 }
struct Var   { name: string }
struct BinOp { op: i32, left: Expr, right: Expr }
type Expr = Num | Var | BinOp;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        cur.set(pos + 1);
        return Var { name: tok_ident_name(toks[pos]) };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_arith(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function parse_src(src: string): Expr {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    return parse_arith(toks, cur);
}

function is_num_with(e: Expr, want: i32): boolean {
    match (e) { Num(n) => { return n.value == want; }, _ => { return false; } }
}
function is_num(e: Expr): boolean {
    match (e) { Num(_) => { return true; }, _ => { return false; } }
}
function num_value(e: Expr): i32 {
    match (e) { Num(n) => { return n.value; }, _ => { return 0; } }
}

// Constfold from #420, included so the integration test can
// chain fold + reduce without depending on cross-spike helpers.
function fold(e: Expr): Expr {
    match (e) {
        Num(_) => { return e; },
        Var(_) => { return e; },
        BinOp(b) => {
            var l: Expr = fold(b.left);
            var r: Expr = fold(b.right);
            if (is_num(l) && is_num(r)) {
                var lv: i32 = num_value(l);
                var rv: i32 = num_value(r);
                if (b.op == 43) { return Num { value: lv + rv }; }
                if (b.op == 45) { return Num { value: lv - rv }; }
                if (b.op == 42) { return Num { value: lv * rv }; }
                if (b.op == 47) { return Num { value: lv / rv }; }
            }
            return BinOp { op: b.op, left: l, right: r };
        },
    }
}

// reduce applies algebraic identities. Walks bottom-up like
// fold so an inner reduction can expose a parent-level one.
function reduce(e: Expr): Expr {
    match (e) {
        Num(_) => { return e; },
        Var(_) => { return e; },
        BinOp(b) => {
            var l: Expr = reduce(b.left);
            var r: Expr = reduce(b.right);
            if (b.op == 43) {
                if (is_num_with(l, 0)) { return r; }
                if (is_num_with(r, 0)) { return l; }
            }
            if (b.op == 45) {
                if (is_num_with(r, 0)) { return l; }
            }
            if (b.op == 42) {
                if (is_num_with(l, 0) || is_num_with(r, 0)) { return Num { value: 0 }; }
                if (is_num_with(l, 1)) { return r; }
                if (is_num_with(r, 1)) { return l; }
            }
            if (b.op == 47) {
                if (is_num_with(r, 1)) { return l; }
                // x / 0 stays as-is — runtime trap, not a fold.
            }
            return BinOp { op: b.op, left: l, right: r };
        },
    }
}

function eval(e: Expr, names: string[], values: i32[]): i32 {
    match (e) {
        Num(n) => { return n.value; },
        Var(v) => {
            var i: i32 = names.len() - 1;
            while (i >= 0) {
                if (names[i] == v.name) { return values[i]; }
                i = i - 1;
            }
            return 0;
        },
        BinOp(b) => {
            var l: i32 = eval(b.left, names, values);
            var r: i32 = eval(b.right, names, values);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            return l / r;
        },
    }
}

function count_binop(e: Expr): i32 {
    match (e) {
        Num(_) => { return 0; },
        Var(_) => { return 0; },
        BinOp(b) => { return 1 + count_binop(b.left) + count_binop(b.right); },
    }
}

function ast_eq(a: Expr, b: Expr): boolean {
    match (a) {
        Num(an) => {
            match (b) {
                Num(bn) => { return an.value == bn.value; },
                _ => { return false; },
            }
        },
        Var(av) => {
            match (b) {
                Var(bv) => { return av.name == bv.name; },
                _ => { return false; },
            }
        },
        BinOp(ab) => {
            match (b) {
                BinOp(bb) => {
                    if (ab.op != bb.op) { return false; }
                    if (!ast_eq(ab.left, bb.left)) { return false; }
                    return ast_eq(ab.right, bb.right);
                },
                _ => { return false; },
            }
        },
    }
}

function main(): i32 {
    var names: string[] = ["x", "y"];
    var values: i32[] = [10, 20];

    // x + 0 → x. The BinOp disappears.
    var r1: Expr = reduce(parse_src("x + 0"));
    if (count_binop(r1) != 0) { return 1; }
    if (eval(r1, names, values) != 10) { return 2; }

    // 0 + x → x (commutative case).
    var r2: Expr = reduce(parse_src("0 + x"));
    if (count_binop(r2) != 0) { return 3; }
    if (eval(r2, names, values) != 10) { return 4; }

    // x * 1 → x.
    var r3: Expr = reduce(parse_src("x * 1"));
    if (count_binop(r3) != 0) { return 5; }
    if (eval(r3, names, values) != 10) { return 6; }

    // 1 * x → x.
    var r4: Expr = reduce(parse_src("1 * x"));
    if (count_binop(r4) != 0) { return 7; }
    if (eval(r4, names, values) != 10) { return 8; }

    // x * 0 → 0 (absorbing).
    var r5: Expr = reduce(parse_src("x * 0"));
    if (count_binop(r5) != 0) { return 9; }
    if (eval(r5, names, values) != 0) { return 10; }

    // x - 0 → x.
    var r6: Expr = reduce(parse_src("x - 0"));
    if (count_binop(r6) != 0) { return 11; }
    if (eval(r6, names, values) != 10) { return 12; }

    // x / 1 → x.
    var r7: Expr = reduce(parse_src("x / 1"));
    if (count_binop(r7) != 0) { return 13; }
    if (eval(r7, names, values) != 10) { return 14; }

    // Nested — (x + 0) * (y + 0) → x * y. The reduce walks
    // children first, then the parent BinOp sees two Var
    // operands and stays.
    var r8: Expr = reduce(parse_src("(x + 0) * (y + 0)"));
    if (count_binop(r8) != 1) { return 15; }
    if (eval(r8, names, values) != 200) { return 16; }

    // 0 - x does NOT collapse — subtraction isn't commutative,
    // so the left-zero rule doesn't apply. Stays as BinOp(-, 0, x).
    var r9: Expr = reduce(parse_src("0 - x"));
    if (count_binop(r9) != 1) { return 17; }
    if (eval(r9, names, values) != -10) { return 18; }

    // x / 0 stays as BinOp — runtime trap, not a fold.
    var r10: Expr = reduce(parse_src("x / 0"));
    if (count_binop(r10) != 1) { return 19; }

    // Combined pipeline — (2 + 3) + (x * 1):
    //   fold: (5) + (x * 1)
    //   reduce: 5 + x
    var combined: Expr = reduce(fold(parse_src("(2 + 3) + (x * 1)")));
    if (count_binop(combined) != 1) { return 20; }
    if (eval(combined, names, values) != 15) { return 21; }

    // Idempotence — running reduce twice gives the same result.
    var once: Expr = reduce(parse_src("x + 0 + y * 1"));
    var twice: Expr = reduce(once);
    if (!ast_eq(once, twice)) { return 22; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (strength-reduce-in-lang)", code)
	}
}

// Printer-in-lang: the natural complement to parse-in-lang.
// Closes the parse/print round-trip — `parse(print(e))` reproduces
// the original AST shape. Together with the constfold pass
// (#420) this rounds out lex / parse / fold / print / eval, the
// full pipeline shape of a self-hosted compiler.
//
// `print_expr(e: Expr): string` walks the AST and produces a
// canonical textual rendering. BinOp is always parenthesised,
// which means the output isn't minimal but is always
// unambiguous — `1 + 2 * 3` round-trips through "(1 + (2 * 3))"
// rather than "1 + 2 * 3", but parse+ast_eq still hold because
// the inner shape is preserved. Real lang's printer uses
// precedence-aware parens (drops redundant ones at top of
// binary chains); the always-parens shape lets the spike stay
// focused on the tree walk rather than precedence tables.
//
// Round-trip property: `parse(print(e))` must structurally
// equal `e`. Tested across the same expression shapes as the
// constfold spike — atoms, simple BinOps, nested, vars mixed
// in. Confirms the printer respects associativity (the parens
// disambiguate; otherwise `1 - 2 - 3` and `1 - (2 - 3)` parse
// to different ASTs).
//
// Bonus integration: fold + print proves the combined pipeline.
// `print(fold(parse("1 + 2 * 3")))` should be "7" (the folded
// Num spits out its value directly, no parens).
func TestArm64PrinterInLang(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num   { value: i32 }
struct Var   { name: string }
struct BinOp { op: i32, left: Expr, right: Expr }
type Expr = Num | Var | BinOp;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        cur.set(pos + 1);
        return Var { name: tok_ident_name(toks[pos]) };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_arith(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function parse_src(src: string): Expr {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    return parse_arith(toks, cur);
}

function op_str(op: i32): string {
    if (op == 43) { return "+"; }
    if (op == 45) { return "-"; }
    if (op == 42) { return "*"; }
    if (op == 47) { return "/"; }
    return "?";
}

// Render an Expr to text. Atoms (Num, Var) come out unwrapped;
// BinOp is always parenthesised so the output is unambiguous
// without consulting a precedence table. Always-parens means
// the printed form isn't minimal but always round-trips.
function print_expr(e: Expr): string {
    match (e) {
        Num(n) => { return n.value.to_string(); },
        Var(v) => { return v.name; },
        BinOp(b) => {
            return "(" + print_expr(b.left) + " " + op_str(b.op) + " " + print_expr(b.right) + ")";
        },
    }
}

function ast_eq(a: Expr, b: Expr): boolean {
    match (a) {
        Num(an) => {
            match (b) {
                Num(bn) => { return an.value == bn.value; },
                _ => { return false; },
            }
        },
        Var(av) => {
            match (b) {
                Var(bv) => { return av.name == bv.name; },
                _ => { return false; },
            }
        },
        BinOp(ab) => {
            match (b) {
                BinOp(bb) => {
                    if (ab.op != bb.op) { return false; }
                    if (!ast_eq(ab.left, bb.left)) { return false; }
                    return ast_eq(ab.right, bb.right);
                },
                _ => { return false; },
            }
        },
    }
}

function is_num(e: Expr): boolean {
    match (e) { Num(_) => { return true; }, _ => { return false; } }
}
function num_value(e: Expr): i32 {
    match (e) { Num(n) => { return n.value; }, _ => { return 0; } }
}

function fold(e: Expr): Expr {
    match (e) {
        Num(_) => { return e; },
        Var(_) => { return e; },
        BinOp(b) => {
            var l: Expr = fold(b.left);
            var r: Expr = fold(b.right);
            if (is_num(l) && is_num(r)) {
                var lv: i32 = num_value(l);
                var rv: i32 = num_value(r);
                if (b.op == 43) { return Num { value: lv + rv }; }
                if (b.op == 45) { return Num { value: lv - rv }; }
                if (b.op == 42) { return Num { value: lv * rv }; }
                if (b.op == 47) { return Num { value: lv / rv }; }
            }
            return BinOp { op: b.op, left: l, right: r };
        },
    }
}

function roundtrip_ok(src: string): boolean {
    var e1: Expr = parse_src(src);
    var s: string = print_expr(e1);
    var e2: Expr = parse_src(s);
    return ast_eq(e1, e2);
}

function main(): i32 {
    // Atoms.
    if (print_expr(Num { value: 42 }) != "42") { return 1; }
    if (print_expr(Var { name: "x" }) != "x") { return 2; }

    // Simple BinOp.
    var e1: Expr = parse_src("1 + 2");
    if (print_expr(e1) != "(1 + 2)") { return 3; }

    // Nested BinOp.
    var e2: Expr = parse_src("1 + 2 * 3");
    if (print_expr(e2) != "(1 + (2 * 3))") { return 4; }

    // Vars mixed with literals.
    var e3: Expr = parse_src("x + 1");
    if (print_expr(e3) != "(x + 1)") { return 5; }

    // Round-trip property — print(parse(s)) re-parses to the
    // same AST. The TEXTUAL form differs (always-parens
    // canonical), but the AST shape is preserved.
    if (!roundtrip_ok("1 + 2")) { return 6; }
    if (!roundtrip_ok("1 + 2 * 3")) { return 7; }
    if (!roundtrip_ok("(1 + 2) * 3")) { return 8; }
    if (!roundtrip_ok("x + y * z")) { return 9; }
    if (!roundtrip_ok("10 - 3 - 2")) { return 10; }
    if (!roundtrip_ok("a * b + c * d - e")) { return 11; }

    // Associativity preservation. The parser is left-associative;
    // 10 - 3 - 2 means (10 - 3) - 2 = 5. The printer's parens
    // make this explicit: "((10 - 3) - 2)". Without parens around
    // the left subtree the re-parse would group right instead.
    var assoc: Expr = parse_src("10 - 3 - 2");
    if (print_expr(assoc) != "((10 - 3) - 2)") { return 12; }

    // Fold + print integration — full-collapse case prints as a
    // bare number, no parens, since BinOps were all folded out.
    var folded: Expr = fold(parse_src("1 + 2 * 3"));
    if (print_expr(folded) != "7") { return 13; }

    // Partial-fold case keeps the Var-bearing outer BinOp.
    var partial: Expr = fold(parse_src("x + 2 * 3"));
    if (print_expr(partial) != "(x + 6)") { return 14; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (printer-in-lang)", code)
	}
}

// Constfold-in-lang: a real compiler pass, written in lang.
// Closes a meaningful self-host milestone — lex/parse/eval has
// been the pattern through v1..v7, but a tree-rewriting
// transformation over the AST is the shape of every real
// compiler optimization (constfold, DCE, inlining, copy-prop,
// strength reduction). This spike proves lang can host that
// shape too.
//
// The pass: `fold(e: Expr): Expr`. Walks the expression tree
// bottom-up, replacing any BinOp whose operands are both Num
// with a single Num holding the folded value. Anything with a
// Var or partial-fold remainder passes through unchanged at
// that node (but its subtrees are still folded). Matches the
// shape of internal/constfold in the real compiler.
//
// Test strategy:
//  1. Parse "1 + 2 * 3" → BinOp(+, Num(1), BinOp(*, Num(2),
//     Num(3))). Fold → Num(7). Confirms fully-constant
//     expressions collapse to a single literal.
//  2. Parse "x + 2 * 3" → fold partials: the 2 * 3 inside
//     a BinOp collapses; the outer BinOp(+, Var(x), Num(6))
//     stays since one operand is a Var.
//  3. Parse "(1 + 2) + (3 + 4)" → Num(10). Confirms recursive
//     folding through nested BinOps.
//  4. Idempotence: fold(fold(e)) == fold(e). Real compilers
//     run optimization passes in a loop; idempotence is the
//     stable-fixpoint property they all need.
//  5. Semantic preservation: eval(fold(e)) == eval(e). The
//     pass must not change the program's meaning.
//
// Counting helpers (`count_num`, `count_binop`) drive the
// structural assertions — after folding the constant cases,
// the node count is exactly 1; after the partial case it's
// 1 BinOp + 1 Var + 1 Num.
func TestArm64ConstfoldInLang(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num   { value: i32 }
struct Var   { name: string }
struct BinOp { op: i32, left: Expr, right: Expr }
type Expr = Num | Var | BinOp;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        cur.set(pos + 1);
        return Var { name: tok_ident_name(toks[pos]) };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_arith(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function parse_src(src: string): Expr {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    return parse_arith(toks, cur);
}

// Extract a Num's value, or -1 if e isn't a Num. The pass uses
// the sentinel approach rather than an Option[i32] return so the
// match arms stay tight; -1 would be a legitimate folded value
// in some cases, but the only caller (fold's BinOp arm) guards
// the check with is_num() first.
function is_num(e: Expr): boolean {
    match (e) { Num(_) => { return true; }, _ => { return false; } }
}
function num_value(e: Expr): i32 {
    match (e) { Num(n) => { return n.value; }, _ => { return 0; } }
}

function fold(e: Expr): Expr {
    match (e) {
        Num(_) => { return e; },
        Var(_) => { return e; },
        BinOp(b) => {
            var l: Expr = fold(b.left);
            var r: Expr = fold(b.right);
            if (is_num(l) && is_num(r)) {
                var lv: i32 = num_value(l);
                var rv: i32 = num_value(r);
                if (b.op == 43) { return Num { value: lv + rv }; }
                if (b.op == 45) { return Num { value: lv - rv }; }
                if (b.op == 42) { return Num { value: lv * rv }; }
                if (b.op == 47) { return Num { value: lv / rv }; }
            }
            return BinOp { op: b.op, left: l, right: r };
        },
    }
}

function eval(e: Expr, names: string[], values: i32[]): i32 {
    match (e) {
        Num(n) => { return n.value; },
        Var(v) => {
            var i: i32 = names.len() - 1;
            while (i >= 0) {
                if (names[i] == v.name) { return values[i]; }
                i = i - 1;
            }
            return 0;
        },
        BinOp(b) => {
            var l: i32 = eval(b.left, names, values);
            var r: i32 = eval(b.right, names, values);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            return l / r;
        },
    }
}

function count_num(e: Expr): i32 {
    match (e) {
        Num(_) => { return 1; },
        Var(_) => { return 0; },
        BinOp(b) => { return count_num(b.left) + count_num(b.right); },
    }
}
function count_var(e: Expr): i32 {
    match (e) {
        Num(_) => { return 0; },
        Var(_) => { return 1; },
        BinOp(b) => { return count_var(b.left) + count_var(b.right); },
    }
}
function count_binop(e: Expr): i32 {
    match (e) {
        Num(_) => { return 0; },
        Var(_) => { return 0; },
        BinOp(b) => { return 1 + count_binop(b.left) + count_binop(b.right); },
    }
}

// Compare two ASTs for structural equality. Used to test
// idempotence: fold(fold(e)) ≡ fold(e). The op field is i32
// so equality is byte-exact; same for Num.value. Var compares
// by name. BinOp recurses into children.
function ast_eq(a: Expr, b: Expr): boolean {
    match (a) {
        Num(an) => {
            match (b) {
                Num(bn) => { return an.value == bn.value; },
                _ => { return false; },
            }
        },
        Var(av) => {
            match (b) {
                Var(bv) => { return av.name == bv.name; },
                _ => { return false; },
            }
        },
        BinOp(ab) => {
            match (b) {
                BinOp(bb) => {
                    if (ab.op != bb.op) { return false; }
                    if (!ast_eq(ab.left, bb.left)) { return false; }
                    return ast_eq(ab.right, bb.right);
                },
                _ => { return false; },
            }
        },
    }
}

function main(): i32 {
    var names: string[] = ["x"];
    var values: i32[] = [10];

    // Fully constant — 1 + 2 * 3 folds to Num(7).
    var e1: Expr = parse_src("1 + 2 * 3");
    var f1: Expr = fold(e1);
    if (count_num(f1) != 1) { return 1; }
    if (count_binop(f1) != 0) { return 2; }
    if (eval(f1, names, values) != 7) { return 3; }
    // Semantic preservation.
    if (eval(e1, names, values) != eval(f1, names, values)) { return 4; }

    // Partial fold — x + 2 * 3 folds 2*3 = 6, leaves x + 6.
    var e2: Expr = parse_src("x + 2 * 3");
    var f2: Expr = fold(e2);
    if (count_num(f2) != 1) { return 5; }
    if (count_var(f2) != 1) { return 6; }
    if (count_binop(f2) != 1) { return 7; }
    if (eval(f2, names, values) != 16) { return 8; }
    if (eval(e2, names, values) != eval(f2, names, values)) { return 9; }

    // Recursive fold through nested constant subtrees.
    // (1 + 2) + (3 + 4) → Num(10). Confirms the post-order walk
    // catches subtrees before the parent gets a chance to fold.
    var e3: Expr = parse_src("(1 + 2) + (3 + 4)");
    var f3: Expr = fold(e3);
    if (count_num(f3) != 1) { return 10; }
    if (count_binop(f3) != 0) { return 11; }
    if (eval(f3, names, values) != 10) { return 12; }

    // Idempotence — running fold on already-folded output is
    // a no-op. Real optimization loops run passes to fixpoint;
    // a non-idempotent constfold blows out the loop.
    var f3_again: Expr = fold(f3);
    if (!ast_eq(f3, f3_again)) { return 13; }
    var f2_again: Expr = fold(f2);
    if (!ast_eq(f2, f2_again)) { return 14; }

    // Division case — 12 / 4 = 3.
    var e4: Expr = parse_src("12 / 4");
    var f4: Expr = fold(e4);
    if (count_num(f4) != 1) { return 15; }
    if (eval(f4, names, values) != 3) { return 16; }

    // Subtraction — 10 - 3 - 2 folds left-to-right via the
    // left-associative parser: (10 - 3) - 2 = 5.
    var e5: Expr = parse_src("10 - 3 - 2");
    var f5: Expr = fold(e5);
    if (count_num(f5) != 1) { return 17; }
    if (eval(f5, names, values) != 5) { return 18; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (constfold-in-lang)", code)
	}
}

// Interp-in-lang v7: multi-arg functions. v6 (#418) shipped
// single-arg functions, deferring multi-arg as "trivially
// additive but doubles the line count without proving
// anything new about the calling convention". This PR makes
// good on that — actually adding multi-arg is mechanical, but
// it lets the toy interpreter host pairwise / triadic helpers
// (`min(a, b)`, `cond(c, t, f)`, `ackermann(m, n)`) without
// the unary workaround of synthetic tuple-wrapping.
//
// Grammar changes:
//
//	fn_def ::= "fn" name "(" param ("," param)* ")" "=" expr ";"
//	call   ::= name "(" expr ("," expr)* ")"
//
// Storage: each function gets a `params: string[]` list (was a
// single `string`) and each Call gets an `args: Expr[]` list
// (was a single `Expr`). The FnDef struct collects each
// function's name + params + body, and the Program holds a
// `fns: FnDef[]` + the main expression.
//
// Eval-side: evaluate each arg LEFT-TO-RIGHT (matches the rest
// of lang), zip with the function's param names into a fresh
// env, recurse with the new env. Zero-arg functions (no params
// between the parens) round-trip cleanly — the empty arg
// array zips with the empty param array and the body sees a
// pristine env.
//
// Recursion still falls out from the closed-over fn table.
// Mutual recursion works for the same reason.
func TestArm64InterpV7MultiArg(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num   { value: i32 }
struct Var   { name: string }
struct BinOp { op: i32, left: Expr, right: Expr }
struct If    { cond: Expr, thn: Expr, els: Expr }
struct Call  { name: string, args: Expr[] }
type Expr = Num | Var | BinOp | If | Call;

struct FnDef { name: string, params: string[], body: Expr }

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function expect_kw(toks: Token[], pos: i32, kw: string): boolean {
    return tok_kind(toks[pos]) == 1 && tok_ident_name(toks[pos]) == kw;
}

function env_lookup(names: string[], values: i32[], name: string): i32 {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) { return values[i]; }
        i = i - 1;
    }
    return 0;
}

function find_fn(fns: FnDef[], name: string): i32 {
    var i: i32 = 0;
    while (i < fns.len()) {
        if (fns[i].name == name) { return i; }
        i = i + 1;
    }
    return -1;
}

function eval(e: Expr, names: string[], values: i32[], fns: FnDef[]): i32 {
    match (e) {
        Num(n) => { return n.value; },
        Var(v) => { return env_lookup(names, values, v.name); },
        BinOp(b) => {
            var l: i32 = eval(b.left, names, values, fns);
            var r: i32 = eval(b.right, names, values, fns);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            return l / r;
        },
        If(ie) => {
            var c: i32 = eval(ie.cond, names, values, fns);
            if (c != 0) { return eval(ie.thn, names, values, fns); }
            return eval(ie.els, names, values, fns);
        },
        Call(ce) => {
            var idx: i32 = find_fn(fns, ce.name);
            var fresh_n: string[] = [];
            var fresh_v: i32[] = [];
            var i: i32 = 0;
            while (i < ce.args.len()) {
                fresh_n = fresh_n.append(fns[idx].params[i]);
                fresh_v = fresh_v.append(eval(ce.args[i], names, values, fns));
                i = i + 1;
            }
            return eval(fns[idx].body, fresh_n, fresh_v, fns);
        },
    }
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_expr(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    if (expect_kw(toks, pos, "if")) {
        cur.set(pos + 1);
        var c: Expr = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip "then"
        var thn: Expr = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip "else"
        var els: Expr = parse_expr(toks, cur);
        return If { cond: c, thn: thn, els: els };
    }
    return parse_arith(toks, cur);
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        var name: string = tok_ident_name(toks[pos]);
        cur.set(pos + 1);
        if (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 40) {
            cur.set(cur.get() + 1);   // skip '('
            var args: Expr[] = [];
            // Empty arg list — immediate ')'.
            if (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 41) {
                cur.set(cur.get() + 1);
                return Call { name: name, args: args };
            }
            args = args.append(parse_expr(toks, cur));
            while (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 44) {
                cur.set(cur.get() + 1);   // skip ','
                args = args.append(parse_expr(toks, cur));
            }
            cur.set(cur.get() + 1);   // skip ')'
            return Call { name: name, args: args };
        }
        return Var { name: name };
    }
    cur.set(pos + 1);   // skip '('
    var inner: Expr = parse_expr(toks, cur);
    cur.set(cur.get() + 1);   // skip ')'
    return inner;
}

struct Program { fns: FnDef[], main_expr: Expr }

function parse_program(src: string): Program {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var fns: FnDef[] = [];
    while (expect_kw(toks, cur.get(), "fn")) {
        cur.set(cur.get() + 1);
        var name: string = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip '('
        var params: string[] = [];
        if (tok_kind(toks[cur.get()]) != 2 || tok_punct_ch(toks[cur.get()]) != 41) {
            params = params.append(tok_ident_name(toks[cur.get()]));
            cur.set(cur.get() + 1);
            while (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 44) {
                cur.set(cur.get() + 1);   // skip ','
                params = params.append(tok_ident_name(toks[cur.get()]));
                cur.set(cur.get() + 1);
            }
        }
        cur.set(cur.get() + 1);   // skip ')'
        cur.set(cur.get() + 1);   // skip '='
        var body: Expr = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip ';'
        fns = fns.append(FnDef { name: name, params: params, body: body });
    }
    var main_expr: Expr = parse_expr(toks, cur);
    return Program { fns: fns, main_expr: main_expr };
}

function interp(src: string): i32 {
    var p: Program = parse_program(src);
    var empty_n: string[] = [];
    var empty_v: i32[] = [];
    return eval(p.main_expr, empty_n, empty_v, p.fns);
}

function main(): i32 {
    // Two-arg function — add(3, 4) = 7.
    if (interp("fn add(a, b) = a + b; add(3, 4)") != 7) { return 1; }

    // Three-arg function — cond(c, t, f) selects via if.
    if (interp("fn cond(c, t, f) = if c then t else f; cond(1, 100, 200)") != 100) { return 2; }
    if (interp("fn cond(c, t, f) = if c then t else f; cond(0, 100, 200)") != 200) { return 3; }

    // Recursive two-arg — gcd(a, b) via Euclidean algorithm.
    // gcd(48, 18) = 6. Uses subtraction-only since % isn't lexed.
    if (interp("fn gcd(a, b) = if b then gcd(b, a - a / b * b) else a; gcd(48, 18)") != 6) { return 4; }

    // Nested call — add(mul(2, 3), mul(4, 5)) = 6 + 20 = 26.
    if (interp("fn mul(a, b) = a * b; fn add(a, b) = a + b; add(mul(2, 3), mul(4, 5))") != 26) { return 5; }

    // Zero-arg function — answer() = 42.
    if (interp("fn answer() = 42; answer()") != 42) { return 6; }

    // Mutual recursion across two two-arg functions.
    // ackermann's smaller sibling: ack2(m, n) using m=0/1 special cases.
    // ack(0, n) = n + 1
    // ack(1, n) = n + 2
    // ack(2, n) = 2n + 3
    // Test: ack(2, 3) = 9.
    if (interp("fn ack(m, n) = if m then if m - 1 then 2 * n + 3 else n + 2 else n + 1; ack(2, 3)") != 9) { return 7; }

    // Argument evaluation order — left-to-right. The toy
    // interp is pure (no side effects), but the order matters
    // if a recursive call's intermediate result depends on
    // another call's value. Test the layered composition:
    // sum3(1, 2, 3) = 6.
    if (interp("fn sum3(a, b, c) = a + b + c; sum3(1, 2, 3)") != 6) { return 8; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (interp v7 multi-arg)", code)
	}
}

// Interp-in-lang v6: function declarations + calls. The toy
// interpreter from v5 gains top-level `fn` definitions and a
// `Call` expression node, closing the gap from "lambda-ish let
// blocks" to "real recursive functions". With this the in-lang
// interpreter can host factorial, fibonacci, and arbitrary
// mutual recursion — the same Turing-complete shape every
// pedagogical interpreter ends on.
//
// Grammar additions (single-arg for brevity):
//
//	program ::= fn_def* expr
//	fn_def  ::= "fn" name "(" param ")" "=" expr ";"
//	factor  ::= ... | name "(" expr ")"
//
// Functions live in three parallel arrays — `fn_names`,
// `fn_params`, `fn_bodies` — passed through every eval call.
// Recursion works because the function table is built before
// eval starts; a Call looks up by name, evaluates the arg,
// pushes (param, arg-value) onto the env, and recurses into
// the body with a FRESH env (functions don't see the caller's
// locals — lexical scope).
//
// Single-arg keeps the type story simple: i32 in, i32 out.
// Multi-arg would need parallel param/arg arrays at the Call
// site; trivially additive but doubles the test's line count
// without proving anything new about the calling convention.
//
// Calls disambiguate at parse time: `ident "("` lookahead
// becomes Call, bare `ident` stays Var. Matches the real
// parser's primary-postfix split.
//
// `fn_bodies: Expr[]` works because Expr is a union (sugar
// over enum) — arrays of enum cells are heap-allocated value
// containers with the standard array runtime, no special
// support needed.
func TestArm64InterpV6Functions(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num   { value: i32 }
struct Var   { name: string }
struct BinOp { op: i32, left: Expr, right: Expr }
struct If    { cond: Expr, thn: Expr, els: Expr }
struct Call  { name: string, arg: Expr }
type Expr = Num | Var | BinOp | If | Call;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            var start: i32 = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function expect_kw(toks: Token[], pos: i32, kw: string): boolean {
    return tok_kind(toks[pos]) == 1 && tok_ident_name(toks[pos]) == kw;
}

function env_lookup(names: string[], values: i32[], name: string): i32 {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) { return values[i]; }
        i = i - 1;
    }
    return 0;
}

function fn_index(fn_names: string[], name: string): i32 {
    var i: i32 = 0;
    while (i < fn_names.len()) {
        if (fn_names[i] == name) { return i; }
        i = i + 1;
    }
    return -1;
}

function eval(e: Expr, names: string[], values: i32[], fn_names: string[], fn_params: string[], fn_bodies: Expr[]): i32 {
    match (e) {
        Num(n) => { return n.value; },
        Var(v) => { return env_lookup(names, values, v.name); },
        BinOp(b) => {
            var l: i32 = eval(b.left, names, values, fn_names, fn_params, fn_bodies);
            var r: i32 = eval(b.right, names, values, fn_names, fn_params, fn_bodies);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            return l / r;
        },
        If(ie) => {
            var c: i32 = eval(ie.cond, names, values, fn_names, fn_params, fn_bodies);
            if (c != 0) { return eval(ie.thn, names, values, fn_names, fn_params, fn_bodies); }
            return eval(ie.els, names, values, fn_names, fn_params, fn_bodies);
        },
        Call(ce) => {
            var av: i32 = eval(ce.arg, names, values, fn_names, fn_params, fn_bodies);
            var idx: i32 = fn_index(fn_names, ce.name);
            // Lexical scope: function bodies start with a fresh
            // env containing only the param binding, not the
            // caller's locals.
            var fresh_n: string[] = [fn_params[idx]];
            var fresh_v: i32[] = [av];
            return eval(fn_bodies[idx], fresh_n, fresh_v, fn_names, fn_params, fn_bodies);
        },
    }
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_expr(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    if (expect_kw(toks, pos, "if")) {
        cur.set(pos + 1);
        var c: Expr = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip "then"
        var thn: Expr = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip "else"
        var els: Expr = parse_expr(toks, cur);
        return If { cond: c, thn: thn, els: els };
    }
    return parse_arith(toks, cur);
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        var name: string = tok_ident_name(toks[pos]);
        cur.set(pos + 1);
        // Call if next is '(' — single-token lookahead.
        if (tok_kind(toks[cur.get()]) == 2 && tok_punct_ch(toks[cur.get()]) == 40) {
            cur.set(cur.get() + 1);
            var arg: Expr = parse_expr(toks, cur);
            cur.set(cur.get() + 1);   // skip ')'
            return Call { name: name, arg: arg };
        }
        return Var { name: name };
    }
    cur.set(pos + 1);   // skip '('
    var inner: Expr = parse_expr(toks, cur);
    cur.set(cur.get() + 1);   // skip ')'
    return inner;
}

struct Program {
    fn_names: string[],
    fn_params: string[],
    fn_bodies: Expr[],
    main_expr: Expr,
}

function parse_program(src: string): Program {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var fn_names: string[] = [];
    var fn_params: string[] = [];
    var fn_bodies: Expr[] = [];
    while (expect_kw(toks, cur.get(), "fn")) {
        cur.set(cur.get() + 1);
        var name: string = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip '('
        var param: string = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);   // skip ')'
        cur.set(cur.get() + 1);   // skip '='
        var body: Expr = parse_expr(toks, cur);
        cur.set(cur.get() + 1);   // skip ';'
        fn_names = fn_names.append(name);
        fn_params = fn_params.append(param);
        fn_bodies = fn_bodies.append(body);
    }
    var main_expr: Expr = parse_expr(toks, cur);
    return Program {
        fn_names: fn_names,
        fn_params: fn_params,
        fn_bodies: fn_bodies,
        main_expr: main_expr,
    };
}

function interp(src: string): i32 {
    var p: Program = parse_program(src);
    var empty_n: string[] = [];
    var empty_v: i32[] = [];
    return eval(p.main_expr, empty_n, empty_v, p.fn_names, p.fn_params, p.fn_bodies);
}

function main(): i32 {
    // Bare expression — no functions, same as v5 territory.
    if (interp("1 + 2 * 3") != 7) { return 1; }

    // Single function — square 5 = 25.
    if (interp("fn square(x) = x * x; square(5)") != 25) { return 2; }

    // Function composition — double(square(3)) = 18.
    if (interp("fn double(x) = x * 2; fn square(x) = x * x; double(square(3))") != 18) { return 3; }

    // Recursion — factorial(6) = 720.
    if (interp("fn fact(n) = if n then n * fact(n - 1) else 1; fact(6)") != 720) { return 4; }

    // Conditional dispatch — abs(-7) via subtraction-based sign trick.
    if (interp("fn dbl(x) = x + x; if 1 then dbl(21) else 0") != 42) { return 5; }

    // Nested call — fact(fact(3)) = fact(6) = 720.
    if (interp("fn fact(n) = if n then n * fact(n - 1) else 1; fact(fact(3))") != 720) { return 6; }

    // Lexical scope check — the function body can't see the
    // caller's bindings. Without lexical scope, the inner Var(x)
    // would resolve to 99 from main's env (if main introduced
    // one) rather than the param. Test: define a function that
    // returns its param, call it with an arg distinct from any
    // outer name. Param shadowing is the point.
    if (interp("fn id(x) = x; id(7)") != 7) { return 7; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (interp v6 functions)", code)
	}
}

// Parser-in-lang v5: `let x = e in body` bindings + `if c
// then a else b` expressions, layered on top of the arith /
// relation grammar. The Expr union grows to five variants:
//
//	type Expr = Num | Var | BinOp | Let | If;
//
// Identifier tokenisation joins the lexer — `let`, `in`, `if`,
// `then`, `else` are recognised as keywords (still TokIdent at
// the lex layer; the parser dispatches on the spelling).
//
// Environment is a pair of parallel string[] / i32[] arrays
// the eval threads through every recursive call. Let-binding
// uses functional `.push` (PR #412 added Array.push to the
// interp) so sibling scopes don't see each other's bindings.
// Lookup walks the array tail-first to give inner shadows
// precedence over outer ones.
//
// This is the closest the toy interp gets to a "real" mini-
// language — Num, Var, BinOp, Let, If is the same five-node
// shape every CS-textbook lambda calculus interpreter starts
// with. Function declarations + calls are the natural next
// step but need named-callable storage, which is a bigger
// jump.
func TestArm64InterpV5LetIf(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokIdent | TokPunct | TokEof;

struct Num   { value: i32 }
struct Var   { name: string }
struct BinOp { op: i32, left: Expr, right: Expr }
struct Let   { name: string, value: Expr, body: Expr }
struct If    { cond: Expr, thn: Expr, els: Expr }
type Expr = Num | Var | BinOp | Let | If;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha()) {
            var start: i32 = i;
            while (i < n && (src[i] as i32).is_ascii_alnum()) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokIdent(_) => { return 1; },
        TokPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}
function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_ident_name(t: Token): string {
    match (t) { TokIdent(x) => { return x.name; }, _ => { return ""; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}

function env_lookup(names: string[], values: i32[], name: string): i32 {
    var i: i32 = names.len() - 1;
    while (i >= 0) {
        if (names[i] == name) { return values[i]; }
        i = i - 1;
    }
    return 0;
}

function eval(e: Expr, names: string[], values: i32[]): i32 {
    match (e) {
        Num(n) => { return n.value; },
        Var(v) => { return env_lookup(names, values, v.name); },
        BinOp(b) => {
            var l: i32 = eval(b.left, names, values);
            var r: i32 = eval(b.right, names, values);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            return l / r;
        },
        Let(le) => {
            var v: i32 = eval(le.value, names, values);
            var n2: string[] = names.append(le.name);
            var v2: i32[] = values.append(v);
            return eval(le.body, n2, v2);
        },
        If(ie) => {
            var c: i32 = eval(ie.cond, names, values);
            if (c != 0) { return eval(ie.thn, names, values); }
            return eval(ie.els, names, values);
        },
    }
}

function expect_kw(toks: Token[], pos: i32, kw: string): boolean {
    return tok_kind(toks[pos]) == 1 && tok_ident_name(toks[pos]) == kw;
}

function parse_expr(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    if (expect_kw(toks, pos, "if")) {
        cur.set(pos + 1);
        var c: Expr = parse_expr(toks, cur);
        cur.set(cur.get() + 1);
        var t: Expr = parse_expr(toks, cur);
        cur.set(cur.get() + 1);
        var el: Expr = parse_expr(toks, cur);
        return If { cond: c, thn: t, els: el };
    }
    if (expect_kw(toks, pos, "let")) {
        cur.set(pos + 1);
        var name: string = tok_ident_name(toks[cur.get()]);
        cur.set(cur.get() + 1);
        cur.set(cur.get() + 1);
        var val: Expr = parse_expr(toks, cur);
        cur.set(cur.get() + 1);
        var body: Expr = parse_expr(toks, cur);
        return Let { name: name, value: val, body: body };
    }
    return parse_arith(toks, cur);
}

function parse_arith(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 2) { return lhs; }
        var op: i32 = tok_punct_ch(toks[pos]);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        cur.set(pos + 1);
        return Num { value: tok_int_value(toks[pos]) };
    }
    if (k == 1) {
        cur.set(pos + 1);
        return Var { name: tok_ident_name(toks[pos]) };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_expr(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function interp(src: string): i32 {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var ast: Expr = parse_expr(toks, cur);
    var empty_n: string[] = [];
    var empty_v: i32[] = [];
    return eval(ast, empty_n, empty_v);
}

function main(): i32 {
    if (interp("let x = 5 in x + 1") != 6) { return 1; }
    if (interp("let x = 5 in let y = 10 in x + y") != 15) { return 2; }
    if (interp("if 1 then 100 else 200") != 100) { return 3; }
    if (interp("if 0 then 100 else 200") != 200) { return 4; }
    if (interp("let x = 7 in if x then x * 2 else 0") != 14) { return 5; }
    if (interp("let x = 1 in let x = 99 in x") != 99) { return 6; }
    if (interp("(1 + 2) * 3") != 9) { return 7; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (interp v5 let+if)", code)
	}
}

// Parser-in-lang v4: comparison operators (`==`, `!=`, `<`,
// `<=`, `>`, `>=`) layered on top of the arithmetic
// interpreter. Adds a new `parse_relation` precedence level
// above `parse_expr`; comparisons don't chain (`a < b < c` is
// rejected by the single-relation grammar). Result of a
// comparison is i32 1 / 0 — no boolean type at the AST level.
//
// BinOp now stores `op: string` instead of `op: i32`. A
// string holds both single-char and multi-char operators
// without a second tag field. The eval body dispatches on
// the string directly.
//
// `rhs` / `op_s` hoisted to function scope in parse_relation
// so the wasm emitter (which names locals by lang identifier)
// doesn't see a duplicate across the two sibling `if`
// branches — same workaround the prelude's `__map_hash`
// already uses.
func TestArm64InterpV4Comparisons(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokPunct { ch: i32 }
struct TokDPunct { text: string }
struct TokEof   { _pad: i32 }
type Token = TokInt | TokPunct | TokDPunct | TokEof;

struct Num   { value: i32 }
struct BinOp { op: string, left: Expr, right: Expr }
type Expr = Num | BinOp;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b == 61 || b == 33 || b == 60 || b == 62) &&
                   i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokDPunct { text: src[i : i + 2] + "" });
            i = i + 2;
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function tok_kind(t: Token): i32 {
    match (t) {
        TokInt(_)   => { return 0; },
        TokPunct(_) => { return 1; },
        TokDPunct(_) => { return 2; },
        TokEof(_)   => { return 3; },
    }
}

function tok_int_value(t: Token): i32 {
    match (t) { TokInt(x) => { return x.value; }, _ => { return 0; } }
}
function tok_punct_ch(t: Token): i32 {
    match (t) { TokPunct(p) => { return p.ch; }, _ => { return 0; } }
}
function tok_dpunct_text(t: Token): string {
    match (t) { TokDPunct(p) => { return p.text; }, _ => { return ""; } }
}

function eval(e: Expr): i32 {
    match (e) {
        Num(n) => { return n.value; },
        BinOp(b) => {
            var l: i32 = eval(b.left);
            var r: i32 = eval(b.right);
            if (b.op == "+") { return l + r; }
            if (b.op == "-") { return l - r; }
            if (b.op == "*") { return l * r; }
            if (b.op == "/") { return l / r; }
            if (b.op == "==") { if (l == r) { return 1; } return 0; }
            if (b.op == "!=") { if (l != r) { return 1; } return 0; }
            if (b.op == "<")  { if (l <  r) { return 1; } return 0; }
            if (b.op == "<=") { if (l <= r) { return 1; } return 0; }
            if (b.op == ">")  { if (l >  r) { return 1; } return 0; }
            if (b.op == ">=") { if (l >= r) { return 1; } return 0; }
            return 0;
        },
    }
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    if (k == 0) {
        var v: i32 = tok_int_value(toks[pos]);
        cur.set(pos + 1);
        return Num { value: v };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_relation(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 1) { return lhs; }
        var ch: i32 = tok_punct_ch(toks[pos]);
        if (ch != 42 && ch != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        var op_s: string = "*";
        if (ch == 47) { op_s = "/"; }
        lhs = BinOp { op: op_s, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_expr(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (tok_kind(toks[pos]) != 1) { return lhs; }
        var ch: i32 = tok_punct_ch(toks[pos]);
        if (ch != 43 && ch != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        var op_s: string = "+";
        if (ch == 45) { op_s = "-"; }
        lhs = BinOp { op: op_s, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_relation(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_expr(toks, cur);
    var pos: i32 = cur.get();
    var k: i32 = tok_kind(toks[pos]);
    var rhs: Expr = Num { value: 0 };
    var op_s: string = "";
    if (k == 1) {
        var ch: i32 = tok_punct_ch(toks[pos]);
        if (ch == 60 || ch == 62) {
            cur.set(pos + 1);
            rhs = parse_expr(toks, cur);
            op_s = "<";
            if (ch == 62) { op_s = ">"; }
            return BinOp { op: op_s, left: lhs, right: rhs };
        }
        return lhs;
    }
    if (k == 2) {
        op_s = tok_dpunct_text(toks[pos]);
        cur.set(pos + 1);
        rhs = parse_expr(toks, cur);
        return BinOp { op: op_s, left: lhs, right: rhs };
    }
    return lhs;
}

function interp(src: string): i32 {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var ast: Expr = parse_relation(toks, cur);
    return eval(ast);
}

function main(): i32 {
    if (interp("1 + 2 * 3") != 7) { return 1; }
    if (interp("3 < 5") != 1) { return 2; }
    if (interp("5 < 3") != 0) { return 3; }
    if (interp("2 + 3 == 5") != 1) { return 4; }
    if (interp("2 + 3 != 5") != 0) { return 5; }
    if (interp("10 >= 10") != 1) { return 6; }
    if (interp("10 <= 9") != 0) { return 7; }
    if (interp("100 - 50 > 40") != 1) { return 8; }
    if (interp("2 * 3 == 6") != 1) { return 9; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (interp v4 comparisons)", code)
	}
}

// Parser-in-lang v2: full lex → parse → eval pipeline. Wires
// the lexer-in-lang spike (PRs #394-#395, #399-#401) to the
// recursive-descent parser (PR #402) so a source string flows
// through tokenisation, AST construction, and recursive
// evaluation — all in lang.
//
//	interp("1 + 2 * 3") → 7
//	interp("(1 + 2) * 3") → 9
//
// This is the smallest end-to-end "compiler-shaped" pipeline
// the lang has ever run on itself. Closes the lex+parse half
// of the self-host validation arc; the next stages (checker,
// IR, codegen) are bigger but architecturally similar shape.
func TestArm64InterpV2(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }

type Token = TokInt | TokPunct | TokEof;

struct Num   { value: i32 }
struct BinOp { op: i32, left: Expr, right: Expr }

type Expr = Num | BinOp;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function peek_kind(toks: Token[], pos: i32): i32 {
    match (toks[pos]) {
        TokInt(_) => { return 0; },
        TokPunct(_) => { return 1; },
        TokEof(_) => { return 2; },
    }
}

function peek_punct(toks: Token[], pos: i32): i32 {
    match (toks[pos]) {
        TokPunct(p) => { return p.ch; },
        _ => { return 0; },
    }
}

function peek_int(toks: Token[], pos: i32): i32 {
    match (toks[pos]) {
        TokInt(t) => { return t.value; },
        _ => { return 0; },
    }
}

function eval(e: Expr): i32 {
    match (e) {
        Num(n) => { return n.value; },
        BinOp(b) => {
            var l: i32 = eval(b.left);
            var r: i32 = eval(b.right);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            return l / r;
        },
    }
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = peek_kind(toks, pos);
    if (k == 0) {
        var v: i32 = peek_int(toks, pos);
        cur.set(pos + 1);
        return Num { value: v };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_expr(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (peek_kind(toks, pos) != 1) { return lhs; }
        var op: i32 = peek_punct(toks, pos);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_expr(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        if (peek_kind(toks, pos) != 1) { return lhs; }
        var op: i32 = peek_punct(toks, pos);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function interp(src: string): i32 {
    var toks: Token[] = tokenize(src);
    var cur: Cell[i32] = cell_new(0);
    var ast: Expr = parse_expr(toks, cur);
    return eval(ast);
}

function main(): i32 {
    if (interp("1 + 2 * 3") != 7) { return 1; }
    if (interp("(1 + 2) * 3") != 9) { return 2; }
    if (interp("10 - 4 - 2") != 4) { return 3; }
    if (interp("100 / 5 / 4") != 5) { return 4; }
    if (interp("  42  ") != 42) { return 5; }
    if (interp("(((7)))") != 7) { return 6; }
    if (interp("2 + 3 * 4 - 5") != 9) { return 7; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (interp v2)", code)
	}
}

// Parser-in-lang v1: recursive-descent arithmetic-expression
// parser that consumes a hand-built Token[] and produces an
// AST in the union shape the Go compiler's `internal/ast`
// uses for Expr nodes. This is the FIRST validation that
// the union-types machinery (PRs #390 + #392) handles
// recursive struct-of-union payloads — `BinOp { left: Expr,
// right: Expr }` references the union containing it.
//
// Operator precedence handled the textbook way: `expr` →
// `term` → `factor`. `factor` recurses into `expr` for
// parenthesised subexpressions, exercising the full
// recursion through the AST.
//
// Cursor passed via a single-element `i32[]` for in-place
// mutation — lang's value semantics + lack of by-reference
// params mean this is the cheapest "out parameter" shape
// available. Will get prettier when generic Cell[T] /
// reference types land.
//
// Closes a meaningful self-host milestone: the AST visitor
// pattern (the main blocker per docs/ROADMAP-AND-SELF-
// HOSTING.md) works end-to-end in real recursive-descent
// code. Lexer-in-lang (PRs #394-#395, #399-#401) +
// parser-in-lang (this) cover lex → parse, the first two
// stages of the compiler pipeline.
func TestArm64ParserV1(t *testing.T) {
	src := `struct TokInt   { value: i32 }
struct TokPunct { ch: i32 }
struct TokEof   { _pad: i32 }

type Token = TokInt | TokPunct | TokEof;

struct Num   { value: i32 }
struct BinOp { op: i32, left: Expr, right: Expr }

type Expr = Num | BinOp;

function peek(toks: Token[], pos: i32): i32 {
    match (toks[pos]) {
        TokInt(_) => { return 0; },
        TokPunct(_) => { return 1; },
        TokEof(_) => { return 2; },
    }
}

function peek_punct(toks: Token[], pos: i32): i32 {
    match (toks[pos]) {
        TokPunct(p) => { return p.ch; },
        _ => { return 0; },
    }
}

function peek_int(toks: Token[], pos: i32): i32 {
    match (toks[pos]) {
        TokInt(t) => { return t.value; },
        _ => { return 0; },
    }
}

function eval(e: Expr): i32 {
    match (e) {
        Num(n) => { return n.value; },
        BinOp(b) => {
            var l: i32 = eval(b.left);
            var r: i32 = eval(b.right);
            if (b.op == 43) { return l + r; }
            if (b.op == 45) { return l - r; }
            if (b.op == 42) { return l * r; }
            return l / r;
        },
    }
}

function parse_factor(toks: Token[], cur: Cell[i32]): Expr {
    var pos: i32 = cur.get();
    var k: i32 = peek(toks, pos);
    if (k == 0) {
        var v: i32 = peek_int(toks, pos);
        cur.set(pos + 1);
        return Num { value: v };
    }
    cur.set(pos + 1);
    var inner: Expr = parse_expr(toks, cur);
    cur.set(cur.get() + 1);
    return inner;
}

function parse_term(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_factor(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        var k: i32 = peek(toks, pos);
        if (k != 1) { return lhs; }
        var op: i32 = peek_punct(toks, pos);
        if (op != 42 && op != 47) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_factor(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function parse_expr(toks: Token[], cur: Cell[i32]): Expr {
    var lhs: Expr = parse_term(toks, cur);
    while (true) {
        var pos: i32 = cur.get();
        var k: i32 = peek(toks, pos);
        if (k != 1) { return lhs; }
        var op: i32 = peek_punct(toks, pos);
        if (op != 43 && op != 45) { return lhs; }
        cur.set(pos + 1);
        var rhs: Expr = parse_term(toks, cur);
        lhs = BinOp { op: op, left: lhs, right: rhs };
    }
    return lhs;
}

function main(): i32 {
    // 1 + 2 * 3 → 7 (precedence)
    var t1: Token[] = [];
    t1 = t1.append(TokInt { value: 1 });
    t1 = t1.append(TokPunct { ch: 43 });
    t1 = t1.append(TokInt { value: 2 });
    t1 = t1.append(TokPunct { ch: 42 });
    t1 = t1.append(TokInt { value: 3 });
    t1 = t1.append(TokEof { _pad: 0 });
    var c1: Cell[i32] = cell_new(0);
    if (eval(parse_expr(t1, c1)) != 7) { return 1; }

    // (1 + 2) * 3 → 9 (parens override)
    var t2: Token[] = [];
    t2 = t2.append(TokPunct { ch: 40 });
    t2 = t2.append(TokInt { value: 1 });
    t2 = t2.append(TokPunct { ch: 43 });
    t2 = t2.append(TokInt { value: 2 });
    t2 = t2.append(TokPunct { ch: 41 });
    t2 = t2.append(TokPunct { ch: 42 });
    t2 = t2.append(TokInt { value: 3 });
    t2 = t2.append(TokEof { _pad: 0 });
    var c2: Cell[i32] = cell_new(0);
    if (eval(parse_expr(t2, c2)) != 9) { return 2; }

    // 10 - 4 - 2 → 4 (left-associativity)
    var t3: Token[] = [];
    t3 = t3.append(TokInt { value: 10 });
    t3 = t3.append(TokPunct { ch: 45 });
    t3 = t3.append(TokInt { value: 4 });
    t3 = t3.append(TokPunct { ch: 45 });
    t3 = t3.append(TokInt { value: 2 });
    t3 = t3.append(TokEof { _pad: 0 });
    var c3: Cell[i32] = cell_new(0);
    if (eval(parse_expr(t3, c3)) != 4) { return 3; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (parser v1)", code)
	}
}

// Lexer-in-lang v7: float literals + the Number/Float token-kind
// split. Builds on v6 (line comments + full keyword set + string
// escapes). Closes the last numeric-literal gap before f-strings.
//
// The Go lexer recognises a float as integer-digits followed by
// `.<digit>+` — `1.5` is a Float, `1.` is the int `1` then a
// `.` punctuator, `.5` is `.` then `5` (not a float either).
// Exponent form (`1e10`, `1.5e-10`) isn't lexed as a single
// token in lang. v7 mirrors that exactly.
//
// TokFloat carries the raw text spelling (`"1.5"`) rather than
// a parsed f32/f64 — the real lexer does the same and defers
// parse-to-double to the parser. The test asserts on the text,
// which is sufficient to prove the lexer captured the right
// bytes; numeric-equality testing would force the lang program
// to do its own f32 parse, which is a downstream concern.
//
// The dot disambiguation point: `a[0].x` and `1.x` shouldn't
// be lexed as floats. v7's `i + 1 < n && src[i] == '.' && next
// is digit` rule handles both — `0.x` falls through (`x` isn't
// a digit) so the `.` becomes a punctuator and `x` an ident.
func TestArm64LexerV7(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokFloat { text: string }
struct TokIdent { name: string }
struct TokKw    { name: string }
struct TokStr   { value: string }
struct TokPunct { text: string }
struct TokEof   { _pad: i32 }

type Token = TokInt | TokFloat | TokIdent | TokKw | TokStr | TokPunct | TokEof;

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    var numV: i32 = 0;
    var start: i32 = 0;
    var isFloat: boolean = false;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if (b == 47 && i + 1 < n && src[i + 1] == 47) {
            i = i + 2;
            while (i < n && src[i] != 10) { i = i + 1; }
        } else if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            start = i;
            numV = 0;
            isFloat = false;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                numV = numV * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            // Float when next is dot followed by digit. 1. and 1.x
            // leave the dot for the punctuator branch — matches
            // the Go lexer at internal/lexer/lexer.go:298.
            if (i + 1 < n && src[i] == 46 && (src[i + 1] as i32).is_ascii_digit()) {
                isFloat = true;
                i = i + 1;
                while (i < n && (src[i] as i32).is_ascii_digit()) { i = i + 1; }
            }
            if (isFloat) {
                toks = toks.append(TokFloat { text: src[start:i] + "" });
            } else {
                toks = toks.append(TokInt { value: numV });
            }
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            start = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { text: src[i:i + 1] + "" });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function main(): i32 {
    // Bare floats — text spelling round-trips byte-exact.
    var t1: Token[] = tokenize("1.5 2.0 100.001");
    if (t1.len() != 4) { return 100 + t1.len(); }
    match (t1[0]) {
        TokFloat(t) => { if (t.text != "1.5") { return 1; } },
        _ => { return 2; },
    }
    match (t1[1]) {
        TokFloat(t) => { if (t.text != "2.0") { return 3; } },
        _ => { return 4; },
    }
    match (t1[2]) {
        TokFloat(t) => { if (t.text != "100.001") { return 5; } },
        _ => { return 6; },
    }

    // Mixed ints + floats — the int branch leaves untouched
    // tokens before/after a float for the surrounding lexer state.
    var t2: Token[] = tokenize("3 + 1.5 - 2");
    if (t2.len() != 6) { return 200 + t2.len(); }
    match (t2[0]) {
        TokInt(t) => { if (t.value != 3) { return 7; } },
        _ => { return 8; },
    }
    match (t2[2]) {
        TokFloat(t) => { if (t.text != "1.5") { return 9; } },
        _ => { return 10; },
    }
    match (t2[4]) {
        TokInt(t) => { if (t.value != 2) { return 11; } },
        _ => { return 12; },
    }

    // Disambiguation: 1. must be the int 1 + the . punctuator,
    // not a malformed float. Same for 1.x.
    var t3: Token[] = tokenize("1.");
    if (t3.len() != 3) { return 300 + t3.len(); }
    match (t3[0]) {
        TokInt(t) => { if (t.value != 1) { return 13; } },
        _ => { return 14; },
    }
    match (t3[1]) {
        TokPunct(t) => { if (t.text != ".") { return 15; } },
        _ => { return 16; },
    }

    var t4: Token[] = tokenize("1.x");
    if (t4.len() != 4) { return 400 + t4.len(); }
    match (t4[0]) {
        TokInt(t) => { if (t.value != 1) { return 17; } },
        _ => { return 18; },
    }
    match (t4[1]) {
        TokPunct(t) => { if (t.text != ".") { return 19; } },
        _ => { return 20; },
    }
    match (t4[2]) {
        TokIdent(t) => { if (t.name != "x") { return 21; } },
        _ => { return 22; },
    }

    // .5 (no leading int digit) lexes as . + int 5 — the
    // float branch never fires because the leading byte isn't
    // a digit.
    var t5: Token[] = tokenize(".5");
    if (t5.len() != 3) { return 500 + t5.len(); }
    match (t5[0]) {
        TokPunct(t) => { if (t.text != ".") { return 23; } },
        _ => { return 24; },
    }
    match (t5[1]) {
        TokInt(t) => { if (t.value != 5) { return 25; } },
        _ => { return 26; },
    }

    // Method-call style on int: 0.to_string() — the dot
    // disambiguation lets the parser see int + dot + ident
    // rather than a botched float consuming to_string.
    var t6: Token[] = tokenize("0.to_string");
    if (t6.len() != 4) { return 600 + t6.len(); }
    match (t6[0]) {
        TokInt(t) => { if (t.value != 0) { return 27; } },
        _ => { return 28; },
    }
    match (t6[1]) {
        TokPunct(t) => { if (t.text != ".") { return 29; } },
        _ => { return 30; },
    }
    match (t6[2]) {
        TokIdent(t) => { if (t.name != "to_string") { return 31; } },
        _ => { return 32; },
    }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (lexer v7)", code)
	}
}

// Lexer-in-lang v6: line comments, full keyword set, string
// literals with escape sequences. Builds on v5 (hex integers +
// numeric suffixes), v4 (numeric suffixes), v3 (string literals,
// re-introduced here with escape handling), v2 (idents +
// punctuation), v1 (decimal integers).
//
// New surface vs v5:
//
//  1. Line comments — `// ...` until newline (or EOF). Standard
//     C-family comment lexing; the body is dropped, the closing
//     newline is left for the whitespace branch to eat.
//  2. Full keyword set — function / var / let / if / else /
//     while / for / break / continue / return / true / false /
//     match / struct / type + sized numeric type names. Matches
//     the Go lexer's `keywords` map. The classifier is a chain
//     of `==` comparisons; a Map lookup would be cleaner but
//     Map[string,boolean] in script-mode interp is on the
//     "blocked behind virtual heap" list.
//  3. String literals with escapes — `\n` `\t` `\r` `\0` `\"`
//     `\\` are decoded inline. Builds the body via repeated
//     `s = s + ...` concat (one alloc per escape, one per run
//     of plain bytes). Quadratic on long strings, fine for the
//     lexer where token bodies are short.
//  4. Underscore in identifiers — `is_digit`, `_pad`, etc.
//     Real lang allows `[a-zA-Z_][a-zA-Z0-9_]*`. v5 only
//     called `is_alpha()` / `is_alnum()`; v6 ORs in the
//     underscore explicitly so `__alloc_u8` and friends lex
//     as a single ident.
//
// Closes a meaningful chunk of lexer-port parity — the only
// remaining gaps vs `internal/lexer/lexer.go` are float
// literals, f-strings, and the Number/Float kind split. Float
// literals will land in v7.
func TestArm64LexerV6(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokKw    { name: string }
struct TokStr   { value: string }
struct TokPunct { text: string }
struct TokEof   { _pad: i32 }

type Token = TokInt | TokIdent | TokKw | TokStr | TokPunct | TokEof;

function is_keyword(name: string): boolean {
    return name == "function" || name == "var" || name == "let" ||
           name == "if" || name == "else" || name == "while" ||
           name == "for" || name == "break" || name == "continue" ||
           name == "return" || name == "true" || name == "false" ||
           name == "match" || name == "struct" || name == "type" ||
           name == "boolean" || name == "void" || name == "string" ||
           name == "i32" || name == "i64" || name == "u8" ||
           name == "u32" || name == "u64" || name == "f32" ||
           name == "f64" || name == "usize";
}

function escape_byte(b: i32): i32 {
    if (b == 110) { return 10; }   // \n
    if (b == 116) { return 9; }    // \t
    if (b == 114) { return 13; }   // \r
    if (b == 48)  { return 0; }    // \0
    return b;                       // \" \\ pass through
}

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    var numV: i32 = 0;
    var s: string = "";
    var start: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if (b == 47 && i + 1 < n && src[i + 1] == 47) {
            // Line comment — drop bytes through next \n.
            i = i + 2;
            while (i < n && src[i] != 10) { i = i + 1; }
        } else if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if (b == 34) {
            // String literal with escape handling. Build the
            // decoded body via plain string concat.
            i = i + 1;
            s = "";
            while (i < n && src[i] != 34) {
                if (src[i] == 92 && i + 1 < n) {
                    var bs: u8[] = __alloc_u8(1);
                    bs = bs.with(0, escape_byte(src[i + 1] as i32) as u8);
                    s = s + string_from_bytes_unchecked(bs);
                    i = i + 2;
                } else {
                    s = s + src[i:i + 1];
                    i = i + 1;
                }
            }
            if (i < n) { i = i + 1; }   // closing "
            toks = toks.append(TokStr { value: s });
        } else if ((b as i32).is_ascii_digit()) {
            numV = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                numV = numV * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: numV });
        } else if ((b as i32).is_ascii_alpha() || b == 95) {
            start = i;
            while (i < n && ((src[i] as i32).is_ascii_alnum() || src[i] == 95)) {
                i = i + 1;
            }
            var name: str = src[start:i];
            if (is_keyword(name)) {
                toks = toks.append(TokKw { name: name + "" });
            } else {
                toks = toks.append(TokIdent { name: name + "" });
            }
        } else {
            toks = toks.append(TokPunct { text: src[i:i + 1] + "" });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function main(): i32 {
    // Comments, keywords, idents, ints, punct in one program.
    var src1: string = "function f() {\n    var x = 42; // local\n    return x;\n}";
    var toks: Token[] = tokenize(src1);
    if (toks.len() != 15) { return 100 + toks.len(); }
    match (toks[0]) {
        TokKw(t) => { if (t.name != "function") { return 1; } },
        _ => { return 2; },
    }
    match (toks[1]) {
        TokIdent(t) => { if (t.name != "f") { return 3; } },
        _ => { return 4; },
    }
    match (toks[5]) {
        TokKw(t) => { if (t.name != "var") { return 5; } },
        _ => { return 6; },
    }
    match (toks[6]) {
        TokIdent(t) => { if (t.name != "x") { return 7; } },
        _ => { return 8; },
    }
    match (toks[8]) {
        TokInt(t) => { if (t.value != 42) { return 9; } },
        _ => { return 10; },
    }
    match (toks[10]) {
        TokKw(t) => { if (t.name != "return") { return 11; } },
        _ => { return 12; },
    }
    match (toks[13]) {
        TokPunct(t) => { if (t.text != "}") { return 13; } },
        _ => { return 14; },
    }
    match (toks[14]) {
        TokEof(_) => { },
        _ => { return 15; },
    }

    // Underscore in idents — __alloc_u8, is_digit lex as one.
    var t2: Token[] = tokenize("__alloc_u8 is_digit _pad");
    if (t2.len() != 4) { return 200 + t2.len(); }
    match (t2[0]) {
        TokIdent(t) => { if (t.name != "__alloc_u8") { return 16; } },
        _ => { return 17; },
    }
    match (t2[1]) {
        TokIdent(t) => { if (t.name != "is_digit") { return 18; } },
        _ => { return 19; },
    }
    match (t2[2]) {
        TokIdent(t) => { if (t.name != "_pad") { return 20; } },
        _ => { return 21; },
    }

    // String literal with escape sequences — \n, \t, \", \\.
    var t3: Token[] = tokenize("\"hello\\nworld\\t!\"");
    if (t3.len() != 2) { return 300 + t3.len(); }
    match (t3[0]) {
        TokStr(t) => { if (t.value != "hello\nworld\t!") { return 22; } },
        _ => { return 23; },
    }
    var t4: Token[] = tokenize("\"a\\\"b\\\\c\"");
    match (t4[0]) {
        TokStr(t) => { if (t.value != "a\"b\\c") { return 24; } },
        _ => { return 25; },
    }

    // Comment at EOF (no trailing newline).
    var t5: Token[] = tokenize("var x // tail");
    if (t5.len() != 3) { return 400 + t5.len(); }
    match (t5[0]) {
        TokKw(t) => { if (t.name != "var") { return 26; } },
        _ => { return 27; },
    }
    match (t5[1]) {
        TokIdent(t) => { if (t.name != "x") { return 28; } },
        _ => { return 29; },
    }

    // Sized numeric type names lex as keywords (i32 / usize etc).
    var t6: Token[] = tokenize("i32 i64 usize f64 string");
    if (t6.len() != 6) { return 500 + t6.len(); }
    match (t6[0]) {
        TokKw(t) => { if (t.name != "i32") { return 30; } },
        _ => { return 31; },
    }
    match (t6[2]) {
        TokKw(t) => { if (t.name != "usize") { return 32; } },
        _ => { return 33; },
    }
    match (t6[4]) {
        TokKw(t) => { if (t.name != "string") { return 34; } },
        _ => { return 35; },
    }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (lexer v6)", code)
	}
}

// Lexer-in-lang v5: hex integer literals (`0x1F`, `0XFF`) +
// the carry-over numeric type-suffix recognition (`0x10i64`).
// Recognises `0x` / `0X` prefix, scans hex digits via the
// prelude's `is_hex_digit()` (PR #397), accumulates the value
// via `hex_value(b)`. Bare `0x` (no hex digits) falls back
// to two tokens — `0` then ident `x` — matching Go's behaviour.
//
// Numeric accumulators (`numV`, `numSfx`) are HOISTED to
// function scope. Wasm names locals by lang identifier and
// rejects sibling-scope duplicates; the hex + decimal arms
// would each declare `var v` / `var sfx` and collide. The
// hoisting is the recommended workaround the prelude already
// uses (see `__map_hash`'s "Single shared `h` declaration"
// comment).
func TestArm64LexerV5(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32, base: i32, suffix: string }
struct TokIdent { name: string }
struct TokKw    { name: string }
struct TokStr   { value: string }
struct TokPunct { text: string }
struct TokEof   { _pad: i32 }

type Token = TokInt | TokIdent | TokKw | TokStr | TokPunct | TokEof;

function is_keyword(name: string): boolean {
    return name == "function" || name == "var" || name == "if";
}

function hex_value(b: i32): i32 {
    if (b >= 48 && b <= 57) { return b - 48; }
    if (b >= 97 && b <= 102) { return b - 87; }
    return b - 55;
}

function read_num_suffix(src: string, i: i32): string {
    var n: i32 = src.len();
    if (i + 3 > n) { return ""; }
    var a: i32 = src[i] as i32;
    var b: i32 = src[i + 1] as i32;
    var c: i32 = src[i + 2] as i32;
    if (!(a == 105 || a == 117 || a == 102)) { return ""; }
    if (b == 51 && c == 50) { return src[i : i + 3] + ""; }
    if (b == 54 && c == 52) { return src[i : i + 3] + ""; }
    return "";
}

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    var numV: i32 = 0;
    var numSfx: string = "";
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if (b == 48 && i + 1 < n && (src[i + 1] == 120 || src[i + 1] == 88)) {
            if (i + 2 >= n || !(src[i + 2] as i32).is_ascii_hex_digit()) {
                toks = toks.append(TokInt { value: 0, base: 10, suffix: "" });
                i = i + 1;
            } else {
                i = i + 2;
                numV = 0;
                while (i < n && (src[i] as i32).is_ascii_hex_digit()) {
                    numV = numV * 16 + hex_value(src[i] as i32);
                    i = i + 1;
                }
                numSfx = read_num_suffix(src, i);
                if (numSfx.len() > 0) { i = i + numSfx.len(); }
                toks = toks.append(TokInt { value: numV, base: 16, suffix: numSfx });
            }
        } else if ((b as i32).is_ascii_digit()) {
            numV = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                numV = numV * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            numSfx = read_num_suffix(src, i);
            if (numSfx.len() > 0) { i = i + numSfx.len(); }
            toks = toks.append(TokInt { value: numV, base: 10, suffix: numSfx });
        } else if ((b as i32).is_ascii_alpha()) {
            var start: i32 = i;
            while (i < n && (src[i] as i32).is_ascii_alnum()) { i = i + 1; }
            var name: str = src[start:i];
            if (is_keyword(name)) {
                toks = toks.append(TokKw { name: name + "" });
            } else {
                toks = toks.append(TokIdent { name: name + "" });
            }
        } else {
            toks = toks.append(TokPunct { text: src[i : i + 1] + "" });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function main(): i32 {
    var toks: Token[] = tokenize("0x1F 0xab 0XFF 42 0x10i64");
    if (toks.len() != 6) { return 100 + toks.len(); }
    match (toks[0]) {
        TokInt(t) => {
            if (t.value != 31) { return 1; }
            if (t.base != 16) { return 2; }
        },
        _ => { return 3; },
    }
    match (toks[1]) {
        TokInt(t) => {
            if (t.value != 171) { return 4; }
            if (t.base != 16) { return 5; }
        },
        _ => { return 6; },
    }
    match (toks[2]) {
        TokInt(t) => {
            if (t.value != 255) { return 7; }
            if (t.base != 16) { return 8; }
        },
        _ => { return 9; },
    }
    match (toks[3]) {
        TokInt(t) => {
            if (t.value != 42) { return 10; }
            if (t.base != 10) { return 11; }
        },
        _ => { return 12; },
    }
    match (toks[4]) {
        TokInt(t) => {
            if (t.value != 16) { return 13; }
            if (t.base != 16) { return 14; }
            if (t.suffix != "i64") { return 15; }
        },
        _ => { return 16; },
    }
    var t2: Token[] = tokenize("0x");
    if (t2.len() != 3) { return 200 + t2.len(); }
    match (t2[0]) {
        TokInt(t) => { if (t.value != 0) { return 17; } },
        _ => { return 18; },
    }
    match (t2[1]) {
        TokIdent(t) => { if (t.name != "x") { return 19; } },
        _ => { return 20; },
    }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (lexer v5)", code)
	}
}

// Lexer-in-lang v4: adds numeric type-suffix recognition
// (`42i64` → TokInt{value: 42, suffix: "i64"}). Suffixes
// accepted: `i32`, `i64`, `u32`, `u64`, `f32`, `f64` — the
// six numeric widths lang exposes. The match is greedy but
// strict: only a complete 3-byte suffix steals the bytes;
// `42i6` lexes as bare `42` followed by ident `i6`. Pattern
// mirrors Rust's numeric-literal suffix recognition.
func TestArm64LexerV4(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32, suffix: string }
struct TokIdent { name: string }
struct TokKw    { name: string }
struct TokStr   { value: string }
struct TokPunct { text: string }
struct TokEof   { _pad: i32 }

type Token = TokInt | TokIdent | TokKw | TokStr | TokPunct | TokEof;

function is_keyword(name: string): boolean {
    return name == "function" || name == "var" || name == "if" ||
           name == "else" || name == "while" || name == "return" ||
           name == "true" || name == "false" || name == "match" ||
           name == "type" || name == "struct" || name == "enum";
}

function read_num_suffix(src: string, i: i32): string {
    var n: i32 = src.len();
    if (i + 3 > n) { return ""; }
    var a: i32 = src[i] as i32;
    var b: i32 = src[i + 1] as i32;
    var c: i32 = src[i + 2] as i32;
    if (!(a == 105 || a == 117 || a == 102)) { return ""; } // i / u / f
    if (b == 51 && c == 50) { return src[i : i + 3] + ""; }      // 32
    if (b == 54 && c == 52) { return src[i : i + 3] + ""; }      // 64
    return "";
}

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            var sfx: string = read_num_suffix(src, i);
            if (sfx.len() > 0) { i = i + sfx.len(); }
            toks = toks.append(TokInt { value: v, suffix: sfx });
        } else if ((b as i32).is_ascii_alpha()) {
            var start: i32 = i;
            while (i < n && (src[i] as i32).is_ascii_alnum()) { i = i + 1; }
            var name: str = src[start:i];
            if (is_keyword(name)) {
                toks = toks.append(TokKw { name: name + "" });
            } else {
                toks = toks.append(TokIdent { name: name + "" });
            }
        } else {
            toks = toks.append(TokPunct { text: src[i : i + 1] + "" });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function main(): i32 {
    // Full suffix path.
    var toks: Token[] = tokenize("var x = 42i64; var y = 7u32; var z = 99;");
    if (toks.len() != 16) { return 100 + toks.len(); }
    match (toks[3]) {
        TokInt(t) => {
            if (t.value != 42) { return 1; }
            if (t.suffix != "i64") { return 2; }
        },
        _ => { return 3; },
    }
    match (toks[8]) {
        TokInt(t) => {
            if (t.value != 7) { return 4; }
            if (t.suffix != "u32") { return 5; }
        },
        _ => { return 6; },
    }
    match (toks[13]) {
        TokInt(t) => {
            if (t.value != 99) { return 7; }
            if (t.suffix != "") { return 8; }
        },
        _ => { return 9; },
    }
    // Incomplete suffix: 42i6 → 42 + ident("i6").
    var t2: Token[] = tokenize("42i6");
    if (t2.len() != 3) { return 200 + t2.len(); }
    match (t2[0]) {
        TokInt(t) => {
            if (t.value != 42) { return 10; }
            if (t.suffix != "") { return 11; }
        },
        _ => { return 12; },
    }
    match (t2[1]) {
        TokIdent(t) => { if (t.name != "i6") { return 13; } },
        _ => { return 14; },
    }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (lexer v4)", code)
	}
}

// Lexer-in-lang v3: builds on v2 by adding block comments
// (`/* ... */`), the `\n` / `\t` / `\r` string escapes via a
// single `unescape(b: i32): i32` helper, AND swaps the inline
// `is_digit` / `is_alpha` / `is_alnum` / `is_ws` helpers for
// the prelude's `(b: i32).is_*` methods from PR #397. The
// classifier swap is the more important change: it's the
// first real-workload validation that the prelude additions
// compose into a hand-rolled lexer without re-declaring local
// helpers.
//
// Token surface still ~10% of the real lang lexer but the
// shape now matches enough that the bigger port can land
// incrementally without re-deriving the tokenisation skeleton.
func TestArm64LexerV3(t *testing.T) {
	src := `
import "std/i32";
struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokKw    { name: string }
struct TokStr   { value: string }
struct TokPunct { text: string }
struct TokEof   { _pad: i32 }

type Token = TokInt | TokIdent | TokKw | TokStr | TokPunct | TokEof;

function is_keyword(name: string): boolean {
    return name == "function" || name == "var" || name == "if" ||
           name == "else" || name == "while" || name == "return" ||
           name == "true" || name == "false" || name == "match" ||
           name == "type" || name == "struct" || name == "enum";
}

function unescape(b: i32): i32 {
    if (b == 110) { return 10; }   // \n
    if (b == 116) { return 9; }    // \t
    if (b == 114) { return 13; }   // \r
    return b;                       // \\ \" or unknown — pass through
}

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if ((b as i32).is_ascii_white_space()) {
            i = i + 1;
        } else if (b == 47 && i + 1 < n && src[i + 1] == 47) {
            i = i + 2;
            while (i < n && src[i] != 10) { i = i + 1; }
        } else if (b == 47 && i + 1 < n && src[i + 1] == 42) {
            i = i + 2;
            var closed: boolean = false;
            while (i + 1 < n) {
                if (src[i] == 42 && src[i + 1] == 47) {
                    i = i + 2;
                    closed = true;
                    break;
                }
                i = i + 1;
            }
            // Unterminated: eat to EOF rather than letting the
            // trailing byte (i = n-1 on exit) fall through and
            // get tokenised as punct.
            if (!closed) { i = n; }
        } else if (b == 34) {
            var out: string = "";
            i = i + 1;
            while (i < n && src[i] != 34) {
                if (src[i] == 92 && i + 1 < n) {
                    var resolved: i32 = unescape(src[i + 1] as i32);
                    var buf: u8[] = __alloc_u8(1);
                    buf = buf.with(0, resolved as u8);
                    out = out + string_from_bytes_unchecked(buf);
                    i = i + 2;
                } else {
                    out = out + src[i : i + 1];
                    i = i + 1;
                }
            }
            if (i < n) { i = i + 1; }
            toks = toks.append(TokStr { value: out });
        } else if ((b as i32).is_ascii_digit()) {
            var v: i32 = 0;
            while (i < n && (src[i] as i32).is_ascii_digit()) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if ((b as i32).is_ascii_alpha()) {
            var start: i32 = i;
            while (i < n && (src[i] as i32).is_ascii_alnum()) { i = i + 1; }
            var name: str = src[start:i];
            if (is_keyword(name)) {
                toks = toks.append(TokKw { name: name + "" });
            } else {
                toks = toks.append(TokIdent { name: name + "" });
            }
        } else if ((b == 61 || b == 33 || b == 60 || b == 62) &&
                   i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { text: src[i : i + 2] + "" });
            i = i + 2;
        } else if (b == 61 && i + 1 < n && src[i + 1] == 62) {
            toks = toks.append(TokPunct { text: src[i : i + 2] + "" });
            i = i + 2;
        } else {
            toks = toks.append(TokPunct { text: src[i : i + 1] + "" });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function main(): i32 {
    var toks: Token[] = tokenize("/* block */ var s = \"a\\nb\";");
    // var s = "a\nb" ; EOF = 6 tokens
    if (toks.len() != 6) { return 100 + toks.len(); }
    match (toks[0]) {
        TokKw(t) => { if (t.name != "var") { return 1; } },
        _ => { return 2; },
    }
    match (toks[3]) {
        TokStr(t) => {
            if (t.value.len() != 3) { return 3; }
            if (t.value[0] != 97) { return 4; }   // 'a'
            if (t.value[1] != 10) { return 5; }   // resolved '\n'
            if (t.value[2] != 98) { return 6; }   // 'b'
        },
        _ => { return 7; },
    }
    // Tab + carriage-return + unknown-escape (\Z passes through).
    var t2: Token[] = tokenize("\"\\t\\r\\Z\"");
    match (t2[0]) {
        TokStr(t) => {
            if (t.value.len() != 3) { return 8; }
            if (t.value[0] != 9) { return 9; }    // '\t'
            if (t.value[1] != 13) { return 10; }  // '\r'
            if (t.value[2] != 90) { return 11; }  // 'Z' (unknown escape)
        },
        _ => { return 12; },
    }
    // Block comment with a star inside but no closing
    // star-slash keeps the lexer alive past EOF without
    // infinite-looping.
    var t3: Token[] = tokenize("/* unterminated * comment");
    // Just EOF — the unterminated comment ate everything.
    if (t3.len() != 1) { return 13; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (lexer v3)", code)
	}
}

// Extended lexer-in-lang: adds string literals (with `\\` /
// `\"` escapes), multi-character operators (`==`, `!=`, `<=`,
// `>=`, `=>`), keyword recognition vs identifier, and line
// comments. Pushes more lang features through the pipeline
// than `TestArm64LexerInLang`:
//
//   - String concatenation (`out = out + src[i:i+1]`) inside
//     a loop — exercises strcat-in-loop allocation pattern
//   - Multi-arm boolean chains in the keyword classifier
//   - Mixed `match` over union variants with both `_` wildcard
//     and explicit-variant arms
//
// Token surface still ~5% of the real lang lexer but enough
// to drive a tiny self-contained source through end-to-end.
// `TestArm64LexerInLang` keeps the minimum-viable shape; this
// test pins the next increment so we catch regressions on the
// bigger features before the full port lands.
func TestArm64LexerV2(t *testing.T) {
	src := `struct TokInt   { value: i32 }
struct TokIdent { name: string }
struct TokKw    { name: string }
struct TokStr   { value: string }
struct TokPunct { text: string }
struct TokEof   { _pad: i32 }

type Token = TokInt | TokIdent | TokKw | TokStr | TokPunct | TokEof;

function is_digit(b: i32): boolean { return b >= 48 && b <= 57; }
function is_alpha(b: i32): boolean {
    return (b >= 65 && b <= 90) || (b >= 97 && b <= 122) || b == 95;
}
function is_alnum(b: i32): boolean { return is_digit(b) || is_alpha(b); }
function is_ws(b: i32): boolean {
    return b == 32 || b == 9 || b == 10 || b == 13;
}

function is_keyword(name: string): boolean {
    return name == "function" || name == "var" || name == "if" ||
           name == "else" || name == "while" || name == "return" ||
           name == "true" || name == "false" || name == "match" ||
           name == "type" || name == "struct" || name == "enum";
}

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if (is_ws(b)) {
            i = i + 1;
        } else if (b == 47 && i + 1 < n && src[i + 1] == 47) {
            i = i + 2;
            while (i < n && src[i] != 10) { i = i + 1; }
        } else if (b == 34) {
            var out: string = "";
            i = i + 1;
            while (i < n && src[i] != 34) {
                if (src[i] == 92 && i + 1 < n) {
                    out = out + src[i + 1 : i + 2];
                    i = i + 2;
                } else {
                    out = out + src[i : i + 1];
                    i = i + 1;
                }
            }
            if (i < n) { i = i + 1; }
            toks = toks.append(TokStr { value: out });
        } else if (is_digit(b)) {
            var v: i32 = 0;
            while (i < n && is_digit(src[i] as i32)) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if (is_alpha(b)) {
            var start: i32 = i;
            while (i < n && is_alnum(src[i] as i32)) { i = i + 1; }
            var name: str = src[start:i];
            if (is_keyword(name)) {
                toks = toks.append(TokKw { name: name + "" });
            } else {
                toks = toks.append(TokIdent { name: name + "" });
            }
        } else if ((b == 61 || b == 33 || b == 60 || b == 62) &&
                   i + 1 < n && src[i + 1] == 61) {
            toks = toks.append(TokPunct { text: src[i : i + 2] + "" });
            i = i + 2;
        } else if (b == 61 && i + 1 < n && src[i + 1] == 62) {
            toks = toks.append(TokPunct { text: src[i : i + 2] + "" });
            i = i + 2;
        } else {
            toks = toks.append(TokPunct { text: src[i : i + 1] + "" });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function main(): i32 {
    var toks: Token[] = tokenize("var x = 42; // a comment\nfunction f() { return \"hi\"; }");
    // var x = 42 ; function f ( ) { return "hi" ; } EOF = 15 tokens.
    if (toks.len() != 15) { return 100 + toks.len(); }
    match (toks[0]) {
        TokKw(t) => { if (t.name != "var") { return 1; } },
        _ => { return 2; },
    }
    match (toks[1]) {
        TokIdent(t) => { if (t.name != "x") { return 3; } },
        _ => { return 4; },
    }
    match (toks[2]) {
        TokPunct(t) => { if (t.text != "=") { return 5; } },
        _ => { return 6; },
    }
    match (toks[3]) {
        TokInt(t) => { if (t.value != 42) { return 7; } },
        _ => { return 8; },
    }
    match (toks[4]) {
        TokPunct(t) => { if (t.text != ";") { return 9; } },
        _ => { return 10; },
    }
    match (toks[5]) {
        TokKw(t) => { if (t.name != "function") { return 11; } },
        _ => { return 12; },
    }
    match (toks[10]) {
        TokKw(t) => { if (t.name != "return") { return 13; } },
        _ => { return 14; },
    }
    match (toks[11]) {
        TokStr(t) => { if (t.value != "hi") { return 15; } },
        _ => { return 16; },
    }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (extended lexer pipeline)", code)
	}
}

// Byte-level ASCII classifiers — `(b: i32).is_ascii_digit()` /
// `is_alpha()` / `is_alnum()` / `is_ascii_white_space()` /
// `is_hex_digit()` / `is_ascii()`. Useful for hand-rolled
// lexers and parsing routines; the lexer-in-lang spike
// programs inline these today, this PR promotes them to
// reusable prelude helpers.
func TestArm64ByteClassifiers(t *testing.T) {
	src := `
import "std/i32";
function main(): i32 {
    if (!(48 as i32).is_ascii_digit()) { return 1; }      // '0'
    if (!(57 as i32).is_ascii_digit()) { return 2; }      // '9'
    if ((47 as i32).is_ascii_digit()) { return 3; }       // '/' is not a digit
    if (!(65 as i32).is_ascii_alpha()) { return 4; }      // 'A'
    if (!(122 as i32).is_ascii_alpha()) { return 5; }     // 'z'
    if ((64 as i32).is_ascii_alpha()) { return 6; }       // '@' is not alpha
    if (!(48 as i32).is_ascii_alnum()) { return 7; }
    if (!(65 as i32).is_ascii_alnum()) { return 8; }
    if (!(32 as i32).is_ascii_white_space()) { return 9; }
    if (!(10 as i32).is_ascii_white_space()) { return 10; }
    if (!(97 as i32).is_ascii_hex_digit()) { return 11; } // 'a'
    if (!(70 as i32).is_ascii_hex_digit()) { return 12; } // 'F'
    if ((103 as i32).is_ascii_hex_digit()) { return 13; } // 'g' is not hex
    if (!(127 as i32).is_ascii()) { return 14; }
    if ((128 as i32).is_ascii()) { return 15; }     // 0x80 is past ASCII
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (byte classifiers)", code)
	}
}

// `cmp.sort` / `cmp.sort_desc` over i32 — non-mutating stable merge
// sort (input untouched; n < 2 inputs are returned as-is, larger
// ones as a fresh array). Locks the generic sort's behavioral
// contract on arm64: ordering, empty/singleton, and the input-preserved
// property (the retired `sort_i32_asc` / `sort_i32_desc`, #5397).
func TestArm64SortI32(t *testing.T) {
	src := `
import "core/cmp";
function main(): i32 {
    var xs: i32[] = [3, 1, 4, 1, 5, 9, 2, 6, 5];
    var asc: i32[] = cmp.sort(xs);
    if (asc.len() != 9) { return 1; }
    if (asc[0] != 1) { return 2; }
    if (asc[1] != 1) { return 3; }
    if (asc[8] != 9) { return 4; }
    if (xs[0] != 3) { return 5; }  // input untouched

    var desc: i32[] = cmp.sort_desc(xs);
    if (desc[0] != 9) { return 6; }
    if (desc[8] != 1) { return 7; }

    var empty: i32[] = [];
    if ((cmp.sort(empty)).len() != 0) { return 8; }

    var one: i32[] = [42];
    var one_sorted: i32[] = cmp.sort(one);
    if (one_sorted.len() != 1) { return 9; }
    if (one_sorted[0] != 42) { return 10; }

    var negs: i32[] = [3, 0 - 5, 0, 0 - 1, 2];
    var n_sorted: i32[] = cmp.sort(negs);
    if (n_sorted[0] != 0 - 5) { return 11; }
    if (n_sorted[4] != 3) { return 12; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (sort_i32)", code)
	}
}

// Tiny lexer written in lang — recognises integer literals,
// identifiers, and single-byte punctuation. Validates that:
//
//   - `type Token = TokInt | TokIdent | ...` (union types
//     from PR #390) handles the AST-shaped sum
//   - `Add { ... }` flows into a Token-typed slot via the
//     implicit wrap (PR #392)
//   - `s[i]` byte access, `s[lo:hi]` slicing, `arr.push`,
//     and `match` over union variants all compose
//   - The frameLoad / frameStore split (this PR) handles
//     the deeper-than-256-byte frame the lexer's match arms
//     produce on arm64
//
// First building block toward self-host: porting the real
// lexer to lang is the smallest realistic milestone (see
// docs/ROADMAP-AND-SELF-HOSTING.md "Realistic first-port
// milestone"). This test pins a minimum-viable lexer shape
// so we catch regressions before the bigger port lands.
func TestArm64LexerInLang(t *testing.T) {
	src := `struct TokInt { value: i32 }
struct TokIdent { name: string }
struct TokPunct { ch: i32 }
struct TokEof { _pad: i32 }

type Token = TokInt | TokIdent | TokPunct | TokEof;

function is_digit(b: i32): boolean { return b >= 48 && b <= 57; }
function is_alpha(b: i32): boolean {
    return (b >= 65 && b <= 90) || (b >= 97 && b <= 122) || b == 95;
}
function is_alnum(b: i32): boolean { return is_digit(b) || is_alpha(b); }

function tokenize(src: string): Token[] {
    var toks: Token[] = [];
    var n: i32 = src.len();
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = src[i] as i32;
        if (b == 32 || b == 9 || b == 10 || b == 13) {
            i = i + 1;
        } else if (is_digit(b)) {
            var v: i32 = 0;
            while (i < n && is_digit(src[i] as i32)) {
                v = v * 10 + ((src[i] as i32) - 48);
                i = i + 1;
            }
            toks = toks.append(TokInt { value: v });
        } else if (is_alpha(b)) {
            var start: i32 = i;
            while (i < n && is_alnum(src[i] as i32)) { i = i + 1; }
            toks = toks.append(TokIdent { name: src[start:i] + "" });
        } else {
            toks = toks.append(TokPunct { ch: b });
            i = i + 1;
        }
    }
    toks = toks.append(TokEof { _pad: 0 });
    return toks;
}

function main(): i32 {
    var toks: Token[] = tokenize("foo + 42");
    if (toks.len() != 4) { return 1; }
    match (toks[0]) {
        TokIdent(t) => { if (t.name != "foo") { return 2; } },
        TokInt(_) => { return 3; },
        TokPunct(_) => { return 4; },
        TokEof(_) => { return 5; },
    }
    match (toks[1]) {
        TokPunct(t) => { if (t.ch != 43) { return 6; } },
        _ => { return 7; },
    }
    match (toks[2]) {
        TokInt(t) => { if (t.value != 42) { return 8; } },
        _ => { return 9; },
    }
    match (toks[3]) {
        TokEof(_) => { return 0; },
        _ => { return 10; },
    }
    return 11;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (lexer pipeline)", code)
	}
}

// Implicit struct → union wrap on arm64: a bare member-struct
// literal flows into a union-typed position without explicit
// `MemberName(...)` re-wrap. Exercises wrap at var-init, call-
// arg, return, and assignment sites — the natural shape for
// AST-style code where the consumer code keeps constructing
// `Add { l: 1, r: 2 }` and letting the type system route it.
func TestArm64UnionImplicitWrap(t *testing.T) {
	src := `
struct Add { l: i32, r: i32 }
struct Mul { l: i32, r: i32 }
struct Lit { v: i32 }

type Expr = Add | Mul | Lit;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Mul(m) => { return m.l * m.r; },
        Lit(l) => { return l.v; },
    }
}

// Implicit wrap at return site.
function mk_add(l: i32, r: i32): Expr {
    return Add { l: l, r: r };
}

function main(): i32 {
    // Var init: bare struct literal → union.
    var a: Expr = Add { l: 2, r: 3 };
    // Call-arg: bare literal flows into the Expr param.
    var sum: i32 = eval(Lit { v: 5 });
    // Assignment: re-bind a different member.
    a = Mul { l: 2, r: sum };
    // Return-site shape — value comes back through mk_add's
    // implicit-wrap path.
    var built: Expr = mk_add(1, 2);
    return eval(a) + eval(built) + sum;
}`
	_, code := compileAndRunArm64(t, src)
	// 2 * 5 (Mul) + 1+2 (Add) + 5 (Lit) = 18.
	if code != 18 {
		t.Errorf("got %d, want 18 (2*5 + 1+2 + 5)", code)
	}
}

// `s.lines()` on arm64 — exercises the prelude function over
// the two-word ABI: `s[i]` byte indexing, `s[lo:hi]` slicing,
// `out.append(line)`, and the array-result return path.
// Runtime template substitution via the prelude format()
// function. Walks fmt and replaces each {} placeholder with
// args[i]. Mirrors Python str.format() / Rust format!()
// minimal subset. Covers what f-strings DON'T: dynamic
// templates (read from a config / locale table / error
// catalogue), and programmatic diagnostic-string assembly
// where the template lives in a const-table rather than at
// every call site.
//
// Cases:
//   - basic substitution: matched arg count
//   - underfilled: more {} than args → literal {} tail
//   - overfilled: more args than {} → extras silently dropped
//   - no placeholders → identity (still cheap)
//   - empty fmt → empty result
//   - chained with to_string() for i32 args
//
// String-array methods — join / index_of / contains. All
// three dispatch via the constrained-string-receiver pattern
// in the checker; bodies live in the prelude as plain loops.
// Tests cover the happy paths plus boundary cases (empty
// array, not-found returns -1 from index_of and false from
// contains, separator edge cases for join).
func TestArm64ArrayJoin(t *testing.T) {
	src := `
import "std/string";
import "std/array";
function main(): i32 {
    if (["alice", "bob", "ciri"].join(", ") != "alice, bob, ciri") { return 1; }
    if (["a", "b", "c"].join("") != "abc") { return 2; }
    var empty: string[] = [];
    if (empty.join(", ") != "") { return 3; }
    if (["solo"].join("|") != "solo") { return 4; }
    if (["x", "y"].join(" -> ") != "x -> y") { return 5; }
    // Use the result of split(...).join(...) — round trip
    // through a string list (with empty-element preservation).
    if ("a,b,c".split(",").join(";") != "a;b;c") { return 6; }

    // index_of / contains — happy paths
    var kws: string[] = ["if", "else", "while", "function", "return"];
    if (kws.index_of("if") != 0) { return 7; }
    if (kws.index_of("return") != 4) { return 8; }
    if (kws.contains("else") == false) { return 9; }

    // index_of — not found returns -1; contains returns false.
    if (kws.index_of("for") != (0 - 1)) { return 10; }
    if (kws.contains("for")) { return 11; }

    // Empty array — index_of(any) = -1, contains(any) = false.
    var none: string[] = [];
    if (none.index_of("x") != (0 - 1)) { return 12; }
    if (none.contains("x")) { return 13; }

    // reverse — fresh array, original untouched.
    var xs: string[] = ["a", "b", "c", "d"];
    if (xs.reverse().join(",") != "d,c,b,a") { return 14; }
    if (xs.join(",") != "a,b,c,d") { return 15; }
    // Single element — reverse is identity.
    if (["solo"].reverse().join("|") != "solo") { return 16; }
    // Empty array — empty result.
    if ((none.reverse()).len() != 0) { return 17; }
    // Reverse twice — identity.
    if (xs.reverse().reverse().join(",") != "a,b,c,d") { return 18; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (Array.join/index_of/contains/reverse)", code)
	}
}

func TestArm64Format(t *testing.T) {
	src := `
import "std/format";
import "std/i32";
function main(): i32 {
    if (format.format("hello {}, age {}", ["alice", "30"]) != "hello alice, age 30") { return 1; }
    if (format.format("{}-{}-{}", ["a", "b", "c"]) != "a-b-c") { return 2; }
    if (format.format("{} and {}", ["only"]) != "only and {}") { return 3; }
    if (format.format("just {}", ["one", "two", "three"]) != "just one") { return 4; }
    if (format.format("no holes here", ["unused"]) != "no holes here") { return 5; }
    if (format.format("", ["x"]) != "") { return 6; }
    if (format.format("count = {}", [(42).to_string()]) != "count = 42") { return 7; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (format)", code)
	}
}

// String surface additions per STDLIB-ROADMAP item #15:
//
//	s.fields()                       — whitespace split, no empties
//	s.eq_ignore_ascii_case(other)    — ASCII case-fold equality
//	s.strip_prefix(p) / strip_suffix(s)  — Option[string] companions to
//	                                       starts_with / ends_with
//
// HTTP header parsing is the main motivator (case-insensitive
// header-name compare, prefix-stripping path components,
// whitespace-tolerant value tokenisation).
// Radix parse/format per STDLIB-ROADMAP item #14:
//
//	parse_int_radix(s, base) Option[i32]
//	int_to_string_radix(n, base) string
//
// Supported bases: 2..36 (digits 0-9 then a-z, case-insensitive
// on parse). Covers hex addressing, binary debug dumps, octal
// permissions, base-36 short IDs.
// Path manipulation (string-level, POSIX) per STDLIB-ROADMAP
// item #8: path_join / path_parent / path_file_name /
// path_extension. Pure string ops — no FS interaction.
// Big bundle from STDLIB-ROADMAP: most of item #3 (pad_start /
// pad_end / split_once / trim_start_matches / trim_end_matches
// / replace_n), plus string-count (a small adjacent helper),
// plus i32 array reductions (sum / max / min). All small
// prelude additions wired through the existing constrained-
// receiver dispatch.
// Thirty-first stdlib bundle: string word_at /
// word_count_min / longest_word / is_quoted, i32[] gcd_all
// / lcm_all / abs_each, string[] all_starts_with /
// all_ends_with. 9 helpers.
//
// `gcd_all` / `lcm_all` fold the existing scalar `gcd` /
// `lcm` helpers. Both use abs() on inputs (gcd is
// sign-invariant; lcm without abs would yield nonsense
// for negative inputs). Empty array → None.
//
// `is_quoted` only checks the outer shape (matching first
// and last byte from {", `, '}); it doesn't validate
// escaping or interior quote pairing.
func TestArm64StdlibBundle31(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    match ("hello world foo".word_at(0)) {
        Some(w) => { if (w != "hello") { return 1; } },
        None => { return 2; },
    }
    match ("hello world foo".word_at(2)) {
        Some(w) => { if (w != "foo") { return 3; } },
        None => { return 4; },
    }
    match ("hello".word_at(1)) { Some(_) => { return 5; }, None => { } }
    match ("".word_at(0)) { Some(_) => { return 6; }, None => { } }
    match ("  multi   space  ".word_at(0)) {
        Some(w) => { if (w != "multi") { return 7; } },
        None => { return 8; },
    }

    if (!["foo_a", "foo_b", "foo_c"].all_starts_with("foo")) { return 10; }
    if (["foo", "bar"].all_starts_with("f")) { return 11; }
    var empty: string[] = [];
    if (!empty.all_starts_with("x")) { return 12; }
    if (!["abc"].all_starts_with("")) { return 13; }

    if (!["a.txt", "b.txt", "c.txt"].all_ends_with(".txt")) { return 20; }
    if (["a.txt", "b.png"].all_ends_with(".txt")) { return 21; }
    if (!empty.all_ends_with(".txt")) { return 22; }

    match ([12, 18, 24].gcd_all()) {
        Some(g) => { if (g != 6) { return 30; } },
        None => { return 31; },
    }
    match ([7, 13, 11].gcd_all()) {
        Some(g) => { if (g != 1) { return 32; } },
        None => { return 33; },
    }
    match ([5].gcd_all()) {
        Some(g) => { if (g != 5) { return 34; } },
        None => { return 35; },
    }
    var empty_i: i32[] = [];
    match (empty_i.gcd_all()) { Some(_) => { return 36; }, None => { } }

    match ([2, 3, 4].lcm_all()) {
        Some(l) => { if (l != 12) { return 40; } },
        None => { return 41; },
    }
    match ([6, 8].lcm_all()) {
        Some(l) => { if (l != 24) { return 42; } },
        None => { return 43; },
    }
    match ([5].lcm_all()) {
        Some(l) => { if (l != 5) { return 44; } },
        None => { return 45; },
    }
    match (empty_i.lcm_all()) { Some(_) => { return 46; }, None => { } }

    if ("a bb ccc dddd".word_count_min(3) != 2) { return 50; }
    if ("a bb ccc dddd".word_count_min(1) != 4) { return 51; }
    if ("a bb ccc".word_count_min(10) != 0) { return 52; }
    if ("".word_count_min(1) != 0) { return 53; }

    match ("the quick brown fox".longest_word()) {
        Some(w) => { if (w != "quick") { return 60; } },
        None => { return 61; },
    }
    match ("solo".longest_word()) {
        Some(w) => { if (w != "solo") { return 62; } },
        None => { return 63; },
    }
    match ("".longest_word()) { Some(_) => { return 64; }, None => { } }
    match ("  ".longest_word()) { Some(_) => { return 65; }, None => { } }

    var a: i32[] = [0 - 3, 5, 0 - 7].abs_each();
    if (a[0] != 3 || a[1] != 5 || a[2] != 7) { return 70; }
    if ((empty_i.abs_each()).len() != 0) { return 71; }

    if (!"\"hello\"".is_quoted()) { return 80; }
    if (!"'hi'".is_quoted()) { return 81; }
    if ("plain".is_quoted()) { return 82; }
    if ("\"unmatched".is_quoted()) { return 83; }
    if ("\"".is_quoted()) { return 84; }
    if ("".is_quoted()) { return 85; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 31)", code)
	}
}

// Thirtieth stdlib bundle: string replace_first /
// is_kebab_case / is_snake_case / shift_byte, i32[]
// first_index_of / pairwise_diffs, i32 factorial / is_prime.
// 8 helpers.
//
// `factorial` caps at 12! (largest factorial that fits in
// i32); n >= 13 or n < 0 returns 0 as the out-of-range
// sentinel. `is_prime` uses 6k±1 trial division, O(sqrt n).
// `shift_byte` is a Caesar-style byte rotation — useful for
// puzzles / toy obfuscation, not security.
func TestArm64StdlibBundle30(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    if ("aXbXc".replace_first("X", "_") != "a_bXc") { return 1; }
    if ("none here".replace_first("x", "y") != "none here") { return 2; }
    if ("".replace_first("a", "b") != "") { return 3; }

    if (!"hello-world".is_kebab_case()) { return 10; }
    if (!"abc".is_kebab_case()) { return 11; }
    if (!"a-b-c-1-2".is_kebab_case()) { return 12; }
    if ("Hello-world".is_kebab_case()) { return 13; }
    if ("-leading".is_kebab_case()) { return 14; }
    if ("trailing-".is_kebab_case()) { return 15; }
    if ("a--b".is_kebab_case()) { return 16; }
    if ("a_b".is_kebab_case()) { return 17; }
    if ("".is_kebab_case()) { return 18; }

    if (!"hello_world".is_snake_case()) { return 20; }
    if (!"abc".is_snake_case()) { return 21; }
    if (!"a_b_c_1_2".is_snake_case()) { return 22; }
    if ("Hello_world".is_snake_case()) { return 23; }
    if ("_leading".is_snake_case()) { return 24; }
    if ("trailing_".is_snake_case()) { return 25; }
    if ("a__b".is_snake_case()) { return 26; }
    if ("a-b".is_snake_case()) { return 27; }
    if ("".is_snake_case()) { return 28; }

    match ([3, 1, 4, 1, 5].first_index_of(1)) {
        Some(i) => { if (i != 1) { return 30; } },
        None => { return 31; },
    }
    match ([3, 1, 4].first_index_of(99)) { Some(_) => { return 32; }, None => { } }
    var empty: i32[] = [];
    match (empty.first_index_of(0)) { Some(_) => { return 33; }, None => { } }

    var d: i32[] = [10, 12, 15, 20].pairwise_diffs();
    if (d.len() != 3) { return 40; }
    if (d[0] != 2 || d[1] != 3 || d[2] != 5) { return 41; }
    if ((empty.pairwise_diffs()).len() != 0) { return 42; }
    var single: i32[] = [5].pairwise_diffs();
    if (single.len() != 0) { return 43; }

    if ((0).factorial() != 1) { return 50; }
    if ((1).factorial() != 1) { return 51; }
    if ((5).factorial() != 120) { return 52; }
    if ((10).factorial() != 3628800) { return 53; }
    if ((12).factorial() != 479001600) { return 54; }
    if ((13).factorial() != 0) { return 55; }
    if ((0 - 1).factorial() != 0) { return 56; }

    if (!(2).is_prime()) { return 60; }
    if (!(3).is_prime()) { return 61; }
    if (!(13).is_prime()) { return 62; }
    if (!(97).is_prime()) { return 63; }
    if ((1).is_prime()) { return 64; }
    if ((0).is_prime()) { return 65; }
    if ((4).is_prime()) { return 66; }
    if ((100).is_prime()) { return 67; }
    if ((0 - 7).is_prime()) { return 68; }

    if ("abc".shift_byte(1) != "bcd") { return 70; }
    if ("xyz".shift_byte(0 - 1) != "wxy") { return 71; }
    if ("".shift_byte(5) != "") { return 72; }
    if ("a".shift_byte(0) != "a") { return 73; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 30)", code)
	}
}

// Twenty-ninth stdlib bundle: i32[] min_max / reversed /
// every_positive, string without_chars / contains_only /
// split_at / count_chars_in, i32 is_perfect_square.
// 8 helpers.
//
// `min_max` returns `Option[(i32, i32)]` — both bounds in
// a single pass. `reversed` is the i32[] companion to the
// existing string[] `reverse` (they collapse to one once
// generic Array methods land). `every_positive` is
// vacuously true on an empty array, matching the all-of-X
// quantifier convention.
//
// `split_at` returns a `(string, string)` tuple; negative
// indices clamp to 0, out-of-range to len. `contains_only`
// is vacuously true on the empty string but false on a
// non-empty string with an empty set.
func TestArm64StdlibBundle29(t *testing.T) {
	src := `
import "std/array";
function main(): i32 {
    match ([3, 1, 4, 1, 5, 9, 2, 6].min_max()) {
        Some(t) => { if (t.0 != 1 || t.1 != 9) { return 1; } },
        None => { return 2; },
    }
    match ([7].min_max()) {
        Some(t) => { if (t.0 != 7 || t.1 != 7) { return 3; } },
        None => { return 4; },
    }
    var emp: i32[] = [];
    match (emp.min_max()) { Some(_) => { return 5; }, None => { } }

    var rev: i32[] = [1, 2, 3, 4].reversed();
    if (rev[0] != 4 || rev[1] != 3 || rev[2] != 2 || rev[3] != 1) { return 10; }
    if ((emp.reversed()).len() != 0) { return 11; }
    var single: i32[] = [7].reversed();
    if (single[0] != 7) { return 12; }

    if ("hello world".without_chars(" lo") != "hewrd") { return 20; }
    if ("abcabc".without_chars("a") != "bcbc") { return 21; }
    if ("xyz".without_chars("") != "xyz") { return 22; }
    if ("".without_chars("abc") != "") { return 23; }

    if (!"abc".contains_only("abcdef")) { return 30; }
    if ("abcX".contains_only("abc")) { return 31; }
    if (!"".contains_only("anything")) { return 32; }
    if (!"aaa".contains_only("a")) { return 33; }

    var sp1: (string, string) = "hello world".split_at(5);
    if (sp1.0 != "hello" || sp1.1 != " world") { return 40; }
    var sp2: (string, string) = "abc".split_at(0);
    if (sp2.0 != "" || sp2.1 != "abc") { return 41; }
    var sp3: (string, string) = "abc".split_at(3);
    if (sp3.0 != "abc" || sp3.1 != "") { return 42; }
    var sp4: (string, string) = "abc".split_at(10);
    if (sp4.0 != "abc" || sp4.1 != "") { return 43; }
    var sp5: (string, string) = "abc".split_at(0 - 1);
    if (sp5.0 != "" || sp5.1 != "abc") { return 44; }

    if (!(0).is_perfect_square()) { return 50; }
    if (!(1).is_perfect_square()) { return 51; }
    if (!(4).is_perfect_square()) { return 52; }
    if (!(100).is_perfect_square()) { return 53; }
    if ((2).is_perfect_square()) { return 54; }
    if ((99).is_perfect_square()) { return 55; }
    if ((0 - 4).is_perfect_square()) { return 56; }

    if ("hello".count_chars_in("aeiou") != 2) { return 60; }
    if ("xyz".count_chars_in("aeiou") != 0) { return 61; }
    if ("".count_chars_in("a") != 0) { return 62; }
    if ("aaa".count_chars_in("a") != 3) { return 63; }

    if (![1, 2, 3].every_positive()) { return 70; }
    if ([1, 0, 3].every_positive()) { return 71; }
    if ([1, 2, 0 - 1].every_positive()) { return 72; }
    if (!emp.every_positive()) { return 73; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 29)", code)
	}
}

// Twenty-eighth stdlib bundle: i32[] sorted_asc / sorted_desc
// / cumsum, string[] sorted_str_asc / sorted_str_desc, string
// ellipsis / first_line, i32 min_zero / sign_str. 9 helpers.
//
// The `sorted_*` variants are method wrappers around the
// existing free `sort_*` functions; they read better in
// pipelines (`arr.sorted_asc().take(3)`). They collapse to a
// single `sorted()` once generic Ord methods land.
//
// `ellipsis(n)` differs from `truncate(n, "...")`: with
// `n == 3` on a longer string, ellipsis returns "..." while
// truncate hard-truncates the source. Use ellipsis when you
// want truncation always to be visually marked.
func TestArm64StdlibBundle28(t *testing.T) {
	src := `
import "std/array";
function main(): i32 {
    var xs: i32[] = [3, 1, 4, 1, 5];
    var a: i32[] = xs.sorted_asc();
    if (a[0] != 1 || a[1] != 1 || a[2] != 3 || a[3] != 4 || a[4] != 5) { return 1; }
    var d: i32[] = xs.sorted_desc();
    if (d[0] != 5 || d[1] != 4 || d[2] != 3 || d[3] != 1 || d[4] != 1) { return 2; }
    var empty_i: i32[] = [];
    if ((empty_i.sorted_asc()).len() != 0) { return 3; }
    if ((empty_i.sorted_desc()).len() != 0) { return 4; }

    var ss: string[] = ["banana", "apple", "cherry"];
    var sa: string[] = ss.sorted_str_asc();
    if (sa[0] != "apple" || sa[1] != "banana" || sa[2] != "cherry") { return 10; }
    var sd: string[] = ss.sorted_str_desc();
    if (sd[0] != "cherry" || sd[1] != "banana" || sd[2] != "apple") { return 11; }
    var empty_s: string[] = [];
    if ((empty_s.sorted_str_asc()).len() != 0) { return 12; }

    var cs: i32[] = [1, 2, 3, 4].cumsum();
    if (cs[0] != 1 || cs[1] != 3 || cs[2] != 6 || cs[3] != 10) { return 20; }
    if ((empty_i.cumsum()).len() != 0) { return 21; }
    var single: i32[] = [7].cumsum();
    if (single[0] != 7) { return 22; }

    if ("hello world".ellipsis(8) != "hello...") { return 30; }
    if ("short".ellipsis(10) != "short") { return 31; }
    if ("exactlyN!".ellipsis(9) != "exactlyN!") { return 32; }
    if ("hello world".ellipsis(3) != "...") { return 33; }
    if ("hi".ellipsis(2) != "hi") { return 34; }
    if ("hi".ellipsis(1) != ".") { return 35; }
    if ("".ellipsis(5) != "") { return 36; }

    if ((0 - 5).min_zero() != 0) { return 40; }
    if ((0).min_zero() != 0) { return 41; }
    if ((7).min_zero() != 7) { return 42; }

    if ((5).sign_str() != "+") { return 50; }
    if ((0 - 3).sign_str() != "-") { return 51; }
    if ((0).sign_str() != "") { return 52; }

    match ("hello\nworld".first_line()) {
        Some(s) => { if (s != "hello") { return 60; } },
        None => { return 61; },
    }
    match ("single".first_line()) {
        Some(s) => { if (s != "single") { return 62; } },
        None => { return 63; },
    }
    match ("".first_line()) { Some(_) => { return 64; }, None => { } }
    match ("\n".first_line()) {
        Some(s) => { if (s != "") { return 65; } },
        None => { return 66; },
    }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 28)", code)
	}
}

// Twenty-seventh stdlib bundle: i32[] median / mode /
// sum_squared, string[] join_with_last, string
// replace_byte / to_acronym / title_case, i32
// is_multiple_of. 8 helpers.
//
// `median` returns the lower middle on even-length arrays
// (no float averaging — return type is i32). `mode` ties
// resolve to the first occurrence in original order.
// `join_with_last` is the Oxford-comma shape: ", " between
// non-final pairs, " and " between the last two. `title_case`
// only upper-folds the first byte after each space; "FOX"
// stays "FOX".
func TestArm64StdlibBundle27(t *testing.T) {
	src := `
import "std/array";
function main(): i32 {
    // median
    match ([5, 1, 3, 4, 2].median()) {
        Some(m) => { if (m != 3) { return 1; } },
        None => { return 2; },
    }
    match ([1, 2, 3, 4].median()) {
        Some(m) => { if (m != 2) { return 3; } },
        None => { return 4; },
    }
    match ([7].median()) {
        Some(m) => { if (m != 7) { return 5; } },
        None => { return 6; },
    }
    var emp: i32[] = [];
    match (emp.median()) { Some(_) => { return 7; }, None => { } }

    // mode
    match ([1, 2, 2, 3, 3, 3, 4].mode()) {
        Some(m) => { if (m != 3) { return 10; } },
        None => { return 11; },
    }
    match ([5].mode()) {
        Some(m) => { if (m != 5) { return 12; } },
        None => { return 13; },
    }
    match ([1, 2, 1, 2].mode()) {
        Some(m) => { if (m != 1) { return 14; } },
        None => { return 15; },
    }
    match (emp.mode()) { Some(_) => { return 16; }, None => { } }

    // is_multiple_of
    if (!(12).is_multiple_of(3)) { return 20; }
    if ((10).is_multiple_of(3)) { return 21; }
    if (!(0).is_multiple_of(5)) { return 22; }
    if ((5).is_multiple_of(0)) { return 23; }
    if (!((0 - 6)).is_multiple_of(3)) { return 24; }

    // replace_byte
    if ("a-b-c".replace_byte(45, 95) != "a_b_c") { return 30; }
    if ("xxx".replace_byte(120, 121) != "yyy") { return 31; }
    if ("".replace_byte(65, 66) != "") { return 32; }
    if ("abc".replace_byte(122, 65) != "abc") { return 33; }

    // join_with_last
    if (["a", "b", "c", "d"].join_with_last(", ", " and ") != "a, b, c and d") { return 40; }
    if (["a", "b"].join_with_last(", ", " and ") != "a and b") { return 41; }
    if (["only"].join_with_last(", ", " and ") != "only") { return 42; }
    var empty: string[] = [];
    if (empty.join_with_last(", ", " and ") != "") { return 43; }

    // to_acronym
    if ("hello world".to_acronym() != "HW") { return 50; }
    if ("the quick brown fox".to_acronym() != "TQBF") { return 51; }
    if ("solo".to_acronym() != "S") { return 52; }
    if ("".to_acronym() != "") { return 53; }
    if ("  spaced  out  ".to_acronym() != "SO") { return 54; }

    // title_case
    if ("hello world".title_case() != "Hello World") { return 60; }
    if ("the quick brown FOX".title_case() != "The Quick Brown FOX") { return 61; }
    if ("".title_case() != "") { return 62; }
    if ("x".title_case() != "X") { return 63; }

    // sum_squared
    if ([1, 2, 3].sum_squared() != 14) { return 70; }
    if ([0].sum_squared() != 0) { return 71; }
    if (emp.sum_squared() != 0) { return 72; }
    if ([0 - 2, 3].sum_squared() != 13) { return 73; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 27)", code)
	}
}

// Twenty-sixth stdlib bundle: string starts_with_any /
// ends_with_any / lines_non_empty, i32[] range / count,
// string[] count_str, HTTP redirect / no_content builders.
// 8 helpers.
//
// `count` and `count_str` exist as separate names because
// the Array.X dispatch in the checker is keyed by method
// name only — it can't fan out on receiver element type
// the way overload resolution would. Once we have generic
// `Array.count[T]` they collapse to one helper.
func TestArm64StdlibBundle26(t *testing.T) {
	src := `
import "std/http";
import "std/math";
function main(): i32 {
    // starts_with_any
    if (!"hello world".starts_with_any(["foo", "hello", "bar"])) { return 1; }
    if ("hello".starts_with_any(["x", "y", "z"])) { return 2; }
    var nopre: string[] = [];
    if ("anything".starts_with_any(nopre)) { return 3; }
    if (!"abc".starts_with_any([""])) { return 4; }

    // ends_with_any
    if (!"file.txt".ends_with_any([".png", ".txt", ".jpg"])) { return 10; }
    if ("file.bin".ends_with_any([".png", ".txt"])) { return 11; }
    var nosuf: string[] = [];
    if ("anything".ends_with_any(nosuf)) { return 12; }

    // math.range(i32[])
    var xs: i32[] = [3, 1, 4, 1, 5, 9, 2, 6];
    match (xs.range()) {
        Some(r) => { if (r != 8) { return 20; } },
        None => { return 21; },
    }
    var empty32: i32[] = [];
    match (empty32.range()) { Some(_) => { return 22; }, None => { } }
    match ([7].range()) {
        Some(r) => { if (r != 0) { return 23; } },
        None => { return 24; },
    }

    // count (i32[])
    if ([1, 2, 3, 2, 4, 2].count(2) != 3) { return 30; }
    if ([1, 2, 3].count(5) != 0) { return 31; }
    var empty_i: i32[] = [];
    if (empty_i.count(1) != 0) { return 32; }

    // count_str (string[])
    if (["a", "b", "a", "c", "a"].count_str("a") != 3) { return 40; }
    if (["a", "b"].count_str("z") != 0) { return 41; }
    var empty_s: string[] = [];
    if (empty_s.count_str("x") != 0) { return 42; }

    // lines_non_empty
    var src1: string = "a\n\nb\nc\n";
    if ((src1.lines_non_empty()).len() != 3) { return 50; }
    if (src1.lines_non_empty()[0] != "a") { return 51; }
    if (src1.lines_non_empty()[1] != "b") { return 52; }
    if (src1.lines_non_empty()[2] != "c") { return 53; }
    if (("".lines_non_empty()).len() != 0) { return 54; }

    // http builders
    var r1: HttpResponse = http.http_response_redirect("/login");
    if (r1.status != 302 || r1.body != "/login") { return 60; }
    var r2: HttpResponse = http.http_response_no_content();
    if (r2.status != 204 || r2.body != "") { return 61; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 26)", code)
	}
}

// Twenty-fifth stdlib bundle: string is_url_like / is_json_like
// / common_prefix / common_suffix, string[] min_by_len, i32
// percent_of. 6 helpers (no array-method dispatch rewrite for
// the string ones — they sit as plain receiver-typed prelude
// fns, like is_ipv4 / is_email_like).
func TestArm64StdlibBundle25(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    // is_url_like
    if (!"http://example.com".is_url_like()) { return 1; }
    if (!"https://x.y/z".is_url_like()) { return 2; }
    if ("example.com".is_url_like()) { return 3; }
    if ("http://nodot".is_url_like()) { return 4; }
    if ("".is_url_like()) { return 5; }

    // is_json_like
    if (!"{}".is_json_like()) { return 10; }
    if (!"  {\"a\":1}  ".is_json_like()) { return 11; }
    if (!"[1,2,3]".is_json_like()) { return 12; }
    if ("plain".is_json_like()) { return 13; }
    if ("{unterminated".is_json_like()) { return 14; }
    if ("".is_json_like()) { return 15; }

    // common_prefix
    if ("abcdef".common_prefix("abcxyz") != "abc") { return 20; }
    if ("hello".common_prefix("world") != "") { return 21; }
    if ("same".common_prefix("same") != "same") { return 22; }
    if ("".common_prefix("anything") != "") { return 23; }
    if ("abc".common_prefix("abcd") != "abc") { return 24; }

    // common_suffix
    if ("hello world".common_suffix("brave world") != " world") { return 30; }
    if ("test".common_suffix("test") != "test") { return 31; }
    if ("hello".common_suffix("world") != "") { return 32; }
    if ("".common_suffix("x") != "") { return 33; }
    if ("a".common_suffix("ba") != "a") { return 34; }

    // min_by_len
    match (["aaa", "b", "cc"].min_by_len()) {
        Some(s) => { if (s != "b") { return 40; } },
        None => { return 41; },
    }
    var empty: string[] = [];
    match (empty.min_by_len()) { Some(_) => { return 42; }, None => { } }

    // percent_of
    if ((50).percent_of(200) != 25) { return 50; }
    if ((0).percent_of(100) != 0) { return 51; }
    if ((100).percent_of(100) != 100) { return 52; }
    if ((5).percent_of(0) != 0) { return 53; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 25)", code)
	}
}

// Twenty-fourth stdlib bundle: string is_ipv4 / is_email_like,
// i32 sum_of_digits / has_digit, string[] any_contains, HTTP
// response builders (bad_request / internal_error). 7 helpers.
//
// is_ipv4's octet parse is a manual digit-walk rather than
// parse_int — parse_int routes through i64 internally and hits
// the native i64-comparison-across-i32-boundary bug for small
// positive inputs (returns None on arm64 / x86-64 for "127").
//
// is_email_like binds the (string, string) tuple fields from
// split_once to local `var`s before passing to `len`. Calling
// `len(p.0)` directly on a tuple-field access crashes the arm64
// backend (the string-header load folds incorrectly). The
// workaround round-trips through a regular string local.
func TestArm64StdlibBundle24(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    // is_ipv4
    if (!"127.0.0.1".is_ipv4()) { return 1; }
    if (!"0.0.0.0".is_ipv4()) { return 2; }
    if (!"255.255.255.255".is_ipv4()) { return 3; }
    if ("256.0.0.0".is_ipv4()) { return 4; }
    if ("1.2.3".is_ipv4()) { return 5; }
    if ("1.2.3.4.5".is_ipv4()) { return 6; }
    if ("abc.def.ghi.jkl".is_ipv4()) { return 7; }
    if ("".is_ipv4()) { return 8; }

    // is_email_like
    if (!"a@b.c".is_email_like()) { return 10; }
    if (!"alice@example.com".is_email_like()) { return 11; }
    if ("no-at-sign".is_email_like()) { return 12; }
    if ("@nodomain".is_email_like()) { return 13; }
    if ("nolocal@".is_email_like()) { return 14; }
    if ("a@b".is_email_like()) { return 15; }   // no dot in domain
    if ("".is_email_like()) { return 16; }

    // sum_of_digits
    if ((0).sum_of_digits() != 0) { return 20; }
    if ((9).sum_of_digits() != 9) { return 21; }
    if ((123).sum_of_digits() != 6) { return 22; }
    if ((9999).sum_of_digits() != 36) { return 23; }
    if ((0 - 123).sum_of_digits() != 6) { return 24; }

    // has_digit
    if (!(123).has_digit(2)) { return 30; }
    if ((123).has_digit(4)) { return 31; }
    if (!(0).has_digit(0)) { return 32; }
    if ((5).has_digit(0)) { return 33; }
    if ((5).has_digit(10)) { return 34; }   // out of range

    // any_contains
    if (!["apple", "banana", "cherry"].any_contains("an")) { return 40; }
    if (["apple", "banana"].any_contains("xyz")) { return 41; }
    var empty: string[] = [];
    if (empty.any_contains("x")) { return 42; }

    // HTTP response builders
    var r1: HttpResponse = http.http_response_bad_request("missing field");
    if (r1.status != 400 || r1.body != "missing field") { return 50; }
    var r2: HttpResponse = http.http_response_internal_error("server boom");
    if (r2.status != 500 || r2.body != "server boom") { return 51; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 24)", code)
	}
}

// Twenty-third stdlib bundle: string remove_all / before /
// after / between, i32 is_between, byte is_letter, string[]
// all_non_empty. 7 helpers.
func TestArm64StdlibBundle23(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    // remove_all
    if ("aXbXc".remove_all("X") != "abc") { return 1; }
    if ("XXX".remove_all("X") != "") { return 2; }
    if ("abc".remove_all("X") != "abc") { return 3; }

    // before / after — first separator
    if ("key=value".before("=") != "key") { return 4; }
    if ("key=value".after("=") != "value") { return 5; }
    if ("plain".before("=") != "plain") { return 6; }
    if ("plain".after("=") != "") { return 7; }
    if ("=tail".before("=") != "") { return 8; }
    if ("head=".after("=") != "") { return 9; }

    // between — markers
    match ("(hello)".between("(", ")")) {
        Some(s) => { if (s != "hello") { return 10; } },
        None => { return 11; },
    }
    match ("<div>body</div>".between("<div>", "</div>")) {
        Some(s) => { if (s != "body") { return 12; } },
        None => { return 13; },
    }
    match ("no markers".between("[", "]")) { Some(_) => { return 14; }, None => { } }
    match ("[unclosed".between("[", "]")) { Some(_) => { return 15; }, None => { } }

    // is_between (inclusive)
    if (!(5).is_between(0, 10)) { return 16; }
    if (!(10).is_between(0, 10)) { return 17; }    // inclusive upper
    if ((11).is_between(0, 10)) { return 18; }
    if ((0 - 1).is_between(0, 10)) { return 19; }

    // is_letter
    if (!(65 as i32).is_ascii_letter()) { return 20; }
    if (!(97 as i32).is_ascii_letter()) { return 21; }
    if ((48 as i32).is_ascii_letter()) { return 22; }

    // all_non_empty
    if (!["a", "b", "c"].all_non_empty()) { return 23; }
    var empty: string[] = [];
    if (!empty.all_non_empty()) { return 24; }
    if (["a", "", "c"].all_non_empty()) { return 25; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 23)", code)
	}
}

// Twenty-second stdlib bundle: parse_hex_int / parse_bin_int,
// is_in_range, matches_any, reverse_digits, is_palindrome,
// to_array. 7 helpers.
func TestArm64StdlibBundle22(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    // parse_hex_int / parse_bin_int
    match ("ff".parse_hex_int()) { Some(v) => { if (v != 255) { return 1; } }, None => { return 2; }, }
    match ("1010".parse_bin_int()) { Some(v) => { if (v != 10) { return 3; } }, None => { return 4; }, }
    match ("xyz".parse_hex_int()) { Some(_) => { return 5; }, None => { } }
    match ("2".parse_bin_int()) { Some(_) => { return 6; }, None => { } }

    // is_in_range — half-open
    if (!(5).is_in_range(0, 10)) { return 7; }
    if (!(0).is_in_range(0, 10)) { return 8; }
    if ((10).is_in_range(0, 10)) { return 9; }    // exclusive upper
    if ((5).is_in_range(10, 0)) { return 10; }    // inverted

    // matches_any
    if (!(97 as i32).matches_any("abc")) { return 11; }
    if ((100 as i32).matches_any("abc")) { return 12; }
    if ((97 as i32).matches_any("")) { return 13; }

    // reverse_digits
    if ((1234).reverse_digits() != 4321) { return 14; }
    if ((1000).reverse_digits() != 1) { return 15; }
    if ((0).reverse_digits() != 0) { return 16; }
    if ((0 - 1234).reverse_digits() != (0 - 4321)) { return 17; }

    // is_palindrome
    if (!(0).is_palindrome()) { return 18; }
    if (!(121).is_palindrome()) { return 19; }
    if (!(12321).is_palindrome()) { return 20; }
    if ((1234).is_palindrome()) { return 21; }
    if ((0 - 121).is_palindrome()) { return 22; }

    // to_array
    var a: string[] = "abc".to_array();
    if (a.len() != 3 || a[0] != "a" || a[2] != "c") { return 23; }
    if (("".to_array()).len() != 0) { return 24; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 22)", code)
	}
}

// Twenty-first stdlib bundle: string is_int / is_float /
// wrap, string[] take / drop, pack_rgb, byte is_printable /
// is_control. 8 helpers.
func TestArm64StdlibBundle21(t *testing.T) {
	src := `
import "std/math";
import "std/string";
function main(): i32 {
    // is_int / is_float
    if (!"42".is_int()) { return 1; }
    if (!"-42".is_int()) { return 2; }
    if ("42.5".is_int()) { return 3; }
    if ("".is_int()) { return 4; }
    if ("+".is_int()) { return 5; }

    if (!"42".is_float()) { return 6; }
    if (!"42.5".is_float()) { return 7; }
    if (!".5".is_float()) { return 8; }
    if ("abc".is_float()) { return 9; }

    // wrap
    if ("x".wrap("[", "]") != "[x]") { return 10; }
    if ("".wrap("a", "b") != "ab") { return 11; }

    // string[] take / drop
    var arr: string[] = ["a", "b", "c", "d", "e"];
    var t: string[] = arr.take(3);
    if (t.len() != 3 || t[0] != "a" || t[2] != "c") { return 12; }
    if ((arr.take(100)).len() != 5) { return 13; }
    if ((arr.take(0)).len() != 0) { return 14; }
    var d: string[] = arr.drop(2);
    if (d.len() != 3 || d[0] != "c") { return 15; }
    if ((arr.drop(100)).len() != 0) { return 16; }

    // math.pack_rgb(+ round-trip via to_rgb_hex)
    if (math.pack_rgb(255, 0, 0) != 16711680) { return 17; }
    if (math.pack_rgb(0, 255, 0) != 65280) { return 18; }
    if (math.pack_rgb(255, 0, 0).to_rgb_hex() != "#ff0000") { return 19; }
    if (math.pack_rgb(0, 128, 64).to_rgb_hex() != "#008040") { return 20; }

    // is_printable / is_control
    if (!(32 as i32).is_ascii_printable()) { return 21; }
    if (!(126 as i32).is_ascii_printable()) { return 22; }
    if ((31 as i32).is_ascii_printable()) { return 23; }
    if ((127 as i32).is_ascii_printable()) { return 24; }
    if (!(0 as i32).is_ascii_control()) { return 25; }
    if (!(127 as i32).is_ascii_control()) { return 26; }
    if ((32 as i32).is_ascii_control()) { return 27; }
    if ((128 as i32).is_ascii_control()) { return 28; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 21)", code)
	}
}

// Twentieth stdlib bundle: i32 divmod (tuple), string
// escape_shell / snake_case / kebab_case / is_valid_identifier,
// is_valid_http_status. 6 helpers.
func TestArm64StdlibBundle20(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    // divmod — (quotient, remainder)
    var p1: (i32, i32) = (10).divmod(3);
    if (p1.0 != 3 || p1.1 != 1) { return 1; }
    var p2: (i32, i32) = (12).divmod(4);
    if (p2.0 != 3 || p2.1 != 0) { return 2; }
    var p3: (i32, i32) = (5).divmod(0);
    if (p3.0 != 0 || p3.1 != 0) { return 3; }

    // escape_shell
    if ("hello".escape_shell() != "'hello'") { return 4; }
    if ("don't".escape_shell() != "'don'\\''t'") { return 5; }
    if ("".escape_shell() != "''") { return 6; }

    // snake_case
    if ("camelCase".snake_case() != "camel_case") { return 7; }
    if ("HelloWorld".snake_case() != "hello_world") { return 8; }
    if ("ABC".snake_case() != "a_b_c") { return 9; }
    if ("hello world".snake_case() != "hello_world") { return 10; }

    // kebab_case
    if ("camelCase".kebab_case() != "camel-case") { return 11; }
    if ("hello world".kebab_case() != "hello-world") { return 12; }

    // is_valid_identifier
    if (!"hello".is_valid_identifier()) { return 13; }
    if (!"_private".is_valid_identifier()) { return 14; }
    if ("42x".is_valid_identifier()) { return 15; }
    if ("has space".is_valid_identifier()) { return 16; }
    if ("".is_valid_identifier()) { return 17; }

    // is_valid_http_status
    if (!http.is_valid_http_status(200)) { return 18; }
    if (!http.is_valid_http_status(100)) { return 19; }
    if (!http.is_valid_http_status(599)) { return 20; }
    if (http.is_valid_http_status(99)) { return 21; }
    if (http.is_valid_http_status(600)) { return 22; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 20)", code)
	}
}

// Nineteenth stdlib bundle: byte digit_value / hex_value,
// string count_byte, http_url_path_only,
// http_user_agent_is_bot, i32 to_string_with_sep. 6 helpers.
func TestArm64StdlibBundle19(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    // digit_value
    if ((48 as i32).digit_value() != 0) { return 1; }
    if ((57 as i32).digit_value() != 9) { return 2; }
    if ((65 as i32).digit_value() != (0 - 1)) { return 3; }

    // hex_value
    if ((48 as i32).hex_value() != 0) { return 4; }
    if ((97 as i32).hex_value() != 10) { return 5; }
    if ((70 as i32).hex_value() != 15) { return 6; }
    if ((71 as i32).hex_value() != (0 - 1)) { return 7; }

    // count_byte
    if ("hello".count_byte(108) != 2) { return 8; }
    if ("hello".count_byte(122) != 0) { return 9; }
    if ("aaaaa".count_byte(97) != 5) { return 10; }

    // http_url_path_only
    if (http.http_url_path_only("/api/users") != "/api/users") { return 11; }
    if (http.http_url_path_only("/api/users?id=42") != "/api/users") { return 12; }
    if (http.http_url_path_only("?foo=bar") != "") { return 13; }

    // http_user_agent_is_bot
    if (!http.http_user_agent_is_bot("Googlebot/2.1")) { return 14; }
    if (!http.http_user_agent_is_bot("Mozilla/5.0 (compatible; bingbot/2.0)")) { return 15; }
    if (http.http_user_agent_is_bot("Mozilla/5.0 (X11; Linux x86_64) Chrome/91")) { return 16; }
    if (!http.http_user_agent_is_bot("yahoo slurp")) { return 17; }

    // to_string_with_sep
    if ((1000).to_string_with_sep(",") != "1,000") { return 18; }
    if ((1234567).to_string_with_sep(",") != "1,234,567") { return 19; }
    if ((0 - 1234567).to_string_with_sep(",") != "-1,234,567") { return 20; }
    if ((999).to_string_with_sep(",") != "999") { return 21; }
    if ((1000).to_string_with_sep("_") != "1_000") { return 22; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 19)", code)
	}
}

// Eighteenth stdlib bundle: i32 ceil_div / round_up_to /
// round_down_to, string remove_prefix / remove_suffix /
// is_uuid, format_duration_ms. 7 helpers.
func TestArm64StdlibBundle18(t *testing.T) {
	src := `
import "std/format";
function main(): i32 {
    // ceil_div
    if ((10).ceil_div(3) != 4) { return 1; }
    if ((9).ceil_div(3) != 3) { return 2; }
    if ((0).ceil_div(3) != 0) { return 3; }
    if ((10).ceil_div(0) != 0) { return 4; }

    // round_up_to / round_down_to
    if ((13).round_up_to(4) != 16) { return 5; }
    if ((16).round_up_to(4) != 16) { return 6; }
    if ((10).round_up_to(0) != 10) { return 7; }
    if ((13).round_down_to(4) != 12) { return 8; }
    if ((3).round_down_to(4) != 0) { return 9; }

    // is_uuid
    if (!"550e8400-e29b-41d4-a716-446655440000".is_uuid()) { return 10; }
    if ("550e8400-e29b-41d4-a716-44665544000".is_uuid()) { return 11; }
    if ("550e8400+e29b-41d4-a716-446655440000".is_uuid()) { return 12; }
    if ("550e8400-e29b-41d4-a716-44665544000g".is_uuid()) { return 13; }

    // remove_prefix / remove_suffix — unchanged on no-match
    if ("https://example.com".remove_prefix("https://") != "example.com") { return 14; }
    if ("hello".remove_prefix("xyz") != "hello") { return 15; }
    if ("file.txt".remove_suffix(".txt") != "file") { return 16; }
    if ("file.txt".remove_suffix(".log") != "file.txt") { return 17; }

    // format_duration_ms
    if (format.format_duration_ms(0) != "0ms") { return 18; }
    if (format.format_duration_ms(500) != "500ms") { return 19; }
    if (format.format_duration_ms(1500) != "1s 500ms") { return 20; }
    if (format.format_duration_ms(90000) != "1m 30s") { return 21; }
    if (format.format_duration_ms(3661000) != "1h 1m 1s") { return 22; }
    if (format.format_duration_ms(0 - 1500) != "-1s 500ms") { return 23; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 18)", code)
	}
}

// Seventeenth stdlib bundle: array max_by_len / sum_lens,
// i32 log2_floor / sqrt_floor / to_rgb_hex, byte is_vowel,
// string rstrip_newline. 7 helpers.
func TestArm64StdlibBundle17(t *testing.T) {
	src := `
import "std/array";
function main(): i32 {
    // max_by_len / sum_lens
    match (["a", "abc", "xy"].max_by_len()) {
        Some(s) => { if (s != "abc") { return 1; } },
        None => { return 2; },
    }
    var empty: string[] = [];
    match (empty.max_by_len()) { Some(_) => { return 3; }, None => { } }
    if (["a", "abc", "xy"].sum_lens() != 6) { return 4; }
    if (empty.sum_lens() != 0) { return 5; }

    // log2_floor
    if ((1).log2_floor() != 0) { return 6; }
    if ((4).log2_floor() != 2) { return 7; }
    if ((1024).log2_floor() != 10) { return 8; }
    if ((0).log2_floor() != (0 - 1)) { return 9; }

    // sqrt_floor — Newton
    if ((0).sqrt_floor() != 0) { return 10; }
    if ((1).sqrt_floor() != 1) { return 11; }
    if ((9).sqrt_floor() != 3) { return 12; }
    if ((10).sqrt_floor() != 3) { return 13; }
    if ((100).sqrt_floor() != 10) { return 14; }
    if ((1000000).sqrt_floor() != 1000) { return 15; }

    // to_rgb_hex — low 24 bits → "#RRGGBB"
    if ((0).to_rgb_hex() != "#000000") { return 16; }
    if ((255).to_rgb_hex() != "#0000ff") { return 17; }
    if ((65280).to_rgb_hex() != "#00ff00") { return 18; }
    if ((16711680).to_rgb_hex() != "#ff0000") { return 19; }

    // is_vowel — ASCII a/e/i/o/u, no y
    if (!(97 as i32).is_ascii_vowel()) { return 20; }
    if (!(65 as i32).is_ascii_vowel()) { return 21; }
    if ((98 as i32).is_ascii_vowel()) { return 22; }
    if ((121 as i32).is_ascii_vowel()) { return 23; }

    // rstrip_newline — single trailing newline
    if ("hello\n".rstrip_newline() != "hello") { return 24; }
    if ("hello\r\n".rstrip_newline() != "hello") { return 25; }
    if ("hello".rstrip_newline() != "hello") { return 26; }
    if ("hello\n\n".rstrip_newline() != "hello\n") { return 27; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 17)", code)
	}
}

// Sixteenth stdlib bundle: string center / reverse_words,
// i32 rotate_left / rotate_right, csv_parse_line,
// http_header_value. 6 helpers.
func TestArm64StdlibBundle16(t *testing.T) {
	src := `
import "std/csv";
import "std/http";
function main(): i32 {
    // center
    if ("hi".center(6, "-") != "--hi--") { return 1; }
    if ("hi".center(7, "-") != "--hi---") { return 2; }   // odd → right
    if ("hi".center(2, "-") != "hi") { return 3; }
    if ("hello".center(3, "-") != "hello") { return 4; }

    // reverse_words
    if ("one two three".reverse_words() != "three two one") { return 5; }
    if ("solo".reverse_words() != "solo") { return 6; }
    if ("".reverse_words() != "") { return 7; }
    if ("a  b\tc".reverse_words() != "c b a") { return 8; }

    // rotate_left / rotate_right
    if ((305419896).rotate_left(8) != 878082066) { return 9; }
    if ((305419896).rotate_left(0) != 305419896) { return 10; }
    if ((305419896).rotate_left(32) != 305419896) { return 11; }   // mod 32
    if ((305419896).rotate_right(8).rotate_left(8) != 305419896) { return 12; }
    if ((123).rotate_left(7).rotate_right(7) != 123) { return 13; }

    // csv_parse_line
    var f: string[] = csv.csv_parse_line("a,b,c");
    if (f.len() != 3 || f[0] != "a" || f[2] != "c") { return 14; }
    var fq: string[] = csv.csv_parse_line("\"a,b\",c");
    if (fq.len() != 2 || fq[0] != "a,b") { return 15; }
    var fe: string[] = csv.csv_parse_line("\"a\"\"b\",c");
    if (fe.len() != 2 || fe[0] != "a\"b") { return 16; }
    if ((csv.csv_parse_line("")).len() != 1) { return 17; }
    var fmt: string[] = csv.csv_parse_line("a,,b");
    if (fmt.len() != 3 || fmt[1] != "") { return 18; }

    // http_header_value
    var hdrs: string = "Content-Type: text/html\r\nContent-Length: 42\r\nX-Foo: bar";
    match (http.http_header_value(hdrs, "content-type")) {
        Some(v) => { if (v != "text/html") { return 19; } },
        None => { return 20; },
    }
    match (http.http_header_value(hdrs, "X-FOO")) {
        Some(v) => { if (v != "bar") { return 21; } },
        None => { return 22; },
    }
    match (http.http_header_value(hdrs, "x-missing")) { Some(_) => { return 23; }, None => { } }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 16)", code)
	}
}

// Fifteenth stdlib bundle: array distinct / distinct_count,
// i32 is_power_of_2 / next_power_of_2, byte
// to_ascii_string, string hash_djb2, http_path_segments.
// 7 helpers.
func TestArm64StdlibBundle15(t *testing.T) {
	src := `
import "std/http";
function main(): i32 {
    // distinct / distinct_count
    var d: string[] = ["a", "b", "a", "c", "b"].distinct();
    if (d.len() != 3) { return 1; }
    if (d[0] != "a" || d[2] != "c") { return 2; }
    if (["a", "b", "a", "c", "b"].distinct_count() != 3) { return 3; }
    var empty: string[] = [];
    if ((empty.distinct()).len() != 0) { return 4; }
    if ((["x", "x", "x"].distinct()).len() != 1) { return 5; }

    // is_power_of_2
    if (!(1).is_power_of_2()) { return 6; }
    if (!(1024).is_power_of_2()) { return 7; }
    if ((3).is_power_of_2()) { return 8; }
    if ((0).is_power_of_2()) { return 9; }

    // next_power_of_2
    if ((0).next_power_of_2() != 1) { return 10; }
    if ((1).next_power_of_2() != 1) { return 11; }
    if ((3).next_power_of_2() != 4) { return 12; }
    if ((1000).next_power_of_2() != 1024) { return 13; }

    // to_ascii_string
    if ((65 as i32).to_ascii_string() != "A") { return 14; }
    if ((48 as i32).to_ascii_string() != "0") { return 15; }

    // hash_djb2 — deterministic + distinguishing
    if ("a".hash_djb2() == "b".hash_djb2()) { return 17; }
    if ("hello".hash_djb2() != "hello".hash_djb2()) { return 18; }

    // http_path_segments
    var ps: string[] = http.http_path_segments("/api/users/42");
    if (ps.len() != 3 || ps[0] != "api" || ps[2] != "42") { return 19; }
    if ((http.http_path_segments("/")).len() != 0) { return 20; }
    if ((http.http_path_segments("")).len() != 0) { return 21; }
    if ((http.http_path_segments("/api?q=1")).len() != 1) { return 22; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 15)", code)
	}
}

// Fourteenth stdlib bundle: trim_start_chars / trim_end_chars,
// random_int, format_bytes, csv_escape / csv_join. 6 helpers.
func TestArm64StdlibBundle14(t *testing.T) {
	src := `
import "std/csv";
import "std/format";
import "std/math";
function main(): i32 {
    // trim_start_chars / trim_end_chars
    if ("==hello".trim_start_chars("=") != "hello") { return 1; }
    if ("hello==".trim_end_chars("=") != "hello") { return 2; }
    if ("(hello)".trim_start_chars("()") != "hello)") { return 3; }
    if ("hello".trim_start_chars("") != "hello") { return 4; }
    if ("===".trim_start_chars("=") != "") { return 5; }

    // random_int — range checks
    var r: i32 = math.random_int(0, 100);
    if (r < 0 || r >= 100) { return 6; }
    if (math.random_int(10, 11) != 10) { return 7; }
    if (math.random_int(5, 5) != 5) { return 8; }
    if (math.random_int(10, 1) != 10) { return 9; }

    // format_bytes — binary prefixes
    if (format.format_bytes(0) != "0 B") { return 10; }
    if (format.format_bytes(512) != "512 B") { return 11; }
    if (format.format_bytes(1024) != "1 KiB") { return 12; }
    if (format.format_bytes(1024 * 1024) != "1 MiB") { return 13; }
    if (format.format_bytes(0 - 512) != "-512 B") { return 14; }

    // csv_escape / csv_join — RFC 4180
    if (csv.csv_escape("simple") != "simple") { return 15; }
    if (csv.csv_escape("has,comma") != "\"has,comma\"") { return 16; }
    if (csv.csv_escape("has\"quote") != "\"has\"\"quote\"") { return 17; }
    if (csv.csv_join(["a", "b", "c"]) != "a,b,c") { return 18; }
    if (csv.csv_join(["has,comma", "plain"]) != "\"has,comma\",plain") { return 19; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 14)", code)
	}
}

// Thirteenth stdlib bundle: array filter_non_empty /
// count_non_empty, string word_count / escape_html /
// strip_quotes, i32 to_string_padded. 6 helpers.
func TestArm64StdlibBundle13(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    // filter_non_empty / count_non_empty
    var src: string[] = "a,,b,,,c".split(",");
    if (src.len() != 6) { return 1; }
    var clean: string[] = src.filter_non_empty();
    if (clean.len() != 3 || clean[0] != "a" || clean[2] != "c") { return 2; }
    if (src.count_non_empty() != 3) { return 3; }

    var empty: string[] = [];
    if ((empty.filter_non_empty()).len() != 0) { return 4; }
    if (empty.count_non_empty() != 0) { return 5; }

    // word_count
    if ("hello world".word_count() != 2) { return 6; }
    if ("  a  b  c  ".word_count() != 3) { return 7; }
    if ("".word_count() != 0) { return 8; }
    if ("foo\tbar\nbaz".word_count() != 3) { return 9; }

    // escape_html
    if ("hello".escape_html() != "hello") { return 10; }
    if ("<>".escape_html() != "&lt;&gt;") { return 11; }
    if ("a & b".escape_html() != "a &amp; b") { return 12; }
    if ("\"quoted\"".escape_html() != "&#34;quoted&#34;") { return 13; }
    if ("a'b".escape_html() != "a&#39;b") { return 14; }

    // strip_quotes
    match ("\"hello\"".strip_quotes()) { Some(s) => { if (s != "hello") { return 15; } }, None => { return 16; }, }
    match ("'world'".strip_quotes()) { Some(s) => { if (s != "world") { return 17; } }, None => { return 18; }, }
    match ("noquotes".strip_quotes()) { Some(_) => { return 19; }, None => { } }
    match ("\"".strip_quotes()) { Some(_) => { return 20; }, None => { } }
    match ("\"unmatched'".strip_quotes()) { Some(_) => { return 21; }, None => { } }

    // to_string_padded
    if ((42).to_string_padded(5) != "00042") { return 22; }
    if ((42).to_string_padded(2) != "42") { return 23; }
    if ((0).to_string_padded(3) != "000") { return 24; }
    if ((0 - 42).to_string_padded(5) != "-0042") { return 25; }
    if ((0 - 42).to_string_padded(2) != "-42") { return 26; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 13)", code)
	}
}

// Twelfth stdlib bundle: bit accessors (bit/set/clear/
// toggle), byte newline predicate, count_lines, HTTP
// response builders, log_info/warn/error. 12 helpers.
func TestArm64StdlibBundle12(t *testing.T) {
	src := `
import "std/http";
import "std/log";
function main(): i32 {
    // Bit accessors
    if ((5).bit(0) != true) { return 1; }
    if ((5).bit(1) != false) { return 2; }
    if ((5).bit(2) != true) { return 3; }
    if ((5).bit(100) != false) { return 4; }

    if ((0).set_bit(3) != 8) { return 5; }
    if ((5).set_bit(1) != 7) { return 6; }
    if ((5).set_bit(100) != 5) { return 7; }

    if ((7).clear_bit(1) != 5) { return 8; }
    if ((7).clear_bit(100) != 7) { return 9; }

    if ((5).toggle_bit(0) != 4) { return 10; }
    if ((5).toggle_bit(1) != 7) { return 11; }
    if ((5).toggle_bit(100) != 5) { return 12; }

    // is_newline
    if (!(10 as i32).is_ascii_newline()) { return 13; }
    if (!(13 as i32).is_ascii_newline()) { return 14; }
    if ((32 as i32).is_ascii_newline()) { return 15; }

    // count_lines
    if ("a\nb\nc".count_lines() != 3) { return 16; }
    if ("a\nb\nc\n".count_lines() != 3) { return 17; }
    if ("".count_lines() != 0) { return 18; }
    if ("solo".count_lines() != 1) { return 19; }
    if ("\n".count_lines() != 1) { return 20; }

    // HTTP response builders
    var r1: HttpResponse = http.http_response_ok("hello");
    if (r1.status != 200 || r1.body != "hello") { return 21; }
    var r2: HttpResponse = http.http_response_not_found();
    if (r2.status != 404 || r2.body != "Not Found") { return 22; }
    var r3: HttpResponse = http.http_response_text(500, "boom");
    if (r3.status != 500 || r3.body != "boom") { return 23; }

    // Log helpers — sanity-check they don't crash; output
    // goes to stderr so the e2e harness exit-code check still
    // sees the program's intended return value.
    log.log_info("test info");
    log.log_warn("test warn");
    log.log_error("test error");
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 12)", code)
	}
}

// Eleventh stdlib bundle: splitn / first / last / take /
// drop / chunks on strings, case-insensitive sort, i32
// to_binary / to_oct. 11 helpers.
func TestArm64StdlibBundle11(t *testing.T) {
	src := `
import "std/sort";
function main(): i32 {
    // splitn
    var s1: string[] = "a=b=c=d".splitn("=", 2);
    if (s1.len() != 2 || s1[0] != "a" || s1[1] != "b=c=d") { return 1; }
    var s3: string[] = "a=b=c=d".splitn("=", 1);
    if (s3.len() != 1 || s3[0] != "a=b=c=d") { return 2; }
    var s4: string[] = "a=b=c=d".splitn("=", 0);
    if (s4.len() != 0) { return 3; }
    var s5: string[] = "a=b".splitn("=", 100);
    if (s5.len() != 2) { return 4; }
    var s6: string[] = "no-sep".splitn("=", 5);
    if (s6.len() != 1 || s6[0] != "no-sep") { return 5; }

    // first / last
    match ("hello".first()) { Some(b) => { if (b != 104) { return 6; } }, None => { return 7; }, }
    match ("hello".last()) { Some(b) => { if (b != 111) { return 8; } }, None => { return 9; }, }
    match ("".first()) { Some(_) => { return 10; }, None => { } }

    // take / drop — bounds clamped
    if ("hello world".take(5) != "hello") { return 11; }
    if ("hello".take(100) != "hello") { return 12; }
    if ("hello".take(0) != "") { return 13; }
    if ("hello world".drop(6) != "world") { return 14; }
    if ("hello".drop(100) != "") { return 15; }
    if ("hello".drop(0 - 1) != "hello") { return 16; }

    // chunks
    var c1: string[] = "abcdef".chunks(2);
    if (c1.len() != 3 || c1[2] != "ef") { return 17; }
    var c2: string[] = "abcdef".chunks(4);
    if (c2.len() != 2 || c2[1] != "ef") { return 18; }   // short tail
    if (("".chunks(3)).len() != 0) { return 19; }
    var c4: string[] = "abc".chunks(0);
    if (c4.len() != 1 || c4[0] != "abc") { return 20; }

    // case-insensitive sort + cmp
    var asc: string[] = sort.sort_strings_asc_ci(["Banana", "apple", "Cherry"]);
    if (asc[0] != "apple" || asc[1] != "Banana" || asc[2] != "Cherry") { return 21; }
    if (sort.string_cmp_ci("APPLE", "apple") != 0) { return 22; }
    if (sort.string_cmp_ci("apple", "banana") != (0 - 1)) { return 23; }

    // to_binary / to_oct
    if ((5).to_binary() != "101") { return 24; }
    if ((255).to_binary() != "11111111") { return 25; }
    if ((0).to_binary() != "0") { return 26; }
    if ((8).to_oct() != "10") { return 27; }
    if ((511).to_oct() != "777") { return 28; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 11)", code)
	}
}

// Tenth stdlib bundle: numeric range constants (i32_max /
// i32_min / i64_max / i64_min), one-sided trim (trim_start /
// trim_end), trim_chars, case-insensitive prefix/suffix,
// string sort, and string_cmp. 11 helpers.
func TestArm64StdlibBundle10(t *testing.T) {
	src := `
import "std/math";
import "std/sort";
import "core/cmp";
function main(): i32 {
    // Range constants
    if (math.i32_max() != 2147483647) { return 1; }
    if (math.i32_min() != (0 - 2147483647 - 1)) { return 2; }
    if (math.i64_max() != 9223372036854775807) { return 3; }

    // trim_start / trim_end
    if ("  hello  ".trim_start() != "hello  ") { return 4; }
    if ("  hello  ".trim_end() != "  hello") { return 5; }
    if ("hello".trim_start() != "hello") { return 6; }
    if ("   ".trim_start() != "") { return 7; }
    if ("".trim_end() != "") { return 8; }

    // trim_chars
    if ("==hello==".trim_chars("=") != "hello") { return 9; }
    if ("(hello)".trim_chars("()") != "hello") { return 10; }
    if ("hello".trim_chars("") != "hello") { return 11; }
    if ("---".trim_chars("-") != "") { return 12; }

    // starts_with_ci / ends_with_ci
    if (!"Content-Type".starts_with_ci("content")) { return 13; }
    if (!"image/png".ends_with_ci("PNG")) { return 14; }
    if ("hello".starts_with_ci("WORLD")) { return 15; }
    if ("hello".ends_with_ci("WORLD")) { return 16; }

    // string_cmp + generic string sort (cmp.sort / cmp.sort_desc)
    if (sort.string_cmp("apple", "banana") != (0 - 1)) { return 17; }
    if (sort.string_cmp("banana", "apple") != 1) { return 18; }
    if (sort.string_cmp("same", "same") != 0) { return 19; }
    if (sort.string_cmp("short", "shorter") != (0 - 1)) { return 20; }   // shorter <

    var unsorted: string[] = ["banana", "apple", "cherry"];
    var asc: string[] = cmp.sort(unsorted);
    if (asc[0] != "apple" || asc[1] != "banana" || asc[2] != "cherry") { return 21; }
    if (unsorted[0] != "banana") { return 22; }   // original untouched
    var desc: string[] = cmp.sort_desc(unsorted);
    if (desc[0] != "cherry" || desc[2] != "apple") { return 23; }
    var empty: string[] = [];
    if ((cmp.sort(empty)).len() != 0) { return 24; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 10)", code)
	}
}

// Ninth stdlib bundle: case-insensitive ASCII search
// (contains_ci / index_of_ci), multi-byte pad
// (pad_start_str / pad_end_str), truncate with ellipsis,
// digit count, pluralize. 7 new methods.
func TestArm64StdlibBundle9(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    // Case-insensitive search
    if ("Hello World".index_of_ci("WORLD") != 6) { return 1; }
    if ("Hello World".index_of_ci("world") != 6) { return 2; }
    if ("Hello World".index_of_ci("xyz") != (0 - 1)) { return 3; }
    if (!"Hello".contains_ci("ELL")) { return 4; }
    if ("Hello".contains_ci("xyz")) { return 5; }

    // Multi-byte pad
    if ("hi".pad_start_str(8, "==") != "======hi") { return 6; }
    if ("hi".pad_end_str(8, "==") != "hi======") { return 7; }
    if ("hi".pad_start_str(5, "ab") != "abahi") { return 8; }
    if ("longer".pad_start_str(3, "=") != "longer") { return 9; }
    if ("hi".pad_start_str(8, "") != "hi") { return 10; }

    // truncate
    if ("hello world".truncate(8, "...") != "hello...") { return 11; }
    if ("hello".truncate(10, "...") != "hello") { return 12; }
    if ("hello world".truncate(3, "...") != "hel") { return 13; }   // hard cut

    // digits
    if ((0).digits() != 1) { return 14; }
    if ((9).digits() != 1) { return 15; }
    if ((10).digits() != 2) { return 16; }
    if ((1000).digits() != 4) { return 17; }
    if ((0 - 1000).digits() != 4) { return 18; }

    // pluralize
    if ((1).pluralize("widget", "widgets") != "widget") { return 19; }
    if ((2).pluralize("widget", "widgets") != "widgets") { return 20; }
    if ((0).pluralize("widget", "widgets") != "widgets") { return 21; }
    if ((0 - 1).pluralize("widget", "widgets") != "widget") { return 22; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 9)", code)
	}
}

// Eighth stdlib bundle: i32 saturating / checked arithmetic
// (overflow detection via sign-bit comparison — natives' i64
// codegen has a comparison bug across the i32::MAX threshold,
// so the prelude sticks to pure-i32 ops), string parse_bool,
// (i32).to_hex(), HTTP method classifiers. 9 helpers.
func TestArm64StdlibBundle8(t *testing.T) {
	src := `
import "std/i32";
function main(): i32 {
    // Saturating add / sub — clamp at MAX / MIN
    if ((100).saturating_add(50) != 150) { return 1; }
    if ((2147483647).saturating_add(1) != 2147483647) { return 2; }
    var min32: i32 = 0 - 2147483647 - 1;
    if (min32.saturating_sub(1) != min32) { return 3; }
    if ((100).saturating_sub(50) != 50) { return 4; }

    // Checked add / sub / div — None on overflow / DBZ / MIN-by--1
    match ((100).checked_add(50)) {
        Some(v) => { if (v != 150) { return 5; } },
        None => { return 6; },
    }
    match ((2147483647).checked_add(1)) { Some(_) => { return 7; }, None => { } }
    match ((10).checked_div(0)) { Some(_) => { return 8; }, None => { } }
    match (min32.checked_div(0 - 1)) { Some(_) => { return 9; }, None => { } }

    // to_hex
    if ((0).to_hex() != "0") { return 10; }
    if ((255).to_hex() != "ff") { return 11; }
    if ((4096).to_hex() != "1000") { return 12; }

    // parse_bool
    match ("true".parse_bool()) { Some(b) => { if (!b) { return 13; } }, None => { return 14; }, }
    match ("false".parse_bool()) { Some(b) => { if (b) { return 15; } }, None => { return 16; }, }
    match ("1".parse_bool()) { Some(b) => { if (!b) { return 17; } }, None => { return 18; }, }
    match ("yes".parse_bool()) { Some(_) => { return 19; }, None => { } }
    match ("TRUE".parse_bool()) { Some(_) => { return 20; }, None => { } }

    // HTTP method classifiers
    if (!"GET".is_http_safe_method()) { return 21; }
    if (!"HEAD".is_http_safe_method()) { return 22; }
    if ("POST".is_http_safe_method()) { return 23; }
    if (!"GET".is_http_idempotent_method()) { return 24; }
    if (!"PUT".is_http_idempotent_method()) { return 25; }
    if (!"DELETE".is_http_idempotent_method()) { return 26; }
    if ("POST".is_http_idempotent_method()) { return 27; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 8)", code)
	}
}

// Seventh stdlib bundle: i32[] product / avg, string leading/
// trailing_count, hash_fnv32, escape_c, repeat_char,
// http_status_text. 8 new helpers. Pure-prelude.
func TestArm64StdlibBundle7(t *testing.T) {
	src := `
import "std/array";
import "std/string";
import "std/http";
function main(): i32 {
    // i32[] product / avg
    if ([2, 3, 4].product() != 24) { return 1; }
    var empty: i32[] = [];
    if (empty.product() != 1) { return 2; }    // multiplicative identity
    match ([2, 4, 6].avg()) {
        Some(v) => { if (v != 4) { return 3; } },
        None => { return 4; },
    }
    match (empty.avg()) { Some(_) => { return 5; }, None => { } }

    // leading_count / trailing_count
    if ("    hello".leading_count(32) != 4) { return 6; }
    if ("hello".leading_count(32) != 0) { return 7; }
    if ("hello   ".trailing_count(32) != 3) { return 8; }
    if ("    ".trailing_count(32) != 4) { return 9; }

    // hash_fnv32 — deterministic, distinct inputs distinct hashes
    if ("".hash_fnv32() != (0 - 2128831035) as u32) { return 10; }
    if ("a".hash_fnv32() == "b".hash_fnv32()) { return 11; }
    if ("hello".hash_fnv32() != "hello".hash_fnv32()) { return 12; }

    // escape_c
    if ("plain".escape_c() != "plain") { return 13; }
    if ("\"".escape_c() != "\\\"") { return 14; }
    if ("\\".escape_c() != "\\\\") { return 15; }
    if ("\n".escape_c() != "\\n") { return 16; }
    if ("a\tb".escape_c() != "a\\tb") { return 17; }

    // repeated chars via pad_start (repeat_char dropped: std/string
    // free fns are uncallable under no-prelude — qualifier is a keyword)
    if ("".pad_start(4, "x") != "xxxx") { return 18; }
    if ("".pad_start(5, "-") != "-----") { return 19; }
    if ("".pad_start(0, "x") != "") { return 20; }

    // http_status_text
    if (http.http_status_text(200) != "OK") { return 21; }
    if (http.http_status_text(404) != "Not Found") { return 22; }
    if (http.http_status_text(500) != "Internal Server Error") { return 23; }
    if (http.http_status_text(418) != "I'm a teapot") { return 24; }
    if (http.http_status_text(999) != "") { return 25; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 7)", code)
	}
}

// Sixth stdlib bundle: i32 bit ops (count_ones, leading/
// trailing_zeros, byte_swap), i64 power/gcd/lcm parity with
// i32, range / range_step generators, repeat_with_sep. 11
// new methods. Pure-prelude.
func TestArm64StdlibBundle6(t *testing.T) {
	src := `
import "std/i32";
import "std/i64";
import "std/math";
function main(): i32 {
    // count_ones — population count
    if ((0).count_ones() != 0) { return 1; }
    if ((1).count_ones() != 1) { return 2; }
    if ((7).count_ones() != 3) { return 3; }
    if ((0 - 1).count_ones() != 32) { return 4; }   // all bits set

    // leading_zeros — top-bit walk
    if ((0).leading_zeros() != 32) { return 5; }
    if ((1).leading_zeros() != 31) { return 6; }
    if ((0 - 1).leading_zeros() != 0) { return 7; }

    // trailing_zeros — bottom-bit walk
    if ((0).trailing_zeros() != 32) { return 8; }
    if ((1).trailing_zeros() != 0) { return 9; }
    if ((8).trailing_zeros() != 3) { return 10; }

    // byte_swap — 0x01020304 → 0x04030201
    if ((16909060).byte_swap() != 67305985) { return 11; }
    if ((0).byte_swap() != 0) { return 12; }

    // i64 pow / gcd / lcm — parity with i32 versions
    if ((2 as i64).pow(40) != (1099511627776 as i64)) { return 13; }
    if ((48 as i64).gcd(18 as i64) != (6 as i64)) { return 14; }
    if ((4 as i64).lcm(6 as i64) != (12 as i64)) { return 15; }

    // range — half-open
    var r1: i32[] = math.range(0, 5);
    if (r1.len() != 5 || r1[0] != 0 || r1[4] != 4) { return 16; }
    if ((math.range(5, 5)).len() != 0) { return 17; }   // empty when start >= end

    // range_step — step <= 0 returns empty
    var rs: i32[] = math.range_step(0, 10, 2);
    if (rs.len() != 5 || rs[0] != 0 || rs[4] != 8) { return 18; }
    if ((math.range_step(0, 10, 0)).len() != 0) { return 19; }

    // repeat_with_sep
    if ("x".repeat_with_sep(3, ", ") != "x, x, x") { return 20; }
    if ("abc".repeat_with_sep(2, "-") != "abc-abc") { return 21; }
    if ("x".repeat_with_sep(1, ", ") != "x") { return 22; }
    if ("x".repeat_with_sep(0, ", ") != "") { return 23; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 6)", code)
	}
}

// Fifth stdlib bundle: numeric methods (is_even/is_odd for
// i32 + i64, pow, gcd, lcm), string search (last_index_of),
// string casing (capitalize). 11 new methods. Pure-prelude.
func TestArm64StdlibBundle5(t *testing.T) {
	src := `
import "std/i32";
import "std/i64";
function main(): i32 {
    // Parity i32 + i64
    if (!(4).is_even()) { return 1; }
    if ((4).is_odd()) { return 2; }
    if (!(7).is_odd()) { return 3; }
    if (!(0).is_even()) { return 4; }
    if (!(0 - 4).is_even()) { return 5; }
    if (!(0 - 7).is_odd()) { return 6; }
    if (!(4 as i64).is_even()) { return 7; }
    if (!(7 as i64).is_odd()) { return 8; }

    // pow (i32) — by squaring
    if ((2).pow(10) != 1024) { return 9; }
    if ((3).pow(4) != 81) { return 10; }
    if ((5).pow(0) != 1) { return 11; }
    if ((1).pow(100) != 1) { return 12; }
    if ((0).pow(5) != 0) { return 13; }
    if ((2).pow(0 - 1) != 0) { return 14; }

    // gcd — Euclidean, sign-agnostic
    if ((48).gcd(18) != 6) { return 15; }
    if ((18).gcd(48) != 6) { return 16; }
    if ((0).gcd(7) != 7) { return 17; }
    if ((7).gcd(0) != 7) { return 18; }
    if ((0).gcd(0) != 0) { return 19; }
    if ((0 - 48).gcd(18) != 6) { return 20; }

    // lcm
    if ((4).lcm(6) != 12) { return 21; }
    if ((3).lcm(5) != 15) { return 22; }
    if ((0).lcm(5) != 0) { return 23; }

    // last_index_of — rightmost match
    if ("hello hello".last_index_of("hello") != 6) { return 24; }
    if ("aaa".last_index_of("a") != 2) { return 25; }
    if ("hello".last_index_of("z") != (0 - 1)) { return 26; }
    if ("hello".last_index_of("") != 5) { return 27; }

    // capitalize — first byte uppercased, rest preserved
    if ("hello".capitalize() != "Hello") { return 28; }
    if ("Hello".capitalize() != "Hello") { return 29; }
    if ("HELLO".capitalize() != "HELLO") { return 30; }
    if ("".capitalize() != "") { return 31; }
    if ("1abc".capitalize() != "1abc") { return 32; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 5)", code)
	}
}

// Fourth stdlib bundle: punctuation classifier, byte→hex
// helper, i32 sign trio (signum / is_positive / is_negative /
// is_zero), string blanks/hex predicates, indent. 11 new
// methods. All pure-prelude, no IR / checker work — sidesteps
// the generic-prelude-function monomorph regression.
func TestArm64StdlibBundle4(t *testing.T) {
	src := `
import "std/i32";
function main(): i32 {
    // is_punct
    if (!(33 as i32).is_ascii_punct()) { return 1; }
    if (!(126 as i32).is_ascii_punct()) { return 2; }
    if (!(64 as i32).is_ascii_punct()) { return 3; }
    if ((48 as i32).is_ascii_punct()) { return 4; }
    if ((65 as i32).is_ascii_punct()) { return 5; }

    // hex_digit
    if ((0 as i32).hex_digit() != "0") { return 6; }
    if ((10 as i32).hex_digit() != "a") { return 7; }
    if ((15 as i32).hex_digit() != "f") { return 8; }
    if ((16 as i32).hex_digit() != "") { return 9; }

    // signum / sign predicates
    if ((5).signum() != 1) { return 11; }
    if ((0).signum() != 0) { return 12; }
    if ((0 - 5).signum() != (0 - 1)) { return 13; }
    if (!(5).is_positive()) { return 14; }
    if (!(0 - 5).is_negative()) { return 15; }
    if (!(0).is_zero()) { return 16; }

    // is_blank
    if (!"".is_blank()) { return 17; }
    if (!"  \t\n".is_blank()) { return 18; }
    if (" x ".is_blank()) { return 19; }

    // is_hex_string
    if (!"deadbeef".is_hex_string()) { return 20; }
    if (!"DEADBEEF".is_hex_string()) { return 21; }
    if ("hello".is_hex_string()) { return 22; }
    if ("".is_hex_string()) { return 23; }

    // indent
    if ("a\nb".indent(">> ") != ">> a\n>> b") { return 24; }
    if ("solo".indent("-> ") != "-> solo") { return 25; }
    if ("".indent(">> ") != "") { return 26; }
    if ("a\n".indent(">> ") != ">> a\n") { return 27; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 4)", code)
	}
}

// Third stdlib bundle: i64 / u32 / u64 scalar reductions
// (abs only on i64; min/max/clamp on all three), plus three
// string helpers (at / chars / reverse_bytes). 13 new methods.
func TestArm64StdlibBundle3(t *testing.T) {
	src := `
import "std/i64";
import "std/u32";
import "std/u64";
import "std/string";
function main(): i32 {
    // i64 abs / min / max / clamp.
    var i: i64 = 0 - 42 as i64;
    if (i.abs() != (42 as i64)) { return 1; }
    if ((5 as i64).min(7 as i64) != (5 as i64)) { return 2; }
    if ((5 as i64).max(7 as i64) != (7 as i64)) { return 3; }
    if ((100 as i64).clamp(0 as i64, 10 as i64) != (10 as i64)) { return 4; }

    // u32 min / max / clamp (no abs — always non-negative).
    if ((5 as u32).min(7 as u32) != (5 as u32)) { return 5; }
    if ((100 as u32).max(50 as u32) != (100 as u32)) { return 6; }
    if ((100 as u32).clamp(0 as u32, 10 as u32) != (10 as u32)) { return 7; }

    // u64 min / max / clamp.
    if ((5 as u64).min(7 as u64) != (5 as u64)) { return 8; }
    if ((5 as u64).max(7 as u64) != (7 as u64)) { return 9; }
    if ((100 as u64).clamp(0 as u64, 10 as u64) != (10 as u64)) { return 10; }

    // String at — bounds-checked Option[i32].
    match ("hello".at(0)) { Some(b) => { if (b != 104) { return 11; } }, None => { return 12; }, }
    match ("hello".at(4)) { Some(b) => { if (b != 111) { return 13; } }, None => { return 14; }, }
    match ("hello".at(5)) { Some(_) => { return 15; }, None => { } }
    match ("hello".at(0 - 1)) { Some(_) => { return 16; }, None => { } }
    match ("".at(0)) { Some(_) => { return 17; }, None => { } }

    // String chars — i32[] one element per byte.
    var cs: i32[] = "abc".chars();
    if (cs.len() != 3) { return 18; }
    if (cs[0] != 97 || cs[1] != 98 || cs[2] != 99) { return 19; }
    if (("".chars()).len() != 0) { return 20; }

    // String reverse_bytes — ASCII only.
    if ("hello".reverse_bytes() != "olleh") { return 21; }
    if ("a".reverse_bytes() != "a") { return 22; }
    if ("".reverse_bytes() != "") { return 23; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 3)", code)
	}
}

// Second stdlib bundle: byte case helpers (is_ascii_lower/is_ascii_upper/
// to_ascii_lower/to_ascii_upper), i32 numeric methods (abs/min/max/clamp),
// whole-string ASCII predicates (is_ascii_only / is_numeric /
// is_alpha_only / is_alnum_only). 12 new methods total.
func TestArm64StdlibBundle2(t *testing.T) {
	src := `
import "std/i32";
function main(): i32 {
    // Byte-level case classifiers + flippers.
    if (!(65 as i32).is_ascii_upper()) { return 1; }
    if ((65 as i32).is_ascii_lower()) { return 2; }
    if (!(97 as i32).is_ascii_lower()) { return 3; }
    if ((97 as i32).is_ascii_upper()) { return 4; }
    if ((65 as i32).to_ascii_lower() != 97) { return 5; }   // 'A' → 'a'
    if ((97 as i32).to_ascii_upper() != 65) { return 6; }   // 'a' → 'A'
    if ((48 as i32).to_ascii_lower() != 48) { return 7; }   // digits pass through
    if ((48 as i32).to_ascii_upper() != 48) { return 8; }

    // i32 numeric methods.
    if ((5).abs() != 5) { return 9; }
    if ((0 - 5).abs() != 5) { return 10; }
    if ((0).abs() != 0) { return 11; }
    if ((3).min(7) != 3) { return 12; }
    if ((7).min(3) != 3) { return 13; }
    if ((3).max(7) != 7) { return 14; }
    if ((7).max(3) != 7) { return 15; }
    if ((5).clamp(10, 20) != 10) { return 16; }
    if ((25).clamp(10, 20) != 20) { return 17; }
    if ((15).clamp(10, 20) != 15) { return 18; }

    // String predicates — whole-string variants.
    if (!"abc".is_ascii_only()) { return 19; }
    if (!"".is_ascii_only()) { return 20; }
    if (!"12345".is_numeric()) { return 21; }
    if ("12a".is_numeric()) { return 22; }
    if ("".is_numeric()) { return 23; }
    if (!"hello".is_alpha_only()) { return 24; }
    if ("hello1".is_alpha_only()) { return 25; }
    if ("".is_alpha_only()) { return 26; }
    if (!"hello123".is_alnum_only()) { return 27; }
    if ("hello world".is_alnum_only()) { return 28; }   // space breaks
    if ("".is_alnum_only()) { return 29; }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle 2)", code)
	}
}

func TestArm64StdlibBundle(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    // pad_start / pad_end
    if ("42".pad_start(5, "0") != "00042") { return 1; }
    if ("42".pad_end(5, " ") != "42   ") { return 2; }
    if ("longer".pad_start(3, "0") != "longer") { return 3; }
    if ("x".pad_start(3, "") != "x") { return 4; }       // empty ch → no-op
    if ("".pad_start(3, "ab") != "aaa") { return 5; }    // only first byte of ch

    // split_once — first match wins, empty sep / no-match → None
    match ("key=value".split_once("=")) {
        Some(p) => { if (p.0 != "key" || p.1 != "value") { return 6; } },
        None => { return 7; },
    }
    match ("key==v".split_once("=")) {
        Some(p) => { if (p.0 != "key" || p.1 != "=v") { return 8; } },
        None => { return 9; },
    }
    match ("nosep".split_once("=")) { Some(_) => { return 10; }, None => { } }
    match ("x".split_once("")) { Some(_) => { return 11; }, None => { } }

    // trim_start_matches / trim_end_matches
    if ("xxxhello".trim_start_matches("x") != "hello") { return 12; }
    if ("hello".trim_start_matches("x") != "hello") { return 13; }
    if ("hello".trim_start_matches("") != "hello") { return 14; }
    if ("hello///".trim_end_matches("/") != "hello") { return 15; }
    if ("ababxyz".trim_start_matches("ab") != "xyz") { return 16; }

    // count — non-overlapping
    if ("hello world".count("l") != 3) { return 17; }
    if ("aaaa".count("aa") != 2) { return 18; }
    if ("hello".count("z") != 0) { return 19; }
    if ("hello".count("") != 0) { return 20; }

    // replace_n — cap at first n
    if ("aaaa".replace_n("a", "b", 2) != "bbaa") { return 21; }
    if ("aaaa".replace_n("a", "b", 0) != "aaaa") { return 22; }
    if ("aaaa".replace_n("a", "b", 99) != "bbbb") { return 23; }

    // i32[] sum / max / min
    var xs: i32[] = [3, 1, 4, 1, 5, 9, 2, 6];
    if (xs.sum() != 31) { return 24; }
    match (xs.max()) { Some(v) => { if (v != 9) { return 25; } }, None => { return 26; }, }
    match (xs.min()) { Some(v) => { if (v != 1) { return 27; } }, None => { return 28; }, }

    // i32[] empty cases
    var empty: i32[] = [];
    if (empty.sum() != 0) { return 29; }
    match (empty.max()) { Some(_) => { return 30; }, None => { } }
    match (empty.min()) { Some(_) => { return 31; }, None => { } }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (stdlib bundle)", code)
	}
}

func TestArm64Paths(t *testing.T) {
	src := `
import "std/path";
function main(): i32 {
    // path_join — simple, leading/trailing slashes, empty parts.
    if (path.path_join(["a", "b", "c"]) != "a/b/c") { return 1; }
    if (path.path_join(["/usr", "local", "bin"]) != "/usr/local/bin") { return 2; }
    if (path.path_join(["a", "", "b"]) != "a/b") { return 3; }
    if (path.path_join(["a/", "b"]) != "a/b") { return 4; }
    if (path.path_join(["a/", "/b"]) != "a/b") { return 5; }
    if (path.path_join(["a", "/b"]) != "a/b") { return 6; }
    if (path.path_join(["/", "a"]) != "/a") { return 7; }
    if (path.path_join(["solo"]) != "solo") { return 8; }
    var empty: string[] = [];
    if (path.path_join(empty) != "") { return 9; }

    // path_parent — handle root and trailing-slash cases.
    if (path.path_parent("/a/b/c") != "/a/b") { return 10; }
    if (path.path_parent("a/b") != "a") { return 11; }
    if (path.path_parent("a") != "") { return 12; }
    if (path.path_parent("/") != "/") { return 13; }
    if (path.path_parent("") != "") { return 14; }
    if (path.path_parent("/a") != "/") { return 15; }
    if (path.path_parent("/a/b/") != "/a") { return 16; }

    // path_file_name — last component.
    if (path.path_file_name("/a/b/c.txt") != "c.txt") { return 17; }
    if (path.path_file_name("file") != "file") { return 18; }
    if (path.path_file_name("/") != "") { return 19; }
    if (path.path_file_name("") != "") { return 20; }
    if (path.path_file_name("a/b/") != "b") { return 21; }

    // path_extension — last .X suffix on the last component.
    if (path.path_extension("a.txt") != "txt") { return 22; }
    if (path.path_extension("/path/to/file.log") != "log") { return 23; }
    if (path.path_extension("multi.dot.tar.gz") != "gz") { return 24; }
    if (path.path_extension("noext") != "") { return 25; }
    if (path.path_extension(".hidden") != "") { return 26; }    // leading-dot file
    if (path.path_extension("a.b/.hidden") != "") { return 27; }
    if (path.path_extension("") != "") { return 28; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (paths)", code)
	}
}

func TestArm64Radix(t *testing.T) {
	src := `
import "core/int";
import "std/format";
function main(): i32 {
    // Parse — bases 2 / 8 / 10 / 16 / 36.
    match (int.parse_int_radix("ff", 16))   { Some(v) => { if (v != 255) { return 1; } }, None => { return 2; }, }
    match (int.parse_int_radix("FF", 16))   { Some(v) => { if (v != 255) { return 3; } }, None => { return 4; }, }
    match (int.parse_int_radix("1010", 2))  { Some(v) => { if (v != 10) { return 5; } }, None => { return 6; }, }
    match (int.parse_int_radix("777", 8))   { Some(v) => { if (v != 511) { return 7; } }, None => { return 8; }, }
    match (int.parse_int_radix("12345", 10)){ Some(v) => { if (v != 12345) { return 9; } }, None => { return 10; }, }
    match (int.parse_int_radix("z", 36))    { Some(v) => { if (v != 35) { return 11; } }, None => { return 12; }, }

    // Sign handling.
    match (int.parse_int_radix("-ff", 16))  { Some(v) => { if (v != (0 - 255)) { return 13; } }, None => { return 14; }, }
    match (int.parse_int_radix("+ff", 16))  { Some(v) => { if (v != 255) { return 15; } }, None => { return 16; }, }

    // Malformed input — None for every shape.
    match (int.parse_int_radix("", 10))     { Some(_) => { return 17; }, None => { } }
    match (int.parse_int_radix("-", 10))    { Some(_) => { return 18; }, None => { } }
    match (int.parse_int_radix("gg", 16))   { Some(_) => { return 19; }, None => { } }
    match (int.parse_int_radix("12", 1))    { Some(_) => { return 20; }, None => { } }
    match (int.parse_int_radix("12", 37))   { Some(_) => { return 21; }, None => { } }

    // Format — same base spread, plus sign + zero + negative.
    if (int.int_to_string_radix(255, 16) != "ff") { return 22; }
    if (int.int_to_string_radix(10, 2) != "1010") { return 23; }
    if (int.int_to_string_radix(511, 8) != "777") { return 24; }
    if (int.int_to_string_radix(12345, 10) != "12345") { return 25; }
    if (int.int_to_string_radix(35, 36) != "z") { return 26; }
    if (int.int_to_string_radix(0, 16) != "0") { return 27; }
    if (int.int_to_string_radix(0 - 255, 16) != "-ff") { return 28; }

    // Round-trip — parse(format.format(n)) == Some(n).
    match (int.parse_int_radix(int.int_to_string_radix(0 - 12345, 16), 16)) {
        Some(v) => { if (v != (0 - 12345)) { return 29; } },
        None => { return 30; },
    }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (radix parse/format)", code)
	}
}

func TestArm64StringExtras(t *testing.T) {
	src := `
import "std/string";
function bstr(b: boolean): string { if (b) { return "true"; } return "false"; }

function main(): i32 {
    // fields — runs of whitespace as separator, no empties.
    var fs: string[] = "  hello\tworld\nfoo bar  ".fields();
    if (fs.len() != 4) { return 1; }
    if (fs[0] != "hello") { return 2; }
    if (fs[3] != "bar") { return 3; }
    if (("".fields()).len() != 0) { return 4; }
    if (("   \t\n".fields()).len() != 0) { return 5; }
    if (("solo".fields()).len() != 1) { return 6; }

    // eq_ignore_ascii_case — symmetric in both arms.
    if (!"Content-Type".eq_ignore_ascii_case("content-type")) { return 7; }
    if (!"HELLO".eq_ignore_ascii_case("hello")) { return 8; }
    if ("foo".eq_ignore_ascii_case("foobar")) { return 9; }
    if ("apple".eq_ignore_ascii_case("banana")) { return 10; }
    if (!"".eq_ignore_ascii_case("")) { return 11; }

    // strip_prefix — Option[string] payload is the tail.
    match ("hello world".strip_prefix("hello ")) {
        Some(r) => { if (r != "world") { return 12; } },
        None => { return 13; },
    }
    match ("hello world".strip_prefix("nope")) {
        Some(_) => { return 14; },
        None => { },
    }

    // strip_suffix — Option[string] payload is the head.
    match ("file.txt".strip_suffix(".txt")) {
        Some(h) => { if (h != "file") { return 15; } },
        None => { return 16; },
    }
    match ("file.txt".strip_suffix(".log")) {
        Some(_) => { return 17; },
        None => { },
    }
    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (string extras: fields/strip/eq_case)", code)
	}
}

func TestArm64StringLines(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    var lf: string[] = "a\nb\nc".lines();
    if (lf.len() != 3) { return 1; }
    if (lf[0] != "a") { return 2; }
    if (lf[1] != "b") { return 3; }
    if (lf[2] != "c") { return 4; }

    var crlf: string[] = "a\r\nb\r\nc".lines();
    if (crlf.len() != 3) { return 5; }
    if (crlf[0] != "a") { return 6; }
    if (crlf[1] != "b") { return 7; }
    if (crlf[2] != "c") { return 8; }

    var trail: string[] = "a\nb\n".lines();
    if (trail.len() != 2) { return 9; }

    var solo: string[] = "\n".lines();
    if (solo.len() != 1) { return 10; }
    if (solo[0] != "") { return 11; }

    if (("".lines()).len() != 0) { return 12; }

    var partial: string[] = "abc".lines();
    if (partial.len() != 1) { return 13; }
    if (partial[0] != "abc") { return 14; }

    return 0;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("got %d, want 0 (string lines)", code)
	}
}

// arm64 empty-string sentinel: the string-constructing runtime
// helpers (__fern_strcat, __str_slice, string_from_bytes_unchecked) skip
// the alloc + memcpy and return the shared .LStr_Empty sentinel
// when the result length is 0. Verified behaviourally: len()
// returns 0, equality with "" holds, and downstream concat with
// a non-empty operand still produces the right bytes.
// Empty u8[] sentinel: `__alloc_u8(0)` returns a shared static
// `[length=0]` buffer rather than allocating a fresh 4-byte
// length-only block. Mirrors the x86_64 suite.
func TestArm64EmptyU8Sentinel(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"len-on-empty-u8", `function main(): i32 {
    var bs: u8[] = __alloc_u8(0);
    return bs.len();
}`, 0},
		{"string-from-empty-bytes", `function main(): i32 {
    var bs: u8[] = __alloc_u8(0);
    var s: string = string_from_bytes_unchecked(bs);
    return s.len();
}`, 0},
		{"to-lower-empty-string", `
import "std/string";
function main(): i32 {
    var s: string = "".to_lower();
    return s.len();
}`, 0},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

func TestArm64EmptyStringSentinel(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"concat-of-empties", `function main(): i32 {
    var s: string = "abcd";
    var a: str = s[0:0];
    var b: str = s[0:0];
    return (a + b).len();
}`, 0},
		{"zero-width-slice", `function main(): i32 {
    var s: string = "abcd";
    return s[2:2].len();
}`, 0},
		{"from-empty-bytes", `function main(): i32 {
    var bs: u8[] = __alloc_u8(0);
    return (string_from_bytes_unchecked(bs)).len();
}`, 0},
		{"sentinel-roundtrip", `function main(): i32 {
    var s: string = "world";
    var empty: str = s[0:0];
    return ("hello, " + empty + s).len();
}`, 12},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

// Enum sentinels: any payloadless enum variant returns a shared
// static `[tag=N]` address rather than allocating a fresh tag-
// only heap box. Mirrors the x86_64 suite.
func TestArm64EnumPayloadlessSentinel(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"user-enum-payloadless", `enum Color { Red, Green, Blue }
function pick(): Color { return Green; }
function main(): i32 {
    match (pick()) {
        Red => { return 1; },
        Green => { return 2; },
        Blue => { return 3; }
    }
}`, 2},
		{"user-enum-mixed", `enum Light { On, Off, Dim(i32) }
function f(): Light { return Off; }
function main(): i32 {
    match (f()) {
        On => { return 1; },
        Off => { return 2; },
        Dim(level) => { return level; }
    }
}`, 2},
		{"user-enum-payloaded-then-payloadless", `enum Light { Dim(i32), On, Off }
function main(): i32 {
    match (Off) {
        Dim(n) => { return n; },
        On => { return 99; },
        Off => { return 42; }
    }
}`, 42},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

// Option.None sentinel: `None` returns a shared static address
// rather than allocating a fresh 4-byte tag-only heap box.
// Mirrors the x86_64 suite of the same shape.
func TestArm64OptionNoneSentinel(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"match-none", `function f(): Option[i32] { return None; }
function main(): i32 {
    match (f()) {
        Some(x) => { return 99; },
        None => { return 42; }
    }
}`, 42},
		{"match-some", `function f(): Option[i32] { return Some(7); }
function main(): i32 {
    match (f()) {
        Some(x) => { return x; },
        None => { return 99; }
    }
}`, 7},
		{"two-nones-share-sentinel", `function f(): Option[i32] { return None; }
function g(): Option[i32] { return None; }
function main(): i32 {
    var _: Option[i32] = f();
    var __: Option[i32] = g();
    match (f()) {
        Some(x) => { return 99; },
        None => { return 1; }
    }
}`, 1},
		{"none-equal-none", `function main(): i32 {
    var a: Option[i32] = None;
    var b: Option[i32] = None;
    match (a) {
        Some(_) => { return 99; },
        None => {
            match (b) {
                Some(_) => { return 98; },
                None => { return 17; }
            }
        }
    }
}`, 17},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

// arm64 small-string-optimisation (tagged-pointer inline).
// Strings of length 1..7 produced by __fern_strcat / __str_slice /
// string_from_bytes_unchecked ride in a 64-bit register (LSB tag = 1, bits
// 1..3 = length, bytes 1..7 = data) rather than being allocated
// on the heap. Verified behaviourally — mirrors the x86_64 suite
// in PR #300; same encoding, different ISA.
func TestArm64SsoInline(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		// __str_slice produces inline at 1..7, heap at 8+.
		{"slice-len-1-inline", `function main(): i32 {
    return ("abcdefghij"[0:1]).len();
}`, 1},
		{"slice-len-7-inline", `function main(): i32 {
    return ("abcdefghij"[0:7]).len();
}`, 7},
		{"slice-len-8-heap", `function main(): i32 {
    return ("abcdefghij"[0:8]).len();
}`, 8},
		// Inline byte indexing must still match the source bytes.
		{"inline-index-first-byte", `function main(): i32 {
    var s: str = "abcdefghij"[0:5]; // inline "abcde"
    return s[0] as i32;
}`, 97},
		{"inline-index-last-byte", `function main(): i32 {
    var s: str = "abcdefghij"[0:5];
    return s[4] as i32;
}`, 101},
		// Equality across forms.
		{"inline-eq-same", `function main(): i32 {
    var a: str = "abcdef"[0:3];
    var b: str = "xabc"[1:4];
    if (a == b) { return 1; }
    return 0;
}`, 1},
		{"inline-eq-heap-same", `function main(): i32 {
    var a: str = "abcdefghij"[0:3];
    var b: string = "abc";
    if (a == b) { return 1; }
    return 0;
}`, 1},
		{"inline-ne", `function main(): i32 {
    var a: str = "abcdef"[0:3];
    var b: string = "xyz";
    if (a != b) { return 1; }
    return 0;
}`, 1},
		// Concat chains.
		{"concat-inline-plus-inline-inline", `function main(): i32 {
    var a: str = "abcdef"[0:3];
    var b: string = "xyz";
    return (a + b).len();
}`, 6},
		{"concat-inline-plus-inline-heap", `function main(): i32 {
    var a: str = "abcdef"[0:5];
    var b: string = "fghij";
    return (a + b).len();
}`, 10},
		{"concat-roundtrip-bytes", `function main(): i32 {
    var a: str = "abcdef"[0:3];
    var b: string = "DEF";
    var c: string = a + b;
    if (c == "abcDEF") { return 1; }
    return 0;
}`, 1},
		// string_from_bytes_unchecked inline.
		{"sfb-inline", `function main(): i32 {
    var bs: u8[] = __alloc_u8(3);
    bs = bs.with(0, 65 as u8);
    bs = bs.with(1, 66 as u8);
    bs = bs.with(2, 67 as u8);
    var s: string = string_from_bytes_unchecked(bs);
    if (s == "ABC") { return 1; }
    return 0;
}`, 1},
		// print(inline) — write syscall must materialise.
		{"print-inline", `function main(): i32 {
    var s: str = "abcdefgh"[0:5];
    print(s);
    return s.len();
}`, 5},
		// Triple concat.
		{"triple-concat-via-inline", `function main(): i32 {
    var s: string = "aa" + "bb" + "ccddee";
    return s.len();
}`, 10},
		// FieldAccess.len regression.
		{"field-access-len-inline", `struct Box { s: string }
function main(): i32 {
    var b: Box = Box { s: "abcdefgh"[0:5] + "" };
    return b.s.len();
}`, 5},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

// arm64 array literals + indexing. Pulls in __fern_alloc and
// the inline __arr_idx helper. Verifies the alloc + store
// + indexed read pipeline composes correctly under qemu.
func TestArm64ArrayLiteral(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    return xs[1];
}`, 20},
		{`function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4, 5];
    return xs.len();
}`, 5},
		{`function sum(xs: i32[]): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) {
        total = total + xs[i];
        i = i + 1;
    }
    return total;
}
function main(): i32 {
    return sum([1, 2, 3, 4, 5]);
}`, 15},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// arm64 Map runtime — exercises the codegen-alias rewrites
// (`map_new` → `map_new_impl`, `__method_Map_*` → `_impl`),
// the new `__store_i32` / `__load_i32` / `__memset` runtime
// helpers, and the lang prelude's open-addressing core. Same
// shape as TestWASMStateMapAcrossCalls.
func TestArm64Map(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(1, 100);
    m = m.insert(2, 200);
    return m.get_or(2, 0);
}`, 200},
		{`
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 8) {
        m = m.insert(i, i * 10);
        i = i + 1;
    }
    if (m.len() != 8) { return 1; }
    if (m.get_or(7, -1) != 70) { return 2; }
    return 42;
}`, 42},
		{`
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("alpha", 1);
    m = m.insert("beta", 2);
    m = m.insert("gamma", 3);
    return m.get_or("beta", -1) + m.len();
}`, 5},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// Regression for the `Map[K, V].get(k)` + `match` segfault on
// natives. The IR rewrites `__method_Map_get` →
// `__map_get_impl` (returns `Option[usize]`) but the caller
// keys its pair-form lookup off the user-visible
// `__method_Map_get` name. That alias isn't in `pairForm` —
// so the call-site emits OpCallDirect (heap-box ABI) — yet
// `__map_get_impl`'s body IS pair-form-eligible
// (`if … return None; … return Some(…)`) and gets lowered
// with the (tag, payload) register-return shape. The caller
// then pushes x0 only (treating tag as a heap-box pointer)
// and `ldr w0, [x0]` segfaults at the match's tag read.
//
// Fix: exclude `__map_get_impl` from pair-form eligibility
// so its ABI matches what the call site expects (heap-box).
// `m.get_or(k, default)` was unaffected — it returns V
// directly, no Option wrapper.
func TestArm64MapGetMatch(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"some_branch", `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(7, 42);
    match (m.get(7)) {
        Some(v) => { return v; },
        None => { return 0; }
    }
    return 1;
}`, 42},
		{"none_branch", `
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    match (m.get(7)) {
        Some(v) => { return 99; },
        None => { return 0; }
    }
    return 1;
}`, 0},
		{"string_key", `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("hello", 42);
    match (m.get("hello")) {
        Some(v) => { return v; },
        None => { return 0; }
    }
    return 1;
}`, 42},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// End-to-end exercise of the word-frequency pipeline that was
// segfaulting on natives before the Map.get + match pair-form
// fix. The shape: tokenize input → `Map[string, i32]` count
// table populated via `m.insert(key, n + 1)` inside the
// `Some(n) => …, None => …` match arm → snapshot keys +
// values via `.keys() / .values()` → print rows. The match
// branch inside the counting loop was the exact pattern
// TestArm64MapGetMatch pins in isolation; this test ensures
// the fix holds up in the realistic mix of slice keys (string
// slicing from the tokenizer), Array.push, and Map iteration.
func TestArm64MapGetMatchFullPipeline(t *testing.T) {
	src := `
import "core/map";
import "std/array";
function tokenize(s: string): string[] {
  var out: string[] = [];
  var i: i32 = 0;
  var sLen: i32 = s.len();
  var start: i32 = 0;
  while (i <= sLen) {
    var b: i32 = 0;
    if (i < sLen) { b = s[i] as i32; }
    var is_break: boolean = i == sLen || b == 32;
    if (is_break) {
      if (i > start) { out = out.append(s[start:i]); }
      start = i + 1;
    }
    i = i + 1;
  }
  return out;
}
function main(): i32 {
  var words: string[] = tokenize("a b a c b a");
  var counts: Map[string, i32] = map_new(8);
  var i: i32 = 0;
  while (i < words.len()) {
    var w: string = words[i];
    match (counts.get(w)) {
      Some(n) => { counts = counts.insert(w, n + 1); },
      None    => { counts = counts.insert(w, 1); }
    }
    i = i + 1;
  }
  // a → 3, b → 2, c → 1; sum 6.
  var keys: string[] = counts.keys();
  var vals: i32[] = counts.values();
  var sum: i32 = 0;
  var j: i32 = 0;
  while (j < vals.len()) {
    sum = sum + vals[j];
    j = j + 1;
  }
  return sum;
}`
	if _, code := compileAndRunArm64(t, src); code != 6 {
		t.Errorf("word-freq pipeline got %d, want 6", code)
	}
}

// Regression for i64 compares + division that silently used
// the w-form on arm64 (`cmp w1, w0`, `sdiv w0, w1, w0`), so
// any value whose upper 32 bits mattered got truncated:
//
//   - `n != (0 as i64)` inside the __int_to_string_u64 loop
//     exited the loop whenever the lower 32 bits hit zero,
//     stringifying every i64 as `n mod 2^32` (1234567000000
//     → "1911386048", 9223372036854775807 → "-1", etc.).
//   - `mag / 10` truncated the dividend to its lower 32 bits
//     before the divide, so the digit-extraction loop walked
//     the wrong number.
//
// Fix: comparisons + divisions now consult `op.Width` and
// emit x-form (`cmp x1, x0`, `sdiv/udiv x0, x1, x0`) when
// width=64 / pointer-width. Three sub-tests pin the shape;
// matching x86_64 sibling lives in TestX86_64I64CmpDivWidth.
func TestArm64I64CmpDivWidth(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"to_string_round_trip", `
import "std/i64";
function main(): i32 {
    var n: i64 = 1234567890123;
    var s: string = n.to_string();
    if (s == "1234567890123") { return 0; }
    return 1;
}`, 0},
		{"i64_max_to_string", `
import "std/i64";
function main(): i32 {
    var n: i64 = 9223372036854775807;
    var s: string = n.to_string();
    if (s == "9223372036854775807") { return 0; }
    return 1;
}`, 0},
		{"i64_mul_then_divide", `function main(): i32 {
    var n: i64 = (1234567 as i64) * (1000000 as i64);  // 1234567000000
    var q: i64 = n / (1000000 as i64);                 // 1234567
    if (q == (1234567 as i64)) { return 0; }
    return 1;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// Regression for unsigned right-shift on arm64. The OpShrS
// codegen unconditionally emitted `asr` (arithmetic right
// shift), which propagates the sign bit. Correct for signed
// types, but `(u64::MAX >> 1)` ended up as `u64::MAX` again
// instead of `2^63 - 1` because every shifted-in bit was 1.
//
// The wasm + x86_64 codegens picked `lsr` / `shr` based on
// `op.Unsigned` from the start; this aligns arm64 with that
// contract.
func TestArm64UnsignedRightShift(t *testing.T) {
	src := `
import "std/u64";
function main(): i32 {
    var n: u64 = 18446744073709551615 as u64;
    var r: u64 = n >> 1;
    var s: string = r.to_string();
    if (s == "9223372036854775807") { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("u64::MAX >> 1 round-trip got %d, want 0", code)
	}
}

// Regression for the tuple-literal i64-element layout bug.
// `(a + b, a - b)` where both operands are i64 used to lay
// out the tuple with 4-byte slots (size=8, offsets {0, 4})
// instead of 8-byte slots (size=16, offsets {0, 8}), so the
// second element's store partially clobbered the first's
// high half. Cross-target — wasm rejected the wat at parse
// time ("type mismatch: expected i32, found i64"); natives
// produced silently-wrong values (sums of garbage high
// bits).
//
// Root cause: `b.exprType(*ast.Binary)` only handled the
// string-concat / string-cmp special cases and returned nil
// for numeric binaries. `payloadSlotSize(nil, ptrW)`
// defaulted to 4. Now exprType returns
// `NumberType{Width: x.IntWidth}` / `FloatType{Width: x.FloatWidth}`
// when the checker stamped one.
func TestArm64I64TupleElementLayout(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"add_sub_pair", `function compute(a: i64, b: i64): (i64, i64) {
    return (a + b, a - b);
}
function main(): i32 {
    var p = compute(1234567890123, 1000);
    if (p.0 == 1234567891123 && p.1 == 1234567889123) { return 0; }
    return 1;
}`, 0},
		{"divmod_inline", `function compute(a: i64, b: i64): (i64, i64) {
    return (a / b, a - (a / b) * b);
}
function main(): i32 {
    var p = compute(1234567890123, 1000);
    if (p.0 == 1234567890 && p.1 == 123) { return 0; }
    return 1;
}`, 0},
		{"f64_elements", `function pair(a: f64, b: f64): (f64, f64) {
    return (a + b, a * b);
}
function main(): i32 {
    var p = pair(1.5, 2.5);
    if (p.0 == 4.0 && p.1 == 3.75) { return 0; }
    return 1;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// Tuple-literal element-type propagation. Two issues fed
// the same observation — `var p: (string, i64) = ("hi",
// 1234567890123)` either rejected as "(string, i32) not
// assignable" or compiled with the i64 element packed into
// a 4-byte slot:
//
//  1. settleNumeric had no TupleType case, so each element
//     was checked in isolation against checkExpr's default
//     (i32 for an unsuffixed integer literal).
//  2. postSettleType + b.exprType didn't re-derive the
//     tuple/element type from the literal after settle,
//     so the type check and slot-sizing both saw the pre-
//     settle width.
//
// Fix splits across the checker (settle + postSettle) and
// the IR (exprType reports NumberLit's settled Width back).
func TestArm64TupleMixedElementSettle(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"string_and_i64", `function main(): i32 {
    var p: (string, i64) = ("hello", 1234567890123);
    if (p.0 == "hello" && p.1 == 1234567890123) { return 0; }
    return 1;
}`, 0},
		{"two_i64_literals", `function main(): i32 {
    var p: (i64, i64) = (1234567890123, 9876543210);
    if (p.0 == 1234567890123 && p.1 == 9876543210) { return 0; }
    return 1;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// Regression for tuple literals whose element is a
// `CastExpr` (`n as i64`) or a match expression returning a
// cast. The IR's `b.exprType(*ast.CastExpr)` previously
// returned nil; tuple-slot sizing fell back to the 4-byte
// default and silently truncated wide i64 elements (the
// observed `1234567890123` came back as `431409005771`).
//
// Match propagation was the indirect path — exprType
// recursed on each arm body, and every arm body was a
// CastExpr.
//
// Fix: exprType returns `x.Target` for CastExpr.
func TestArm64TupleCastExprElementType(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"cast_in_tuple", `function main(): i32 {
    var n: i32 = 100;
    var p: (i64, i64) = (n as i64, 1234567890123);
    if (p.0 == 100 && p.1 == 1234567890123) { return 0; }
    return 1;
}`, 0},
		{"match_returning_cast", `enum E { A, B }
function main(): i32 {
    var e: E = A;
    var p: (i64, i64) = (
        match (e) {
            A => 1234567890123 as i64,
            B => 9876543210 as i64
        },
        100 as i64
    );
    if (p.0 == 1234567890123 && p.1 == 100) { return 0; }
    return 1;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// Regression for tuple-field-access in a tuple literal.
// `b.exprType(*ast.FieldAccess)` only handled struct field
// access via `fieldOwner`; tuple field access (numeric
// selector like `inner.0`) fell through and returned nil.
// TupleLit slot-sizing then defaulted to 4 bytes per
// element, truncating wide i64 reads.
//
// Observed: `(inner.0, inner.1)` where inner is `(i64, i32)`
// returned a garbage i64 (`182300902603`) and `0` instead of
// the original `(1234567890123, 42)`.
//
// Fix: exprType(FieldAccess) now checks `targetTupleType`
// first and returns the element type for numeric selectors.
func TestArm64NestedTupleFieldExprType(t *testing.T) {
	src := `function main(): i32 {
    var inner: (i64, i32) = (1234567890123, 42);
    var p: (i64, i32) = (inner.0, inner.1);
    if (p.0 == 1234567890123 && p.1 == 42) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("nested tuple field access got %d, want 0", code)
	}
}

// `return (1234567890123, 42)` from a function with a
// `(i64, i32)` signature used to be rejected as "return
// type mismatch: function returns (i64, i32) but
// expression is (i32, i32)". `settleNumeric` had been
// taught to propagate tuple-element widths into the AST,
// but the `Return` path didn't refresh its local `got`
// from the post-settle tree — so the assignability check
// still saw the pre-settle `(i32, i32)` shape and rejected
// a valid return.
//
// Fix: feed `got` through `postSettleType` after
// `settleNumeric`, mirroring the `Var` initializer path.
func TestArm64ReturnTupleI64Settle(t *testing.T) {
	src := `function pick(cond: boolean): (i64, i32) {
    if (cond) {
        return (1234567890123, 42);
    }
    return (9999999999999, 0 - 1);
}
function main(): i32 {
    var p = pick(true);
    if (p.0 == 1234567890123 && p.1 == 42) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("return (i64, i32) tuple got %d, want 0", code)
	}
}

// Tuple settle through match / if expressions. After #533
// (Return refreshes from post-settle) and #530
// (settleNumeric got a TupleType case), `return match (e)
// { A => (1234567890123, 42) }` still failed because
// settleNumeric on a TupleType hint only matched literal
// *ast.TupleLit nodes — it didn't recurse through MatchExpr
// or IfExpr arms.
//
// Fix: settleNumeric(ast.TupleType) recurses into MatchExpr
// arms and IfExpr Then/Else; postSettleType handles the
// same shapes so the assignability check sees the resolved
// widths.
func TestArm64TupleSettleInMatchAndIf(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"match_arm_tuple", `enum E { A }
function pick(e: E): (i64, i32) {
    return match (e) {
        A => (1234567890123, 42)
    };
}
function main(): i32 {
    var p = pick(A);
    if (p.0 == 1234567890123 && p.1 == 42) { return 0; }
    return 1;
}`, 0},
		{"if_arm_tuple", `function pick(cond: boolean): (i64, i32) {
    return if (cond) { (1234567890123, 42) } else { (9876543210, 0 - 1) };
}
function main(): i32 {
    var p = pick(true);
    var q = pick(false);
    if (p.0 == 1234567890123 && p.1 == 42 && q.0 == 9876543210 && q.1 == 0 - 1) { return 0; }
    return 1;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// Regression: tuple-field access on an array index.
// `arr[i].N` over `(i64, i32)[]` errored at IR-build time
// with `field access on unresolved struct ""` —
// `targetTupleType` recognised Ident / TupleLit / nested
// FieldAccess but had no `*ast.Index` case, so the
// FieldAccess lowering fell through to the struct path and
// `fieldOwner` returned "" for the indexed value.
//
// Fix: `targetTupleType(*ast.Index)` consults `exprType`,
// which already returns the array's ElemType (a TupleType
// when the array elements are tuples).
func TestArm64ArrayIndexTupleFieldAccess(t *testing.T) {
	src := `function main(): i32 {
    var arr: (i64, i32)[] = [(1234567890123, 42), (9876543210, 0 - 1)];
    if (arr[0].0 == 1234567890123 && arr[1].0 == 9876543210) {
        if (arr[0].1 == 42 && arr[1].1 == 0 - 1) {
            return 0;
        }
    }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("(i64, i32)[] index + field access got %d, want 0", code)
	}
}

// Regression for `var n: i64 = if cond { 1 } else { 2 }`
// and the matching f64 / match-expression flavour. The
// destination's int/float width never reached the arm
// bodies because settleInt / settleFloat only recursed
// through Unary / Binary nodes — IfExpr and MatchExpr were
// no-ops. The arm-body literals stayed at the i32 / f32
// default and the i64 / f64 load read the wrong width.
//
// Observed: `var n: i64 = if (true) { 1234567890123 } else
// { 0 }` returned `1912276171` (the lower 32 bits of the
// literal).
//
// Fix: settleInt + settleFloat recurse into IfExpr Then /
// Else and MatchExpr arm bodies with the same hint.
func TestArm64SettleIntFloatInCondArms(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"i64_if_expr", `function main(): i32 {
    var cond: boolean = true;
    var n: i64 = if (cond) { 1234567890123 } else { 0 };
    if (n == 1234567890123) { return 0; }
    return 1;
}`, 0},
		{"i64_match_expr", `enum E { A, B }
function main(): i32 {
    var e: E = A;
    var n: i64 = match (e) {
        A => 1234567890123,
        B => 9876543210
    };
    if (n == 1234567890123) { return 0; }
    return 1;
}`, 0},
		{"f64_if_expr", `function main(): i32 {
    var cond: boolean = true;
    var f: f64 = if (cond) { 3.14 } else { 0.0 };
    if (f > 3.0 && f < 4.0) { return 0; }
    return 1;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// `var n: i64 = if cond { a * b } else { 0 };` failed with
// "if-expression branches differ: i64 vs i32". The Then
// arm typed as i64 (concrete from `a * b`), but the Else
// arm's `0` was a polymorphic NumberLit that the checker
// reports as `NumberType{Polymorphic: true}`. unifyIfArms
// had no rule for polymorphic-vs-concrete, returned nil,
// and the error fired before settleInt got a chance to
// propagate the i64 hint to the literal.
//
// Fix: unifyIfArms treats a polymorphic NumberType as a
// match for any concrete NumberType / FloatType, returning
// the concrete side so settleInt can stamp the polymorphic
// literal's width on the post-check pass.
func TestArm64UnifyIfArmsPolymorphicNumeric(t *testing.T) {
	src := `function main(): i32 {
    var a: i64 = 1000000;
    var b: i64 = 1234567;
    var n: i64 = if (true) { a * b } else { 0 };
    if (n == 1234567000000) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("if (a*b) else 0 with i64 lhs got %d, want 0", code)
	}
}

// Sibling to #537: the match-expression arm-unify path
// used a plain ast.Equal check, so a polymorphic NumberLit
// (`0`) in one arm vs a concrete i64 expression (`a * b`)
// in another erred at type-check time as
// "match-expression arms differ: i64 vs i32" — the literal
// never reached settleInt to be widened.
//
// Fix: route the arm unify through `unifyIfArms` so the
// polymorphic-vs-concrete widening rules apply to match
// arms the same way they do to if-expression arms.
func TestArm64MatchExprUnifyPolyNumeric(t *testing.T) {
	src := `enum E { A, B }
function main(): i32 {
    var a: i64 = 1234567;
    var b: i64 = 1000000;
    var e: E = A;
    var n: i64 = match (e) {
        A => a * b,
        B => 0
    };
    if (n == 1234567000000) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("match (a*b) (0) with i64 lhs got %d, want 0", code)
	}
}

// Regression for the `?` try-op on Option[i64] / Result[i64, _].
// The IR's TryOp lowering loaded the success-path payload
// at the hardcoded `ptr + 4` offset, but `emitEnumNew`
// stores 8-byte payloads (i64 / f64 / two-word strings)
// at `ptr + 8` to keep them 8-byte aligned past the
// 4-byte tag. The success branch read the alignment
// padding instead of the payload — every i64 unwrap
// returned a 0-with-junk-high-bits value (observed:
// 8213163615365103616 for an Option[i64] holding
// 1234567890123).
//
// Fix: ask `payloadLayout` for the payload's actual
// offset and emit that immediate instead of `4`.
func TestArm64TryOpI64PayloadOffset(t *testing.T) {
	src := `function fetch(): Option[i64] {
    return Some(1234567890123);
}

function process(): Option[i64] {
    var v: i64 = fetch()?;
    return Some(v + 100);
}

function main(): i32 {
    match (process()) {
        Some(n) => {
            if (n == 1234567890223) { return 0; }
            return 1;
        },
        None => { return 2; },
    }
    return 99;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("Option[i64] try-op got %d, want 0", code)
	}
}

// Regression for variant constructor calls whose payload
// needed post-settle widening. `var o: Option[(i64, i32)] =
// Some((1234567890123, 42));` failed with "cannot assign
// Option[(i32, i32)] to variable of type Option[(i64, i32)]"
// because postSettleType returned the pre-settle EnumType
// type unchanged — it didn't recompute Args from the
// post-settle constructor argument.
//
// settleNumeric DID propagate the destination type into
// the tuple literal's elements (i64 stamped on the wide
// literal), but the surrounding Some(...) call kept its
// pre-settle (i32, i32) shape for the assignable check.
//
// Fix: postSettleType(*ast.Call) re-runs postSettleType on
// each constructor argument when prior is an EnumType with
// matching arity, rebuilding the Args from the resolved
// argument types.
func TestArm64PostSettleVariantCall(t *testing.T) {
	src := `function main(): i32 {
    var o: Option[(i64, i32)] = Some((1234567890123, 42));
    match (o) {
        Some(p) => {
            if (p.0 == 1234567890123 && p.1 == 42) { return 0; }
            return 1;
        },
        None => { return 2; },
    }
    return 99;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("Option[(i64, i32)] variant call got %d, want 0", code)
	}
}

// `var arr: Option[i64][] = [Some(1234567890123), None,
// Some(9876543210)];` failed with "array element type
// Option, expected Option[i64]". The ArrayLit check
// compared elements with raw `ast.Equal`, so a
// `Some(literal)` (typed `Option[<polymorphic>]`) vs
// a payloadless `None` (typed `Option` with empty Args)
// triggered the error even though both shapes are
// destination-compatible.
//
// Fix: ArrayLit element-check routes mismatches through
// `unifyIfArms`, picking the concrete side. The
// no-payload-vs-with-payload enum unify (already in
// unifyIfArms) and the polymorphic-vs-concrete numeric
// unify (#537) both flow through, so a mixed array of
// Some / None lands on the concrete `Option[<num>]` and
// settleNumeric walks each element with the right hint.
func TestArm64ArrayLitOptionMixedSomeNone(t *testing.T) {
	src := `function main(): i32 {
    var arr: Option[i64][] = [Some(1234567890123), None, Some(9876543210)];
    var s: i64 = 0;
    var i: i32 = 0;
    while (i < arr.len()) {
        match (arr[i]) {
            Some(n) => { s = s + n; },
            None => {},
        }
        i = i + 1;
    }
    if (s == 1244444433333) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("[Some(i64), None, Some(i64)] got %d, want 0", code)
	}
}

// `var m: Map[string, i64] = Map { "a": 1234567890123, ... };`
// rejected with "cannot assign Map[string, i32] to variable
// of type Map[string, i64]". MapLit's first-entry walk
// returned `NumberType{Polymorphic: true}` for the bare
// literal, and settleNumeric had no `ast.StructType` case
// to flow the destination's V (i64) into each entry. The
// IR also keyed off the MapLit's own `KeyType` /
// `ValueType` stamps for the runtime keyKind / valKind
// tags, so even compiling past the assignable check left
// the box-allocator emitting the wrong stride for i64
// values.
//
// Fix:
//
//   - settleNumeric gains a StructType case that walks each
//     MapLit entry's key + value with the destination's
//     `Map[K, V]` Args.
//   - postSettleType returns a fresh StructType built from
//     the resolved first entry, and refreshes the MapLit
//     node's KeyType / ValueType in place so the IR sees
//     the post-settle widths.
func TestArm64MapLitI64ValueSettle(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i64] = Map { "a": 1234567890123, "b": 9876543210 };
    var v: i64 = m.get_or("a", 0);
    if (v == 1234567890123) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("Map[string, i64] literal got %d, want 0", code)
	}
}

// Generic-function calls inferred their type-param T from
// the arguments alone — for a polymorphic NumberLit arg the
// inferred T was `NumberType{Polymorphic: true}` and the
// arg's width never settled. `var x: i64 = pick(true,
// 1234567890123, 0);` against
// `function pick[T](cond: boolean, a: T, b: T): T`
// silently truncated to `1912276171` (the literal's lower
// 32 bits).
//
// Fix: settleInt gains an `*ast.Call` case that walks a
// generic-function call's args, settling each arg position
// that maps to the T parameter against the destination
// width. The monomorph pass picks up the resolved width
// via the refreshed `TypeArgs`.
func TestArm64SettleGenericCallArgs(t *testing.T) {
	src := `function pick[T](cond: boolean, a: T, b: T): T {
    if (cond) { return a; }
    return b;
}
function main(): i32 {
    var x: i64 = pick(true, 1234567890123, 0);
    if (x == 1234567890123) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("generic call with bare i64 literal got %d, want 0", code)
	}
}

// Sibling to #543. The settleInt Call case widened generic
// arg widths against an integer destination, but
// settleFloat had no Call case — so `var x: f64 = pick(true,
// 3.14, 0.0);` printed `0` instead of `3.14`. The float
// literal arguments stayed at the f32 / Polymorphic
// default, the destination's 8-byte load read the wrong
// half of the operand-stack slot, and the d0 register
// received a zero.
//
// Fix: mirror the settleInt Call case in settleFloat —
// recognise generic-function calls via
// c.info.GenericFuncs, settle each `ParamType` arg
// position against the destination float type, and
// re-stamp TypeArgs[0] for monomorph.
func TestArm64SettleGenericCallArgsFloat(t *testing.T) {
	src := `function pick[T](cond: boolean, a: T, b: T): T {
    if (cond) { return a; }
    return b;
}
function main(): i32 {
    var x: f64 = pick(true, 3.14, 0.0);
    if (x > 3.0 && x < 4.0) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("generic call with f64 literal got %d, want 0", code)
	}
}

// Settle for `EnumType` hints recurses through if / match
// arms. Before: `return match (c) { Set => Some(1234567890123),
// Reset => Some(0), Init => None };` against an `Option[i64]`
// return type silently produced `Some(0)` because the
// settleNumeric EnumType case only matched a top-level
// `*ast.Call` — when the variant constructor sat inside a
// match arm body, the literal in `Some(...)` never saw
// the destination's `T = i64` and stayed at the i32
// default.
//
// Mirrors #534 (TupleType arm recursion) and #538
// (match-arm unify) — same fan-out pattern for enum hints.
func TestArm64SettleEnumInMatchAndIfArms(t *testing.T) {
	src := `enum Cmd { Set, Reset, Init }
function get(c: Cmd): Option[i64] {
    return match (c) {
        Set => Some(1234567890123),
        Reset => Some(0),
        Init => None
    };
}
function main(): i32 {
    match (get(Set)) {
        Some(v) => {
            if (v == 1234567890123) { return 0; }
            return 1;
        },
        None => { return 2; },
    }
    return 99;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("Option[i64] from match arm got %d, want 0", code)
	}
}

// Sibling to #545: settleNumeric for `ArrayType` /
// `SliceType` hints only matched a top-level `*ast.ArrayLit`.
// When the array literal sat inside an `IfExpr` or
// `MatchExpr`, the destination element type never reached
// the inner literal — `var arr: i64[] = if cond { [...] }
// else { [...] };` rejected with
// "cannot assign i32[] to variable of type i64[]".
//
// Fix: ArrayType and SliceType cases recurse through
// IfExpr Then/Else and MatchExpr arm bodies with the same
// hint. Same shape as the TupleType / EnumType fan-outs
// added previously.
func TestArm64SettleArraySliceInCondArms(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"if_expr_array", `function main(): i32 {
    var cond: boolean = true;
    var arr: i64[] = if (cond) { [1234567890123, 9876543210] } else { [0, 0] };
    if (arr[0] == 1234567890123 && arr[1] == 9876543210) { return 0; }
    return 1;
}`, 0},
		{"match_expr_array", `enum E { A, B }
function pick(e: E): i64[] {
    return match (e) {
        A => [1234567890123, 9876543210],
        B => [0, 0]
    };
}
function main(): i32 {
    var arr: i64[] = pick(A);
    if (arr[0] == 1234567890123 && arr[1] == 9876543210) { return 0; }
    return 1;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// Sibling to #546: settleNumeric for the StructType
// (`Map[K, V]`) hint only matched a top-level
// `*ast.MapLit`. When the map literal sat inside an
// `IfExpr` or `MatchExpr`, the destination's V never
// reached the inner literal, so a bare-i64 value stayed
// at the i32 default and `Map[string, i64]` rejected as
// `Map[string, i32]`.
//
// Fix: recurse the Map-shaped StructType hint into IfExpr
// Then/Else and MatchExpr arm bodies.
func TestArm64SettleMapLitInCondArms(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var cond: boolean = true;
    var m: Map[string, i64] = if (cond) { Map { "a": 1234567890123 } } else { Map { "a": 0 } };
    var v: i64 = m.get_or("a", 0);
    if (v == 1234567890123) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("Map[string, i64] from if-expr got %d, want 0", code)
	}
}

// Sibling to #530's NumberLit case. The IR's
// b.exprType(*ast.FloatLit) returned nil — TupleLit
// slot-sizing fell back to the 4-byte default for
// `(3.14, 42)` against `(f64, i32)` and the f64 store /
// load mis-aligned its operand-stack slot. Observed:
// `var p: (f64, i32) = if (true) { (3.14, 42) } else
// { (0.0, 0) };` printed `0` for p.0 instead of `3.14`.
//
// Fix: exprType(*ast.FloatLit) returns `FloatType{Width:
// x.Width}` once the checker has stamped a width.
func TestArm64FloatLitInTupleViaIfExpr(t *testing.T) {
	src := `function main(): i32 {
    var cond: boolean = true;
    var p: (f64, i32) = if (cond) { (3.14, 42) } else { (0.0, 0) };
    if (p.0 > 3.0 && p.0 < 4.0 && p.1 == 42) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("(f64, i32) tuple via if-expr got %d, want 0", code)
	}
}

// `struct S { v: Option[i64] }` initialised with `S { v:
// None }` rejected with "field v: expected Option[i64],
// got Option". The StructLit check used a raw `ast.Equal`
// comparison between the field's declared type and the
// value's `checkExpr` result — `None` checks to `Option`
// with empty Args (the destination's T is unknown to the
// caller), and the strict Equal fails.
//
// Same family as #541 (array element widen via
// unifyIfArms). Fix: route StructLit's field-type mismatch
// through `unifyIfArms`, which already knows
// no-payload-vs-with-payload enums are compatible.
func TestArm64StructFieldNoneOption(t *testing.T) {
	src := `struct Wrap { inner: Option[i64] }
function main(): i32 {
    var w1: Wrap = Wrap { inner: Some(1234567890123) };
    var w2: Wrap = Wrap { inner: None };
    match (w1.inner) {
        Some(v) => {
            if (v != 1234567890123) { return 1; }
        },
        None => { return 2; },
    }
    match (w2.inner) {
        Some(_) => { return 3; },
        None => { return 0; },
    }
    return 99;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("struct { Option[i64] } with None initialiser got %d, want 0", code)
	}
}

// A match-expression returning i64 was lowered with a
// scratch-slot type of polymorphic NumberType{} — that
// landed as `local.set $T (i32)` on wasm, and the i64 arm
// body's `local.set` failed with "type mismatch: expected
// i32, found i64" at validation time.
//
// Native targets store every slot as an 8-byte word so
// the mismatch was hidden, but wasm declares each local's
// width explicitly. Symptom: any function whose body is
// `return match (o) { Some(n) => i64-expr, None => 0 };`
// rejected at wasm compile time.
//
// Fix: the IR's MatchExpr lowering walks the arm bodies,
// picks the first non-polymorphic NumberType / FloatType
// it finds via `exprType`, and uses that for the scratch
// slot. Wasm's local declaration then sees i64 / f64 and
// the validator accepts the store. The arm-body settle
// from #534 / #545 already resolves widths; this just
// consumes them.
//
// The test pin lives on arm64 (where it passed silently
// before too) — the actual win is on wasm, exercised
// implicitly by the full e2e suite.
func TestArm64MatchExprI64ResultWidth(t *testing.T) {
	src := `struct Node { v: i64 }
function get(o: Option[Node]): i64 {
    return match (o) {
        Some(node) => node.v,
        None => 0 as i64
    };
}
function main(): i32 {
    var n: Node = Node { v: 1234567890123 };
    var s: i64 = get(Some(n));
    if (s == 1234567890123) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("match-expression returning i64 got %d, want 0", code)
	}
}

// Sibling to #537. The polymorphic-numeric branch in
// unifyIfArms had no float counterpart, so a polymorphic
// FloatLit (`0.0`) in one if-arm vs a concrete f64
// (struct field load) in another rejected the
// if-expression as "branches differ: f64 vs f32".
//
// Fix: unifyIfArms also pairs a polymorphic FloatType
// with a concrete FloatType, returning the concrete side
// so the settle pass can stamp the literal's width.
func TestArm64UnifyIfArmsPolymorphicFloat(t *testing.T) {
	src := `struct N { v: f64 }
function get(n: N, cond: boolean): f64 {
    return if (cond) { n.v } else { 0.0 };
}
function main(): i32 {
    var n: N = N { v: 3.14 };
    var s: f64 = get(n, true);
    if (s > 3.0 && s < 4.0) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("if-expr f64 vs poly float got %d, want 0", code)
	}
}

// An IfExpr returning i64 silently emitted the wasm block
// type as `(if (result i32))` even when both arms produced
// i64. Native targets ignored the block type, so the bug
// was wasm-only. The validator rejected:
//
//	"type mismatch: expected i32, found i64"
//
// Triggered when arm bodies aren't compile-time-constant
// (e.g. a function-parameter `a: i64` referenced from an
// arm); literal-only IfExprs escaped because the IR's
// constant fold inlined them.
//
// Mirrors the MatchExpr scratch-slot fix from #550 — same
// "consume the post-settle width" pattern, applied to
// IfExpr's BlockType selection. Added i64 / f64 branches
// for the wide-numeric arms.
//
// Test pin lives on arm64 (where it passed silently) —
// the actual win is on wasm, exercised implicitly by the
// e2e suite.
func TestArm64IfExprI64BlockType(t *testing.T) {
	src := `function pickOpt(cond: boolean, a: i64, b: i64): i64 {
    return if (cond) { a } else { b };
}
function main(): i32 {
    var r: i64 = pickOpt(true, 1234567890123, 0);
    if (r == 1234567890123) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("if-expr returning i64 got %d, want 0", code)
	}
}

// Two IR fold paths emitted an i32 constant onto the
// operand stack ahead of an i64 op, breaking the wasm
// validator's type discipline (the natives silently
// promoted the 32-bit slot to 64-bit on the operand
// stack so the failures were wasm-only):
//
//  1. `x * 2^k` strength reduction → `x << k` emitted
//     OpConstI32 for k regardless of the binary's
//     resolved width. For an i64 LHS, the subsequent
//     OpShl resolved to `i64.shl` and the i32 const on
//     the stack failed validation.
//  2. `x - x` / `x ^ x` self-identity fold emitted
//     OpConstI32 0 regardless of the resolved width.
//     Same i64.add / i64.sub consumer mismatch on wasm.
//
// Fixes: emit OpConstI64 (and Width=64 on OpShl in the
// strength-reduction path) when the binary's IntWidth is
// 64. Comparison self-identities (==, <=, >=, !=, <, >)
// still emit i32 — the result is bool regardless of
// operand width.
//
// Test pin lives on arm64 (where it passed silently) —
// the actual win is on wasm, exercised implicitly by the
// e2e suite.
func TestArm64IRFoldI64Constants(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"strength_reduction_mul", `function step(): Result[i64, string] {
    var v: i64 = 50;
    return Ok(v * 2);
}
function main(): i32 {
    match (step()) {
        Ok(n) => { if (n == 100) { return 0; } return 1; },
        Err(_) => { return 2; },
    }
    return 99;
}`, 0},
		{"self_identity_sub", `function step(): Result[i64, string] {
    var v: i64 = 50;
    return Ok(v - v);
}
function main(): i32 {
    match (step()) {
        Ok(n) => { if (n == 0) { return 0; } return 1; },
        Err(_) => { return 2; },
    }
    return 99;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// Two cross-target type-shape bugs that the wasm validator
// caught and natives silently absorbed:
//
//  1. b.exprType(*ast.Binary) returned the IR's
//     IntWidth-stamped NumberType even for comparison ops
//     (==, !=, <, <=, >, >=). The OPERAND width is what
//     drives codegen's `i64.eq` vs `i32.eq` selection, but
//     the RESULT type is bool (i32). Without the
//     comparison-op shortcut, `(a > b, a + b)` inside a
//     `(boolean, i64)` tuple inferred its first slot as
//     i64 — the tuple stride doubled, the i32 0/1 store
//     overflowed into the i64 slot, and wasm rejected the
//     load with "type mismatch: expected i64, found i32".
//
//  2. settleNumeric had no `*ast.TryOp` case. The
//     destination's hint applies to the inner expression's
//     payload, not to the TryOp itself. Without a wrap of
//     the hint in `Option[T]` / `Result[T, E]`,
//     `var v: f64 = Some(3.14)?;` left 3.14 at the f32
//     default and wasm rejected the f64 destination load
//     ("type mismatch: expected f64, found f32").
//
// Both fixes land in the same PR — they share the "the
// surrounding type-shape needs to reach the inner
// expression for settle to fire" pattern and were both
// silently miscompiled to a near-correct shape on natives.
func TestArm64BoolCmpAndTryOpSettle(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"bool_in_tuple_with_i64", `function step(a: i64, b: i64): (boolean, i64) {
    return (a > b, a + b);
}
function main(): i32 {
    var p = step(1234567890123, 100);
    if (p.0 && p.1 == 1234567890223) { return 0; }
    return 1;
}`, 0},
		{"f64_tryop_widen", `function process(): Option[f64] {
    var v: f64 = Some(3.14)?;
    return Some(v * 2.0);
}
function main(): i32 {
    match (process()) {
        Some(f) => { if (f > 6.0 && f < 7.0) { return 0; } return 1; },
        None => { return 2; },
    }
    return 99;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// A tail-recursive function returning i64 failed wasm
// validation with "type mismatch: expected i64, found
// i32". TailCallOptimize wraps the body in a `loop ... end`
// so the call sites become `local.set $param; br 0` — the
// loop never falls through. But the wasm validator still
// needs *something* of the function's return type after
// the loop end (to type-check the unreachable fall-off);
// the codegen padded that slot with `i32.const 0`
// regardless of the return type, which an i64 / f64
// signature rejected.
//
// Triggered any time a tail-recursive function returned
// i64 (e.g. `factorial`-style accumulator loops, recursive
// Fibonacci with i64 accumulator). Native targets ignored
// the validator and ran fine.
//
// Fix: branch on the return type for the padding — push
// `i64.const 0` for i64 returns alongside the existing
// `i32.const 0` / `f32.const 0` / `f64.const 0` cases.
//
// Tests pin a tail-recursive i64 factorial (20! =
// 2432902008176640000 — needs i64). arm64 already passed;
// wasm now joins it.
func TestArm64WasmI64TailCallReturn(t *testing.T) {
	src := `function fact(n: i32, acc: i64): i64 {
    if (n <= 1) { return acc; }
    return fact(n - 1, acc * (n as i64));
}
function main(): i32 {
    var r: i64 = fact(20, 1);
    if (r == 2432902008176640000) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("i64 tail-call factorial got %d, want 0", code)
	}
}

// A match-expression returning a string was lowered with
// a scratch slot typed as polymorphic NumberType{} — that
// landed as a single i32 local on wasm32. String values
// flow as the two-word `(data, len)` ABI on wasm32, so
// the arm body's pair-push failed validation
// ("expected i32 but nothing on stack") at the `local.set`
// site that expected a single i32 instead of a pair.
//
// Same code path as the i64 / f64 fix from #550 — the
// arm-body type walk now also recognises StringType and
// carries it through, so the wasm codegen declares
// `<slot>_data` and `<slot>_len` for the two-word fan
// out.
//
// Test pin lives on arm64 (where the bug was hidden by
// the i32 single-pointer string ABI); the actual win is
// on wasm via the e2e suite.
func TestArm64MatchExprStringResult(t *testing.T) {
	src := `
import "std/i64";
function fmt(o: Option[i64]): string {
    return match (o) {
        Some(v) => f"got {v}",
        None => "none"
    };
}
function main(): i32 {
    var s: string = fmt(Some(1234567890123));
    if (s == "got 1234567890123") { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("match-expression returning string got %d, want 0", code)
	}
}

// unifyIfArms used to bail on tuple types whose elements
// only differed by polymorphic-vs-concrete width. The
// previous `ast.Equal(a, b)` check failed for `(i32, f32)`
// vs `(i64, f32)` (when one arm's first element was a
// polymorphic NumberLit and the other was a concrete i64
// expression), and the if-expression rejected with:
//
//	branches differ: (i32, f32) vs (i64, f32)
//
// Same story for `(i64, f64)` mixed with `(i64, f32)` —
// any whole-tuple-Equal failure short-circuited the
// per-element widening.
//
// Fix: unifyIfArms learns to recurse element-wise into
// TupleType. The integer / float widening rules already
// added (#537, #551) flow through each element pair and
// the surrounding settle pass stamps the final widths.
func TestArm64UnifyIfArmsTupleElementWiden(t *testing.T) {
	src := `function pick(b: boolean): (i64, f64) {
    return if (b) { (1234567890123, 3.14) } else { (0 as i64, 0.0) };
}
function main(): i32 {
    var p = pick(true);
    if (p.0 == 1234567890123 && p.1 > 3.0) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("tuple if-expr with mixed-width elements got %d, want 0", code)
	}
}

// A generic function returning a tuple of type params
// failed type-check with the puzzling error "function
// returns (T, T) but expression is (T, T)" — both sides
// rendered identically.
//
// Root cause: `resolveType` had no `ast.TupleType` /
// `ast.SliceType` case. The parser builds bare type
// identifiers as `StructType{Name:"T"}`; resolveType walks
// the function's params + return type rewriting those into
// `ParamType{Name:"T"}` when the name is in the type-param
// set. With no TupleType recursion, a `(T, T)` return type
// kept its elements as `StructType` while `checkExpr((x,
// x))` returned a `TupleType` over the param's
// already-resolved `ParamType`. `ast.Equal` compared
// StructType vs ParamType and returned false; the user
// saw identical-looking sides reject each other.
//
// Same hole for `SliceType`, which had a `case ArrayType`
// already but the slice form (parser builds `[T]` as
// `ast.SliceType`) silently kept its inner StructType.
//
// Fix: resolveType recurses into TupleType.Elems and
// SliceType.Elem.
func TestArm64ResolveTypeTupleSliceGeneric(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"tuple_return", `function dup[T](x: T): (T, T) { return (x, x); }
function main(): i32 {
    var p = dup(42);
    if (p.0 == 42 && p.1 == 42) { return 0; }
    return 1;
}`},
		{"pair_two_params", `function pair[A, B](a: A, b: B): (A, B) { return (a, b); }
function main(): i32 {
    var p = pair(1234567890123, "hello");
    if (p.0 == 1234567890123 && p.1 == "hello") { return 0; }
    return 1;
}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != 0 {
				t.Errorf("got exit %d, want 0", code)
			}
		})
	}
}

// `resolveTypesInBlock` didn't carry the surrounding
// function's type-param set into local Var declarations,
// nested FuncDecls, or nested control-flow / Match arms.
// As a result, a generic function with a local
// `var m: Map[string, V] = map_new(0)` kept V as
// `StructType{Name:"V"}` (the parser's default for a
// bare-name type identifier) instead of `ParamType{"V"}`.
// The Map method dispatch then substituted Map's V with
// `StructType{"V"}`, but `checkExpr(v)` on the function
// param returned `ParamType{"V"}` — `ast.Equal` saw them
// as different and the user got the puzzling:
//
//	argument 3: expected V, got V
//
// — with identical-looking sides.
//
// Same root family as the resolveType TupleType /
// SliceType fix from #558. This PR threads the params
// map through resolveTypesInBlock so local Var types,
// nested FuncDecl signatures, and control-flow / Match
// arm bodies all consult the type-param set.
//
// Test pin: generic Map-returning function with a
// local `Map[string, V]` literal — `mk[V](v) ->
// Map[string, V]` round-trips a key/value via set + get.
func TestArm64GenericLocalMapType(t *testing.T) {
	src := `
import "core/map";
function mk[V](v: V): Map[string, V] {
    var m: Map[string, V] = map_new(0);
    m = m.insert("k", v);
    return m;
}
function main(): i32 {
    var m: Map[string, i32] = mk(42);
    var v: i32 = m.get_or("k", 0);
    return v - 42;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("generic local Map type got %d, want 0", code)
	}
}

// `var b: Box[i64] = Box { v: 1234567890123 };` failed
// with "cannot assign Box[i32] to variable of type
// Box[i64]". The StructLit checker's generic inference
// only saw the literal's pre-settle width — the `T = i32`
// default never widened against the destination
// `Box[i64]` annotation.
//
// Same family as the MapLit widening from #542 / the
// EnumType variant call refresh from #540. The generic
// struct literal needed:
//
//  1. settleNumeric(StructType{Args:[i64]}) walking
//     each field with the substituted type
//     (`Box.v` → i64), AND
//  2. postSettleType refreshing the StructType.Args
//     from the literal's TypeArgs stamp so the
//     assignable check sees the resolved shape.
//
// Fix: settleNumeric's StructType case handles
// `*ast.StructLit` with a generic StructDecl — builds
// the type-param sub, settles each field's value, and
// stamps `sl.TypeArgs`. postSettleType for StructLit
// returns the refreshed StructType from `x.TypeArgs`.
func TestArm64GenericStructLitSettle(t *testing.T) {
	src := `struct Box[T] { v: T }
function main(): i32 {
    var b: Box[i64] = Box { v: 1234567890123 };
    if (b.v == 1234567890123) { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("generic Box[i64] StructLit got %d, want 0", code)
	}
}

// Two stability / usability bugs hit when nested array
// literals included an empty inner array. Both were
// hiding behind each other:
//
//  1. `ast.ArrayType.String()` panicked with "invalid
//     memory address or nil pointer dereference" when
//     `Elem == nil`. The error formatter for the
//     checker's "array element type X, expected Y"
//     message tried to format the empty-array literal's
//     pre-settle type — which is `ArrayType{Elem: nil}`
//     because the empty `[]` has no element. The result
//     was a crash dump in the error message instead of
//     the actual diagnostic. Same hole in `SliceType`.
//  2. Even with the formatter fixed, the checker still
//     rejected `var arr: i64[][] = [[1234567890123],
//     [9876543210, 100], []];` as "array element type
//     [], expected i64[]". The empty `[]`'s type
//     doesn't carry an Elem; unifyIfArms had no rule
//     for empty-array-vs-typed-array. Adding that rule
//     lets the empty inner array inherit the outer's
//     element type from a non-empty sibling.
//
// Fix:
//   - ArrayType.String() and SliceType.String() return
//     "[]" when Elem is nil instead of dereferencing.
//   - unifyIfArms learns the empty-array compatibility
//     rule (mirrored for SliceType).
//
// Test pin: a 2D i64 array literal with an empty inner
// element.
func TestArm64ArrayLitEmptyInnerUnify(t *testing.T) {
	src := `function main(): i32 {
    var arr: i64[][] = [[1234567890123], [9876543210, 100], []];
    if (arr.len() == 3 && arr[0][0] == 1234567890123 && arr[1][1] == 100) {
        return 0;
    }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("2D array with empty inner got %d, want 0", code)
	}
}

// Inner-scope variable shadowing collapsed onto the outer
// scope's slot, so the outer reads silently saw the inner
// store's value after the inner block returned:
//
//	var x: i64 = 100;
//	if (true) {
//	    var x: i64 = 200;
//	}
//	print(x.to_string());  // printed 200, not 100
//
// The IR's `b.locals` flat name → slot map keyed by the
// AST name; two `var x` declarations both set `b.locals["x"]`
// in turn, second-write-wins. Both slots existed in
// `info.Locals[fn]` (distinct entries, distinct indices), so
// just reading them out preserved both, but every Ident
// reference resolved to the most-recently-bound slot.
//
// Fix: a new shadowrename pass walks each function body
// before closureconv / IR build, tracks a scope stack
// (block / if / for / match-arm / iflet bindings), and
// renames every shadowing Var / Destructure / pattern
// binding to `name$N`. References inside the shadowing
// scope follow via per-frame lookup. Native ABIs and wasm
// alike see post-rename names everywhere, so the IR's
// name-based slot map stays unambiguous.
//
// Three sub-tests pin sibling-shadow, depth-2 shadow, and
// for-loop counter shadowing the outer name.
func TestArm64VariableShadowing(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"shadow_in_if", `function main(): i32 {
    var x: i64 = 100;
    if (true) {
        var x: i64 = 200;
        if (x != 200) { return 1; }
    }
    if (x != 100) { return 2; }
    return 0;
}`, 0},
		{"shadow_depth_2", `function main(): i32 {
    var x: i64 = 1;
    if (true) {
        var x: i64 = 2;
        if (true) {
            var x: i64 = 3;
            if (x != 3) { return 1; }
        }
        if (x != 2) { return 2; }
    }
    if (x != 1) { return 3; }
    return 0;
}`, 0},
		{"shadow_for_counter", `function main(): i32 {
    var i: i32 = 100;
    for (var i: i32 = 0; i < 5; i = i + 1) { }
    return i - 100;
}`, 0},
		{"shadow_rhs_reads_outer", `function main(): i32 {
    var x: i64 = 10;
    var y: i64 = 0;
    if (true) {
        var x: i64 = x + 100;
        y = x;
    }
    if (x != 10 || y != 110) { return 1; }
    return 0;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got exit %d, want %d", code, c.want)
			}
		})
	}
}

// arm64 f32 / f64 arithmetic + comparisons. Float values
// live as raw bit patterns on the operand stack; the codegen
// fmov's them into the V-register file (s0/s1 for f32,
// d0/d1 for f64), runs the op, and fmov's the result back.
func TestArm64Floats(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		// f32 arithmetic
		{`function main(): i32 {
    var a: f32 = 3.5;
    var b: f32 = 1.5;
    return (a + b) as i32;
}`, 5},
		{`function main(): i32 {
    var a: f32 = 10.0;
    var b: f32 = 3.0;
    return (a / b) as i32;
}`, 3},
		// f64 arithmetic + comparison
		{`function main(): i32 {
    var pi: f64 = 3.14f64;
    var two: f64 = 2.0f64;
    if (pi * two > 6.0f64) { return 42; }
    return 0;
}`, 42},
		// Mixed: i32 → f64 → i32 round trip.
		{`function main(): i32 {
    var n: i32 = 7;
    var f: f64 = (n as f64) * 1.5f64;
    return f as i32;
}`, 10},
		// Float negation.
		{`function main(): i32 {
    var x: f32 = 5.5;
    var y: f32 = 0.0 - x;
    if (y < 0.0) { return 1; }
    return 0;
}`, 1},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// arm64 indirect calls: OpConstFunc (function value
// materialisation via adrp + add :lo12:) + OpCallIndirect
// (blr xN). Lets handlers be passed as function values to
// generic helpers like tcp_serve.
func TestArm64IndirectCall(t *testing.T) {
	_, code := compileAndRunArm64(t, `function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 {
    var f: (i32, i32) => i32 = add;
    return f(20, 22);
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (indirect call through function value)", code)
	}
}

// arm64 print / write / putchar — stdout builtins lowered to
// direct write(2) syscalls. Verifies the asm wires the right
// fd, length, and newline behaviour (`print` adds one, `write`
// does not).
func TestArm64Print(t *testing.T) {
	out, code := compileAndRunArm64(t, `function main(): i32 {
    print("hello arm64");
    write("no-nl");
    putchar(10);
    putchar(65);
    putchar(10);
    return 0;
}`)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	want := "hello arm64\nno-nl\nA\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// arm64 args() — materialises argv as a length-prefixed
// string[]. compileAndRunArm64 doesn't pass extra args, so
// we drive qemu-aarch64 directly with a fixed argv list and
// check len + each entry. argv[0] is implementation-defined
// (the binary path under emulation, often `/tmp/...`); we
// just check that it ends with our binary name.
func TestArm64Args(t *testing.T) {
	gcc, qemu := arm64Tooling(t)

	src := `function main(): i32 {
    var a: string[] = args();
    var i: i32 = 0;
    while (i < a.len()) {
        print(a[i]);
        i = i + 1;
    }
    return a.len();
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Monomorphise generic functions before codegen — the
	// production driver (cmd/fern) always runs this; the e2e
	// harness was missing it which only mattered once OpCallDirect
	// started consulting per-arg types for SysV register allocation
	// under the two-word string ABI.
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := runArm64Bin(qemu, binPath, "alpha", "beta", "gamma")
	out, _ := cmd.CombinedOutput()
	if got, want := cmd.ProcessState.ExitCode(), 4; got != want {
		t.Errorf("exit = %d (argc), want %d", got, want)
	}
	// argv[0] is the binary path; check just the user args.
	for _, want := range []string{"alpha\n", "beta\n", "gamma\n"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// Mirror of TestX86_64SliceMake — slice construction +
// indexing now works on arm64 for all four element strides.
func TestArm64SliceMake(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"i32 slice read", `function main(): i32 {
    var arr: i32[] = [10, 20, 30, 40, 50];
    var s: [i32] = arr[1:4];
    return s[1];
}`, 30},
		{"u8 slice read", `function main(): i32 {
    var arr: u8[] = [10, 20, 30, 40, 50];
    var s: [u8] = arr[1:4];
    return s[1] as i32;
}`, 30},
		{"i64 slice read", `function main(): i32 {
    var arr: i64[] = [(1i64 << 40), (1i64 << 41), (1i64 << 42)];
    var s: [i64] = arr[1:3];
    return (s[0] >> 41) as i32;
}`, 1},
		{"len(slice)", `function main(): i32 {
    var arr: i32[] = [1, 2, 3, 4, 5];
    var s: [i32] = arr[1:4];
    return s.len();
}`, 3},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

// arm64 random_bytes(n) — kernel CSPRNG fill. Verifies length
// matches and that the output isn't all zeros (extremely unlikely
// from getrandom + actual entropy).
func TestArm64RandomBytes(t *testing.T) {
	out, code := compileAndRunArm64(t, `function main(): i32 {
    var s: string = random_bytes(16);
    write(s);
    return s.len();
}`)
	if code != 16 {
		t.Errorf("exit = %d, want 16 (length of returned bytes)", code)
	}
	if len(out) != 16 {
		t.Errorf("stdout len = %d, want 16", len(out))
	}
	allZero := true
	for _, b := range []byte(out) {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Errorf("random_bytes returned all zeros — getrandom likely failed silently")
	}
}

// arm64 random_i32() — single CSPRNG i32 via a 4-byte getrandom
// read. Cross-backend companion to the interp / x86-64 / wasm
// random_i32 paths (issue #2747). Folds two draws into a live-
// and-varying signal: exit 7 = good, 0 = stuck-zero, 1 = the two
// draws matched (non-varying generator).
func TestArm64RandomI32(t *testing.T) {
	_, code := compileAndRunArm64(t, `function main(): i32 {
    var a: i32 = random_i32();
    var b: i32 = random_i32();
    if (a == 0) { return 0; }
    if (a == b) { return 1; }
    return 7;
}`)
	if code != 7 {
		t.Errorf("random_i32: exit = %d, want 7 (0=stuck-zero, 1=non-varying)", code)
	}
}

// arm64 s.as_bytes() — non-copying (data, len) → slice<u8> view.
// Under the two-word string ABI the receiver arrives as
// (data, len); the helper builds a slice header aliasing those
// bytes (issue #2747). Verifies the slice length and that
// indexing reads back the original bytes for both inline-sized
// ("ABC") and heap-sized ("ABCDEFGHIJ") source strings.
func TestArm64StringAsBytes(t *testing.T) {
	// 3 (len) + 65+66+67 = 201.
	if _, code := compileAndRunArm64(t, `function main(): i32 {
    var b = "ABC".as_bytes();
    return b.len() + (b[0] as i32) + (b[1] as i32) + (b[2] as i32);
}`); code != 201 {
		t.Errorf("inline as_bytes: exit = %d, want 201 (3 + 65+66+67)", code)
	}
	// 10 (len) + 'J' (74) = 84.
	if _, code := compileAndRunArm64(t, `function main(): i32 {
    var b = "ABCDEFGHIJ".as_bytes();
    return b.len() + (b[9] as i32);
}`); code != 84 {
		t.Errorf("heap as_bytes: exit = %d, want 84 (10 + 'J')", code)
	}
}

// arm64 eprint + exit. eprint(s) writes to fd 2 (stderr); exit(code)
// is a direct exit syscall and skips main's normal return path.
// Combined: write to stderr, then bail with a specific code.
func TestArm64EprintExit(t *testing.T) {
	out, code := compileAndRunArm64(t, `function main(): i32 {
    eprint("oops");
    exit(7);
    return 99;
}`)
	if code != 7 {
		t.Errorf("exit = %d, want 7 (exit(7) should not fall through to return 99)", code)
	}
	// compileAndRunArm64 captures CombinedOutput so stderr is folded in.
	want := "oops\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// arm64 TCP primitives: tcp_listen / tcp_close round-trip
// validates the socket / bind / listen / close syscall
// chain. Port 0 means "kernel-assigned ephemeral" — fast
// way to confirm the listener works without picking a free
// port. Full HTTP server e2e (handle() + auto-main +
// tcp_serve + parser/serializer composed) is a follow-up.
func TestArm64TcpListen(t *testing.T) {
	_, code := compileAndRunArm64(t, `function main(): i32 {
    var fd: i32 = tcp_listen(0);
    if (fd < 0) { return 1; }
    tcp_close(fd);
    return 42;
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (tcp_listen + tcp_close on ephemeral port)", code)
	}
}

// `now_unix_ms()` on the arm64 backend lowers to a
// `clock_gettime(CLOCK_REALTIME, &ts)` syscall (asm-generic
// 113) + a `tv_sec * 1000 + tv_nsec / 1_000_000` reduction.
// Asserts the returned ms value is in a plausible range —
// past a sentinel epoch (2023) and before the year-9999 wall.
// Closes docs/STDLIB-DESIGN-RESEARCH.md Rec §4 Phase 2.x on
// arm64 (Linux); Darwin gets caught by the pre-scan
// "not-yet-ported" guard.
func TestArm64InstantNow(t *testing.T) {
	_, code := compileAndRunArm64(t, `
import "std/time";
function main(): i32 {
    var ts: Instant = time.instant_now();
    if (ts.sec < (1700000000 as i64)) { return 1; }
    if (ts.sec > (253402300800 as i64)) { return 2; }
    return 0;
}`)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

// End-to-end arm64 HTTP handler. Compiles a program that only
// defines `function handle(req: HttpRequest, plat: Platform):
// HttpResponse` — the checker synthesises `main()` from it as
// `tcp_serve(__port_from_env("PORT", 8080), handle)`, and
// tcp_serve constructs a Platform per request before calling
// the handler. The
// resulting binary listens on the PORT env var, parses an
// HTTP/1.1 request, calls the user handler, and writes the
// serialised response back. Then this test sends two
// back-to-back requests on separate connections and asserts
// the bodies — the second one reuses the same long-lived
// process, proving handler-built allocations from the first
// request are reclaimed (by reference counting) rather than
// leaking across requests.
//
// Runs under qemu-aarch64; the binary opens a real TCP socket
// on the host's kernel (user-mode emulation forwards syscalls
// 1:1). Picks a port via Go's net.Listen("tcp", ":0") then
// closes the listener — tiny TOCTOU window before the binary
// claims it, acceptable for CI.
func TestArm64HttpHandler(t *testing.T) {
	gcc, qemu := arm64Tooling(t)

	// Pick a free port. Close the Go listener immediately so
	// the lang binary can claim it. Race window is small in
	// practice.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	src := `
import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("method=" + req.method + " path=" + req.path + " body-len=" + req.body_len().to_string());
}`

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// modload (not bare parser.Parse) so std/http + std/tcp resolve.
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Monomorphise generic functions before codegen — the
	// production driver (cmd/fern) always runs this; the e2e
	// harness was missing it which only mattered once OpCallDirect
	// started consulting per-arg types for SysV register allocation
	// under the two-word string ABI.
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}

	cmd := runArm64Bin(qemu, binPath)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	// Poll-connect until the lang binary has actually bound
	// the port (qemu startup + tcp_listen take a few hundred
	// ms). 10s deadline is generous for CI.
	deadline := time.Now().Add(10 * time.Second)
	var ready bool
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("server never bound on %s within 10s", addr)
	}

	// Two requests, two connections — the second reuses the
	// process after the first request's allocations have been
	// reclaimed by reference counting.
	cases := []struct {
		req  string
		want string
	}{
		{"GET /first HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n", "method=GET path=/first body-len=0"},
		{"POST /second HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\nhello", "method=POST path=/second body-len=5"},
	}
	for i, c := range cases {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatalf("request %d dial: %v", i, err)
		}
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write([]byte(c.req)); err != nil {
			t.Fatalf("request %d write: %v", i, err)
		}
		resp, err := io.ReadAll(conn)
		conn.Close()
		if err != nil {
			t.Fatalf("request %d read: %v", i, err)
		}
		// HTTP/1.1 response: status-line + headers + blank line + body.
		body := string(resp)
		if !strings.Contains(body, "HTTP/1.1 200") {
			t.Errorf("request %d: missing 200 status\n--- got ---\n%s", i, body)
		}
		if !strings.Contains(body, c.want) {
			t.Errorf("request %d: missing %q\n--- got ---\n%s", i, c.want, body)
		}
	}
}

// arm64-darwin baseline: native Apple Silicon macOS Mach-O
// binaries. Compiles via clang --target=arm64-apple-darwin +
// lld's Mach-O backend; the resulting binary runs natively on
// Apple Silicon Macs (no Linux container needed). Tests
// can't execute the binary here (qemu-aarch64 only emulates
// Linux), so they assert the output is a valid Mach-O 64-bit
// arm64 executable.
//
// All three syscall surfaces the runtime needs are now
// Darwin-aware: SYS_exit (1), SYS_mmap (197) in __fern_alloc,
// and the TCP/IO family (socket=97, bind=104, listen=106,
// accept=30, read=3, write=4, close=6). Each emits via
// `svc #0x80` with x16=number, and TCP/IO normalises Darwin's
// C-flag error shape into Linux-style -errno in x0 so the
// existing callers' `cmp x0, #0; blt` checks work unchanged.
func TestArm64DarwinBuilds(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not on PATH; skipping arm64-darwin cross-compile e2e")
	}
	// lld is required for Mach-O cross-compilation from Linux,
	// but on a native macOS arm64 host clang ships with ld64
	// and we don't need (or want) lld. The macOS CI runner
	// hits this branch.
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	if !native {
		if _, err := exec.LookPath("ld.lld"); err != nil {
			t.Skip("lld not on PATH; skipping arm64-darwin cross-compile e2e")
		}
	}

	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		// Plain return — exercises only SYS_exit.
		{"exit_42", `function main(): i32 { return 42; }`, 42},
		// String concat — exercises SYS_mmap via __fern_alloc.
		{"strconcat", `function main(): i32 {
    var s: string = "hello, " + "world!";
    return s.len();
}`, 13},
		// TCP listen + close — exercises socket/bind/listen/close
		// syscalls (Darwin numbers + svc #0x80 path).
		{"tcp", `function main(): i32 {
    var fd: i32 = tcp_listen(0);
    if (fd < 0) { return 1; }
    tcp_close(fd);
    return 42;
}`, 42},
		// Array push — exercises the IR's emitArrayPush
		// inline lowering (alloc + memcpy + tail store).
		// push() returns a new array; lang uses value
		// semantics so the receiver must be reassigned.
		{"arrpush", `function main(): i32 {
    var xs: i32[] = [];
    xs = xs.append(7);
    xs = xs.append(35);
    return xs[0] + xs[1];
}`, 42},
		// Stdout builtins — print(s) lowers to two write(2)s
		// (string + newline), putchar(c) to a single 1-byte
		// write. Exercises Darwin write syscall + the
		// .LLangNewline rodata entry on Mach-O.
		{"print", `function main(): i32 {
    print("hi");
    putchar(33);
    putchar(10);
    return 0;
}`, 0},
		// exit(code) — direct exit syscall; bypasses main's
		// normal return path. Verifies the user-supplied code
		// makes it through Darwin's `mov x16, #1; svc #0x80`
		// flavour of exit.
		{"exit", `function main(): i32 {
    exit(7);
    return 99;
}`, 7},
		// args() — argv reader. With no extra args passed by
		// the harness, argv contains just the binary path, so
		// argc == 1. Verifies the start-runtime prologue
		// stashed argc/argv from the kernel-delivered stack.
		{"args", `function main(): i32 {
    return (args()).len();
}`, 1},
		// stdin().read_line() — exercises the .bss buffer +
		// byte-by-byte read syscall + Option[string] result.
		// CI runs the binary with no stdin attached, so the
		// first read returns 0 (EOF) and we get None.
		{"read_line", `function main(): i32 {
    match (stdin().read_line()) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return -1;
}`, 0},
		// Map[i32, i32] — pointer-width fix exercise. The
		// Map handle now uses __store_ptr / __load_ptr (8
		// bytes on arm64) so the buf pointer round-trips
		// correctly even when macOS hands us heap addresses
		// above 4 GiB.
		{"map_i32", `
import "core/map";
import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(1, 100);
    m = m.insert(2, 200);
    return m.get_or(2, 0);
}`, 200},
		// random_bytes(n) — Darwin getentropy path
		// (chunked, 256-byte cap per call). Just verify the
		// length round-trips; can't assert content.
		{"random_bytes", `function main(): i32 {
    return (random_bytes(32)).len();
}`, 32},
		// Map[string, i32] — string keys exercise the
		// pointer-width entry-slot fix. set("world", 99)
		// writes the string pointer through __store_ptr (8
		// bytes on arm64), so lookup with the same key
		// (FNV-1a hash + byte-wise string compare) finds
		// the entry even when the heap is above 4 GiB. The
		// returned i32 value rides x0 untruncated.
		{"map_str_key", `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("hello", 42);
    m = m.insert("world", 99);
    return m.get_or("world", 0);
}`, 99},
		// Map[i32, string] — string values. get_or returns
		// the entry's pointer-width V slot via __load_ptr;
		// the i32-typed return rides x0 as a full 64-bit
		// pointer, and len(s) reads s's length prefix at
		// the correct (high-bit-preserved) address.
		{"map_str_val", `
import "core/map";
import "std/i32";
import "std/string";
function main(): i32 {
    var m: Map[i32, string] = map_new(4);
    m = m.insert(1, "abc");
    m = m.insert(2, "abcdef");
    return (m.get_or(2, "")).len();
}`, 6},
		// Map[string, string] — both key and value are
		// pointer-width. End-to-end check that the entry
		// stride doubled to 2*ptr_width on arm64 (16 bytes)
		// without breaking the bucket arithmetic.
		{"map_str_str", `
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[string, string] = map_new(4);
    m = m.insert("k1", "ab");
    m = m.insert("k2", "abcde");
    return (m.get_or("k2", "")).len();
}`, 5},
		// Iteration over Map[string, i32] via has_next /
		// key / value — accumulates the sum of all values.
		// Exercises __mapiter_entry_addr's stride math and
		// the pointer-width key load (even though we don't
		// inspect keys here, the iterator's address math
		// must use the same entryStride or it'd walk off).
		{"map_str_iter", `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 10);
    m = m.insert("b", 20);
    m = m.insert("c", 30);
    var it: MapIter[string, i32] = m.iter();
    var sum: i32 = 0;
    while (it.has_next()) {
        sum = sum + it.value();
        it.advance();
    }
    return sum;
}`, 60},
		// Delete over a string-keyed map — verifies the
		// swap-with-last path correctly uses __load_ptr /
		// __store_ptr on the moved entry's K/V slots. After
		// removing "b" and "c", get_or("a") still finds the
		// remaining entry.
		{"map_str_delete", `
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    m = m.without("b").0;
    m = m.without("c").0;
    return m.get_or("a", 0) * 10 + m.len();
}`, 11},
		// Option[string] payload — the Some(s) variant now
		// stores `s` in a pointer-width payload slot (8
		// bytes on arm64), so the high 32 bits of macOS
		// heap pointers survive the match's payload-load.
		// `len(s)` reads s's length prefix at [s_ptr - 4],
		// which would trap on a truncated pointer.
		{"option_str", `function get_msg(): Option[string] {
    return Some("hi there");
}
function main(): i32 {
    match (get_msg()) {
        Some(s) => { return s.len(); },
        None => { return 0; }
    }
    return -1;
}`, 8},
		// User-defined enum with a pointer-typed payload —
		// same widening as Option[string] but exercises the
		// full payloadLayout / payloadStore / payloadLoad
		// triple for a non-prelude variant.
		{"enum_str", `enum Msg {
    Text(string),
    Empty
}
function build(): Msg {
    return Text("payload-string");
}
function main(): i32 {
    match (build()) {
        Text(s) => { return s.len(); },
        Empty => { return 0; }
    }
    return -1;
}`, 14},
		// Struct with a string field — exercises ptrW-aware
		// field offsets and stores. `name` lands at offset
		// 8 (aligned to 8) on arm64, sandwiched between two
		// i32 fields, and round-trips a real heap pointer.
		{"struct_str_field", `struct Person {
    age: i32,
    name: string,
    weight: i32
}
function main(): i32 {
    var p: Person = Person { age: 30, name: "Claude", weight: 100 };
    return p.name.len() + p.age + p.weight;
}`, 136},
		// Array of strings — array literal stride + element
		// store widened to 8 bytes for pointer-typed elems
		// on arm64; indexing via __arr_idx_8 picks the
		// matching `lsl #3` address compute.
		{"string_arr", `function main(): i32 {
    var xs: string[] = ["alpha", "beta", "gamma"];
    return xs[0].len() + xs[1].len() + xs[2].len() + xs.len();
}`, 17},
		// Map[string, i32].keys() — the snapshot array is
		// now ptrW-aware (destStride=8 on arm64 for pointer
		// K), so iterating the keys() result and calling
		// len() on each returns valid lengths instead of
		// segfaulting on truncated pointers.
		{"map_keys_str", `
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("alpha", 1);
    m = m.insert("beta", 2);
    m = m.insert("gamma", 3);
    var ks: string[] = m.keys();
    var i: i32 = 0;
    var total: i32 = 0;
    while (i < ks.len()) {
        total = total + ks[i].len();
        i = i + 1;
    }
    return total;
}`, 14},
		// Map[i32, string].values() — same shape on the V
		// side. valKind is now tracked at buf+12 so
		// __map_values_impl picks destStride correctly per-
		// instance without per-V monomorphisation.
		{"map_values_str", `
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[i32, string] = map_new(4);
    m = m.insert(1, "one");
    m = m.insert(2, "two");
    m = m.insert(3, "three");
    var vs: string[] = m.values();
    var i: i32 = 0;
    var total: i32 = 0;
    while (i < vs.len()) {
        total = total + vs[i].len();
        i = i + 1;
    }
    return total;
}`, 11},
		// Probe for the arm64-darwin heap-address truncation
		// bug (BACKEND-PARITY.md "Known limitations"). Map
		// values are HEAP-allocated strings (built via concat
		// at runtime), NOT .rodata literals. On macOS the
		// mmap address hint is ignored and the heap lands at
		// a high (>4 GiB) address. The prelude's Map runtime
		// previously declared pointer locals + params as
		// `i32`, truncating the high 32 bits of the round-
		// tripped pointer; the fix in this PR migrates the
		// V-side of every Map helper (and most K-side cases)
		// to `usize` so the full 8-byte address survives. The
		// previous `t.Skip` on Darwin has been removed —
		// macOS CI now exercises this case alongside Linux.
		{"map_heap_value_probe", `
import "core/map";
import "std/string";
function main(): i32 {
    var m: Map[i32, string] = map_new(4);
    var v1: string = "alp" + "ha";
    var v2: string = "be" + "ta";
    m = m.insert(1, v1);
    m = m.insert(2, v2);
    return (m.get_or(1, "")).len() + (m.get_or(2, "")).len();
}`, 9},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// modload.LoadSource (not bare parser.Parse) so the
			// programs' std/ + core/ imports resolve under no-prelude.
			prog, _, err := modload.LoadSource(c.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if err := constfold.Fold(prog); err != nil {
				t.Fatalf("constfold: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			asm, err := arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{Darwin: true})
			if err != nil {
				t.Fatalf("emit: %v", err)
			}

			dir := t.TempDir()
			asmPath := filepath.Join(dir, "prog.s")
			binPath := filepath.Join(dir, "prog")
			if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
				t.Fatalf("write asm: %v", err)
			}
			// On macOS arm64 native, the default clang IS the
			// arm64-apple-darwin clang and ld64 is its default
			// linker; the cross-compile flags would force an
			// unnecessary lld dependency. Cross from Linux
			// requires lld because the host's clang defaults
			// to ELF.
			var args []string
			if native {
				// Newer ld64 (Xcode 16+ on macOS Sequoia/
				// Tahoe) refuses dynamic executables without
				// libSystem.dylib linked. `-nostdlib`
				// suppresses crt0/libc startup; `-lSystem`
				// re-adds just the dyld-stub linkage. See
				// cmd/fern/main.go's linkDarwin for matching
				// production-driver behaviour.
				args = []string{"-nostdlib", "-lSystem", asmPath, "-o", binPath}
			} else {
				args = []string{
					"--target=arm64-apple-darwin",
					"-fuse-ld=lld",
					"-nostdlib",
					"-Wl,-arch,arm64",
					asmPath,
					"-o", binPath,
				}
			}
			if out, err := exec.Command("clang", args...).CombinedOutput(); err != nil {
				t.Fatalf("clang Mach-O: %v\n%s\n--- asm ---\n%s", err, out, asm)
			}
			out, _ := exec.Command("file", binPath).CombinedOutput()
			// Linux `file` reports "Mach-O 64-bit arm64 executable";
			// macOS `file` reports "Mach-O 64-bit executable arm64"
			// (word order differs). Both are fine — check the three
			// pieces separately.
			s := string(out)
			if !strings.Contains(s, "Mach-O 64-bit") || !strings.Contains(s, "arm64") || !strings.Contains(s, "executable") {
				t.Errorf("not a Mach-O arm64 executable: %s\n%s", out, asm)
			}
			// Cross-compilation hosts can't run the Mach-O —
			// qemu-aarch64 only speaks the Linux ABI. The
			// macos-14 CI runner hits this and verifies the
			// runtime actually behaves correctly.
			if native {
				cmd := exec.Command(binPath)
				_, _ = cmd.CombinedOutput()
				if got := cmd.ProcessState.ExitCode(); got != c.wantExit {
					t.Errorf("native exit = %d, want %d\n--- asm ---\n%s", got, c.wantExit, asm)
				}
			}
		})
	}
}

// arm64 control flow: while loop, if/else, comparison ops.
// Verifies OpBlock / OpLoop / OpIf / OpEnd / OpBr / OpBrIf
// scope tracking + the cbz / cbnz branch idioms.
func TestArm64ControlFlow(t *testing.T) {
	for _, c := range []struct {
		src  string
		want int
	}{
		{`function main(): i32 {
    var sum: i32 = 0;
    var i: i32 = 1;
    while (i <= 10) {
        sum = sum + i;
        i = i + 1;
    }
    return sum;
}`, 55},
		{`function classify(n: i32): i32 {
    if (n < 0) { return 1; }
    if (n == 0) { return 2; }
    return 3;
}
function main(): i32 {
    var a: i32 = classify(0 - 5);
    var b: i32 = classify(0);
    var c: i32 = classify(7);
    return a * 100 + b * 10 + c;
}`, 123},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%q: exit = %d, want %d", c.src, code, c.want)
		}
	}
}

// Tail-call optimisation. arm64 now wires `ir.TailCallOptimize`
// (backported from PR #274's x86-64 first-consumer wire-up).
// Two assertions:
//
//  1. The asm has exactly one `bl sum_to` (the kick-off from
//     `main`). Without TCO the recursive site would still
//     emit `bl <self>`; with TCO that site becomes
//     `b .Lloop_top`.
//  2. Recursion that would overflow the qemu-aarch64 default
//     stack returns cleanly with the right value.
func TestArm64TailCall(t *testing.T) {
	gcc, qemu := arm64Tooling(t)

	src := `function sum_to(n: i32, acc: i32): i32 {
    if (n == 0) { return acc; }
    return sum_to(n - 1, acc + n);
}
function main(): i32 {
    return sum_to(100000, 0);
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Monomorphise generic functions before codegen — the
	// production driver (cmd/fern) always runs this; the e2e
	// harness was missing it which only mattered once OpCallDirect
	// started consulting per-arg types for SysV register allocation
	// under the two-word string ABI.
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := strings.Count(asm, "bl sum_to"); got != 1 {
		t.Errorf("`bl sum_to` appearances = %d, want 1 (only from main); TCO didn't fire", got)
	}

	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := runArm64Bin(qemu, binPath)
	_, _ = cmd.CombinedOutput()
	// 5,000,050,000 → i32 (705,082,704) → exit code (mod 256) = 80.
	if got := cmd.ProcessState.ExitCode(); got != 80 {
		t.Errorf("sum_to(100000, 0) → exit = %d, want 80", got)
	}
}

// Closure factory pattern: `var f = makeAdder(7); f(35)`. The
// IR's Defunctionalise pass rewrites `f(35)` into a direct call
// to the hoisted `add` with env_ptr pulled out of the closure
// pair at offset +ptrW (=8 on native).
// Mirror of TestX86_64ClosureAliasing — closure values flowing
// through intermediate variables get defunctionalized to direct
// calls instead of crashing on call-of-pair-pointer in the
// backend's OpCallIndirect.
// Mirror of TestX86_64ClosureChainNoAlloc: closure-pair elision
// for chained no-capture aliases must work on arm64 too.
func TestArm64ClosureChainNoAlloc(t *testing.T) {
	_, code := compileAndRunArm64(t, `function main(): i32 {
    function answer(): i32 { return 7; }
    var f = answer;
    var x: i32 = f();
    return x;
}`)
	if code != 7 {
		t.Errorf("exit = %d, want 7 (chained no-capture alias returns 7)", code)
	}
}

// Mirror of TestX86_64ResultPairForm.
func TestArm64ResultPairForm(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"Ok path", `function divide(a: i32, b: i32): Result[i32, i32] {
    if (b == 0) { return Err(1); }
    return Ok(a / b);
}
function main(): i32 {
    match (divide(10, 2)) {
        Ok(v)  => { return v; },
        Err(_) => { return 99; }
    }
}`, 5},
		{"Err path", `function divide(a: i32, b: i32): Result[i32, i32] {
    if (b == 0) { return Err(7); }
    return Ok(a / b);
}
function main(): i32 {
    match (divide(10, 0)) {
        Ok(_)  => { return 99; },
        Err(e) => { return e; }
    }
}`, 7},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

// Mirror of TestX86_64PointerPayloadPairForm — pointer-shaped
// payloads through pair-form on arm64. 16-byte heap box,
// 8-byte payload store at offset 8, 8-byte consumer read.
func TestArm64PointerPayloadPairForm(t *testing.T) {
	src := `function pick(b: boolean): Option[string] {
    if (b) { return Some("hello world"); }
    return None;
}
function main(): i32 {
    match (pick(true)) {
        Some(s) => { return s.len(); },
        None    => { return -1; }
    }
}`
	_, exit := compileAndRunArm64(t, src)
	if exit != 11 {
		t.Errorf("exit = %d, want 11 (len(\"hello world\"))", exit)
	}
}

// Mirror of TestX86_64UseCallback: closure-pair ABI uniforming
// on arm64. `use` + function-value-as-param now works.
func TestArm64UseCallback(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"use with no captures", `function tryThing(cb: (i32) => i32): i32 {
    return cb(42);
}
function main(): i32 {
    use n <- tryThing();
    return n;
}`, 42},
		{"use with capture", `function each(items: i32[], cb: (i32) => i32): i32 {
    return cb(items[0]);
}
function main(): i32 {
    var n: i32 = 10;
    function addN(x: i32): i32 { return x + n; }
    return each([5], addN);
}`, 15},
		{"top-level fn passed as callback", `function step(x: i32): i32 { return x + 1; }
function tryThing(cb: (i32) => i32): i32 {
    return cb(42);
}
function main(): i32 {
    return tryThing(step);
}`, 43},
		{"generic callee with use inference", `function each[T](items: T[], cb: (T) => i32): i32 {
    return cb(items[0]);
}
function main(): i32 {
    var nums: i32[] = [10, 20, 30];
    use n <- each(nums);
    return n + 1;
}`, 11},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

func TestArm64ClosureAliasing(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"single-alias", `function main(): i32 {
    function answer(): i32 { return 42; }
    var f = answer;
    return f();
}`, 42},
		{"single-alias-with-arg", `function main(): i32 {
    function double(x: i32): i32 { return x * 2; }
    var f = double;
    return f(21);
}`, 42},
		{"multi-hop-alias", `function main(): i32 {
    function answer(): i32 { return 17; }
    var a = answer;
    var b = a;
    var c = b;
    return c();
}`, 17},
		{"aliased-and-used-twice", `function main(): i32 {
    function plus_one(x: i32): i32 { return x + 1; }
    var f = plus_one;
    return f(20) + f(20);
}`, 42},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

func TestArm64ClosureFactory(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var f = makeAdder(7);
    return f(35);
}`
	if _, code := compileAndRunArm64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// Two closures over different captured values must not share
// state — separate env blocks per MakeClosure.
func TestArm64ClosureMultipleInstances(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function main(): i32 {
    var add5 = makeAdder(5);
    var add10 = makeAdder(10);
    return add5(1) + add10(1);
}`
	// (5+1) + (10+1) = 17
	if _, code := compileAndRunArm64(t, src); code != 17 {
		t.Errorf("got %d, want 17", code)
	}
}

// Direct nested-function call (the ElideClosurePair case): the
// slot writer IS OpMakeClosure, so elide fires and the closure
// pair allocation collapses to just an env_ptr in the slot.
// Exercises the OpMakeEnv path.
func TestArm64ClosureCapturesParamAndVar(t *testing.T) {
	src := `function outer(seed: i32): i32 {
    var bonus: i32 = 100;
    function inner(x: i32): i32 { return x + seed + bonus; }
    return inner(2);
}
function main(): i32 { return outer(40); }`
	// 2 + 40 + 100 = 142
	if _, code := compileAndRunArm64(t, src); code != 142 {
		t.Errorf("got %d, want 142", code)
	}
}

// String capture — pointer-shaped capture takes a full ptr-width
// (8-byte) slot in the env block. Verifies arm64CaptureSlotSize
// routes through `str x1, ...` rather than the 4-byte `str w1`
// truncation path.
func TestArm64ClosureCapturesString(t *testing.T) {
	src := `function outer(s: string): i32 {
    function inner(): i32 { return s.len(); }
    return inner();
}
function main(): i32 { return outer("hello"); }`
	if _, code := compileAndRunArm64(t, src); code != 5 {
		t.Errorf("got %d, want 5 (len(\"hello\") via captured string)", code)
	}
}

// Closure RETURNS a captured string under the two-word `(data,
// len)` ABI. The OpCallClosureDirect emit must push BOTH x0
// (data) and x1 (len) post-call so the subsequent OpReturn pops
// the full pair. Regression for the bug where only x0 was
// pushed, leaving the caller's len-popping `ldr x1, [sp]` to
// re-read the data half and `ldr x0, [sp]` to read an unrelated
// stale stack slot.
func TestArm64ClosureReturnsCapturedString(t *testing.T) {
	src := `function outer(s: string): string {
    function inner(): string { return s; }
    return inner();
}
function main(): i32 {
    var got = outer("hello");
    if (got == "hello") { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got %d, want 0 (closure returning captured string)", code)
	}
}

// Method call on a captured string from inside an anonymous
// lambda. Closureconv hoists Lambda → MakeClosure at IR-lower
// time, but treeshake runs FIRST — so without a Lambda case
// in `walkExpr`, the lambda body is invisible to liveness
// analysis and `__method_string_trim` (only reachable through
// the lambda) gets pruned. Link then fires "undefined
// reference to __method_string_trim".
func TestArm64LambdaCallsMethodOnCapturedString(t *testing.T) {
	src := `
import "std/string";
function main(): i32 {
    var s: string = "  hi  ";
    var f = function (): string { return s.trim().to_owned(); };
    var got = f();
    if (got == "hi") { return 0; }
    return 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got %d, want 0 (s.trim() inside lambda body)", code)
	}
}

// Two anonymous lambdas hoisted in the same converter session.
// Both arrive at closureconv with origin name "lambda"; the
// freshName counter used to key off `len(c.hoisted)` so both
// hoists produced `__closure_lambda_1` and the assembler died
// with "symbol already defined". Per-origin counting fixes it.
func TestArm64NestedLambdaUniqueNames(t *testing.T) {
	src := `function main(): i32 {
    var outer = function (): i32 {
        var inner = function (): i32 {
            var x = 21;
            return x * 2;
        };
        var y = inner();
        return y;
    };
    return outer();
}`
	if _, code := compileAndRunArm64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// Anonymous lambda with body-local vars. Regression for the
// "var X has no slot (compiler bug)" the IR panicked with
// when checker stored the body's Var nodes against a synthetic
// FuncDecl pointer that closureconv never re-keyed onto the
// hoisted lambda. Cover takes a scalar local (`sq`) and a
// string-typed local (`tag`) — the latter exercises the
// pointer-width slot the original failure surfaced inside
// closures that captured strings.
func TestArm64LambdaWithBodyLocals(t *testing.T) {
	src := `function main(): i32 {
    var greet = "hi";
    var f = function (n: i32): i32 {
        var sq = n * n;
        var tag = greet + "!";
        print(tag);
        return sq;
    };
    return f(6);
}`
	stdout, code := compileAndRunArm64(t, src)
	if code != 36 {
		t.Errorf("got exit %d, want 36", code)
	}
	if stdout != "hi!\n" {
		t.Errorf("got stdout %q, want %q", stdout, "hi!\n")
	}
}

// Multi-capture closure: two i32 captures laid out at offsets 0
// and 4 in the env block. Verifies the running-offset
// arithmetic in emitMakeClosureOrEnv.
func TestArm64ClosureMultiCapture(t *testing.T) {
	src := `function make2(a: i32, b: i32): (i32) => i32 {
    function f(x: i32): i32 { return a + b + x; }
    return f;
}
function main(): i32 {
    var h = make2(10, 20);
    return h(12);
}`
	if _, code := compileAndRunArm64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// Pointer + scalar captures mixed in one closure — pointer slot
// is 8 bytes, scalar slot is 4 bytes. Exercises mixed-width
// offset arithmetic.
func TestArm64ClosureCapturesMixedPointers(t *testing.T) {
	src := `function outer(s: string, n: i32): i32 {
    function inner(): i32 { return s.len() + n; }
    return inner();
}
function main(): i32 { return outer("hi", 40); }`
	// len("hi") + 40 = 42
	if _, code := compileAndRunArm64(t, src); code != 42 {
		t.Errorf("got %d, want 42", code)
	}
}

// Mirror of TestWASMClosureCapturesTuple — `targetTupleType`
// now recognises `*ast.CaptureRef` so `t.0` / `t.1` inside a
// closure body resolves through the tuple-offset path instead
// of falling through to the struct path and erroring with
// `field access on unresolved struct ""` at IR-emit time.
func TestArm64ClosureCapturesTuple(t *testing.T) {
	src := `function build(): () => i64 {
    var t: (i64, i64) = (1000000000000i64, 2000000000000i64);
    function read(): i64 { return t.0 + t.1; }
    return read;
}
function main(): i32 {
    var f = build();
    if (f() != 3000000000000i64) { return 1; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got %d, want 0 (closure captures (i64, i64))", code)
	}
}

// Mirror of TestWASMLenOfClosureReturningString — `exprType`
// of a *ast.Call now resolves function-typed locals so the
// `len()` dispatch picks `OpStrLen` over the array-shape
// `[ptr-4]; load`.
func TestArm64LenOfClosureReturningString(t *testing.T) {
	src := `function makeReader(): () => string {
    function build(): string { return "hello"; }
    return build;
}
function main(): i32 {
    var f = makeReader();
    return (f()).len();
}`
	if _, code := compileAndRunArm64(t, src); code != 5 {
		t.Errorf("got %d, want 5", code)
	}
}

// Mirror of TestWASMClosureFStringCapture — closureconv now
// recurses through FString.Desugared so captured-name idents
// inside `f"…{cap}…"` get rewritten to CaptureRef nodes.
func TestArm64ClosureFStringCapture(t *testing.T) {
	src := `
import "std/string";
function makeNamer(name: string): () => string {
    function build(): string { return f"hello, {name}!"; }
    return build;
}
function main(): i32 {
    var f = makeNamer("world");
    if (f() != "hello, world!") { return 1; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got %d, want 0", code)
	}
}

// Mirror of TestWASMMutableCapturedVar — assignment to a
// captured outer-scope variable now stores into the env block.
func TestArm64MutableCapturedVar(t *testing.T) {
	src := `function makeCounter(): () => i32 {
    var count: i32 = 0;
    function tick(): i32 {
        count = count + 1;
        return count;
    }
    return tick;
}
function main(): i32 {
    var c = makeCounter();
    var a: i32 = c();
    var b: i32 = c();
    var d: i32 = c();
    return a + b + d;
}`
	if _, code := compileAndRunArm64(t, src); code != 6 {
		t.Errorf("got %d, want 6 (counter increments in env)", code)
	}
}

// Mirror of TestWASMClosureCallsCapturedFn — the IR's call()
// path now handles `*ast.CaptureRef` callees so calling a
// captured function value inside a nested closure works.
func TestArm64ClosureCallsCapturedFn(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
    function add(x: i32): i32 { return x + n; }
    return add;
}
function makeApplier(f: (i32) => i32): (i32) => i32 {
    function apply(x: i32): i32 { return f(x) + 1; }
    return apply;
}
function main(): i32 {
    var a = makeAdder(10);
    var ap = makeApplier(a);
    return ap(5);
}`
	if _, code := compileAndRunArm64(t, src); code != 16 {
		t.Errorf("got %d, want 16", code)
	}
}

// Mirror of TestWASMClosureRecursiveSelfCall — closureconv now
// rewrites a recursive self-reference inside the hoisted body
// from the original local name (`fact`) to the hoisted name
// (`__closure_fact_1`) and forwards `__env` through so the
// recursive callee gets the same captured-state block.
func TestArm64ClosureRecursiveSelfCall(t *testing.T) {
	src := `function makeFact(): (i32) => i32 {
    function fact(n: i32): i32 {
        if (n <= 1) { return 1; }
        return n * fact(n - 1);
    }
    return fact;
}
function main(): i32 {
    var f = makeFact();
    return f(5);
}`
	if _, code := compileAndRunArm64(t, src); code != 120 {
		t.Errorf("got %d, want 120 (5!)", code)
	}
}

// i32 ↔ i64 conversion. arm64 lowers OpExtendI32S via `sxtw`,
// OpExtendI32U + OpWrapI64 via `mov w0, w0` (the 32-bit reg
// form implicitly zero-extends the high half on AArch64).
// OpConstI64 uses `ldr x0, =N` with a literal-pool entry.
func TestArm64I32I64Convert(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"i32_to_i64_roundtrip", `function main(): i32 {
			var a: i32 = 7;
			var b: i64 = a as i64;
			var c: i32 = b as i32;
			return c + 35;
		}`, 42},
		{"wrap_drops_high_half", `function main(): i32 {
			var big: i64 = 4294967300i64;
			return (big as i32);
		}`, 4},
		{"sxtw_preserves_sign", `function main(): i32 {
			var neg: i32 = 0 - 1;
			var ext: i64 = neg as i64;
			if (ext == 0 - 1i64) { return 7; }
			return 99;
		}`, 7},
		{"i64_arith_roundtrip", `function main(): i32 {
			var a: i64 = 1000000000000i64;
			var b: i64 = a + 42i64;
			return (b - 1000000000000i64) as i32;
		}`, 42},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

// Mirrors TestX86_64{Defer,Switch,FStringInterpolation,Generic,
// Tuple,ForEach,IfLet} — backfills the BACKEND-PARITY.md test
// gaps on arm64.
func TestArm64Defer(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"defer fires after return value computed", `function inner(): i32 {
    var x: i32 = 1;
    defer x = 99;
    x = 2;
    return x;
}
function main(): i32 { return inner(); }`, 2},
		{"multiple defers run LIFO", `function check(c: Cell[i32]): i32 {
    c.set(1);
    defer c.set(10);
    defer c.set(20);
    return c.get();
}
function main(): i32 {
    var c: Cell[i32] = cell_new(0);
    check(c);
    return c.get();
}`, 10},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestArm64FStringInterpolation(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"interpolated i32", `
import "std/i32";
function main(): i32 {
    var n: i32 = 42;
    var s: string = f"n is {n}";
    return s.len();
}`, 7},
		{"interpolated string", `
import "std/i32";
function main(): i32 {
    var who: string = "world";
    var s: string = f"hello, {who}!";
    return s.len();
}`, 13},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestArm64Generic(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"generic identity", `function id[T](x: T): T { return x; }
function main(): i32 { return id(42); }`, 42},
		{"generic with two type params", `function pick[A, B](a: A, b: B, take_first: boolean): A {
    return a;
}
function main(): i32 { return pick(7, "hi", true); }`, 7},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestArm64Tuple(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"tuple destructure", `function pair(): (i32, i32) { return (10, 20); }
function main(): i32 {
    let (a, b) = pair();
    return a + b;
}`, 30},
		{"heterogeneous tuple element access", `function main(): i32 {
    var t: (i32, string, i32) = (1, "two", 3);
    return t.0 + t.2;
}`, 4},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestArm64ForEach(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"sum array", `function main(): i32 {
    var sum: i32 = 0;
    for n in [1, 2, 3, 4, 5] { sum = sum + n; }
    return sum;
}`, 15},
		{"break exits the loop", `function main(): i32 {
    var found: i32 = -1;
    for n in [10, 20, 30, 40] {
        if (n == 30) { found = n; break; }
    }
    return found;
}`, 30},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestArm64IfLet(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"Some matches", `function main(): i32 {
    var x: Option[i32] = Some(42);
    if let Some(v) = x { return v; }
    return 99;
}`, 42},
		{"None falls through", `function main(): i32 {
    var x: Option[i32] = None;
    if let Some(v) = x { return v; }
    return 99;
}`, 99},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

// Mirror of TestX86_64Usize. arm64 + arm64-darwin are the
// targets that actually NEED usize — Linux qemu has a low heap
// (< 4 GiB) so 32-bit truncation passes by coincidence; macOS's
// high heap is the real test bed once the prelude pointer
// locals migrate to usize.
// Mirror of TestX86_64UsizeAutowiden.
func TestArm64UsizeAutowiden(t *testing.T) {
	src := `function offset_compute(base: usize, idx: i32, stride: i32): usize {
    return base + idx * stride;
}
function main(): i32 {
    var heap_ptr: usize = 4294967296 as usize;
    var elem: usize = offset_compute(heap_ptr, 4, 8);
    return (elem as i32);
}`
	_, code := compileAndRunArm64(t, src)
	if code != 32 {
		t.Errorf("got %d, want 32 (low 32 bits of 0x100000020)", code)
	}
}

// TestArm64UsizeDivRem — parity mirror of TestX86_64UsizeDivRem. arm64
// already resolves WidthPtr to the 64-bit register form via regForWidth,
// so this guards against a regression and documents the cross-backend
// contract. See docs/ADVERSARIAL-REVIEW-2026-06.md (B1).
func TestArm64UsizeDivRem(t *testing.T) {
	src := `function main(): i32 {
    var x: usize = 5000000000 as usize;
    var q: usize = x / 3;
    var r: usize = x % 3;
    if ((q as i32) != 1666666666) { return 1; }
    if ((r as i32) != 2) { return 2; }
    return 7;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 7 {
		t.Errorf("got %d, want 7 (usize div/rem truncated to 32 bits?)", code)
	}
}

// TestArm64LargeStringLiteral — a string literal longer than 64 KiB must
// compile: its byte length no longer fits `mov w0, #imm16`, so OpConstStr
// must fall back to the literal-pool form. Regression for B3 in
// docs/ADVERSARIAL-REVIEW-2026-06.md (the assembler would reject the
// over-wide `mov` before the fix).
func TestArm64LargeStringLiteral(t *testing.T) {
	const n = 70000 // > 0xffff
	src := fmt.Sprintf("function main(): i32 {\n    var s: string = %q;\n    if (s.len() == %d) { return 7; }\n    return 1;\n}", strings.Repeat("a", n), n)
	_, code := compileAndRunArm64(t, src)
	if code != 7 {
		t.Errorf("got exit %d, want 7 (>64KiB literal: assembled and len()==%d?)", code, n)
	}
}

// TestArm64FloatToUsize — parity mirror of TestX86_64FloatToUsize for the
// shared-IR B2 fix. See docs/ADVERSARIAL-REVIEW-2026-06.md.
func TestArm64FloatToUsize(t *testing.T) {
	src := `function main(): i32 {
    var f: f64 = 5000000000.0;
    var u: usize = f as usize;
    if (u == 5000000000 as usize) { return 7; }
    return 1;
}`
	_, code := compileAndRunArm64(t, src)
	if code != 7 {
		t.Errorf("got %d, want 7 (f64->usize truncated to 32 bits?)", code)
	}
}

// Mirror of TestX86_64WideScalarMap. Native arm64 (Linux qemu)
// shares the slot-wider-than-declared-type coincidence with
// x86-64 — i64 / f64 / u64 keys + values flow through the
// `(m: i32, k: i32, v: i32)` prelude signatures without
// truncation.
func TestArm64WideScalarMap(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"Map[i64, i32]", `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(4);
    m = m.insert(1i64, 100);
    return m.get_or(1i64, 0);
}`, 100},
		{"Map[i32, f64]", `
import "core/map";
function main(): i32 {
    var m: Map[i32, f64] = map_new(4);
    m = m.insert(1, 3.14);
    return m.get_or(1, 0.0) as i32;
}`, 3},
		{"Map[i64, string]", `
import "core/map";
function main(): i32 {
    var m: Map[i64, string] = map_new(4);
    m = m.insert(1i64, "hello");
    return (m.get_or(1i64, "")).len();
}`, 5},
		{"Map[string, i64]", `
import "core/map";
function main(): i32 {
    var m: Map[string, i64] = map_new(4);
    m = m.insert("hello", 42i64);
    return m.get_or("hello", 0i64) as i32;
}`, 42},
		{"Map[u64, i32]", `
import "core/map";
function main(): i32 {
    var m: Map[u64, i32] = map_new(4);
    m = m.insert(1u64, 100);
    return m.get_or(1u64, 0);
}`, 100},
		{"distinct high-bit i64 keys", `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(8);
    var k1: i64 = 0i64;
    var k2: i64 = 1i64 << 33i64;
    m = m.insert(k1, 1);
    m = m.insert(k2, 2);
    var v1: i32 = m.get_or(k1, 99);
    var v2: i32 = m.get_or(k2, 99);
    return v1 + v2;
}`, 3},
		// m.keys() on Map[i64, _] needs to materialise an i64[]
		// snapshot, not the i32[] truncation the lang-level
		// `__map_keys_impl` would produce with its hard-coded
		// 4-byte destStride. The IR's emitWideMapKeys walks the
		// entries and memcpy's the raw 8-byte K slot into the
		// result. Without it, every key gets its upper 32 bits
		// dropped — distinct high-bit keys collide into the same
		// snapshot value.
		{"keys() preserves 8-byte values", `
import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(4);
    m = m.insert(1i64, 10);
    m = m.insert(1000000000000i64, 20);
    var keys: i64[] = m.keys();
    if (keys.len() != 2) { return 1; }
    if (keys[0] != 1i64 && keys[0] != 1000000000000i64) { return 2; }
    if (keys[1] != 1i64 && keys[1] != 1000000000000i64) { return 3; }
    if (keys[0] == keys[1]) { return 4; }
    return 0;
}`, 0},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

func TestArm64Usize(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"basic usize round-trip", `function main(): i32 {
    var x: usize = 42;
    return x as i32;
}`, 42},
		{"usize arithmetic", `function main(): i32 {
    var a: usize = 10;
    var b: usize = 32;
    return (a + b) as i32;
}`, 42},
		{"usize as fn param + return", `function dbl(x: usize): usize { return x + x; }
function main(): i32 {
    var n: usize = 21;
    return dbl(n) as i32;
}`, 42},
		{"large value survives on native (> 32 bits)", `function main(): i32 {
    var big: usize = 4294967301 as usize;
    var rt: i64 = big as i64;
    if ((rt >> 32) > 0i64) { return 42; }
    return 1;
}`, 42},
		{"string ptr round-trip through usize", `function main(): i32 {
    var s: string = "hello, " + "world";
    var ptr: usize = s as usize;
    var s2: string = ptr as string;
    return s2.len();
}`, 12},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d", c.name, code, c.want)
		}
	}
}

// compileArm64InDir builds `src` and runs the resulting binary
// in a fresh temp dir seeded with `seed` files (path → content).
// Returns stdout, exit code, AND the dir so callers can read
// back files the program created. Mirrors wasm's runWasmInDir.
func compileArm64InDir(t *testing.T, src string, seed map[string]string) (stdout string, exitCode int, dir string) {
	t.Helper()
	gcc, qemu := arm64Tooling(t)
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Monomorphise generic functions before codegen — the
	// production driver (cmd/fern) always runs this; the e2e
	// harness was missing it which only mattered once OpCallDirect
	// started consulting per-arg types for SysV register allocation
	// under the two-word string ABI.
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir = t.TempDir()
	for name, content := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	cmd := runArm64Bin(qemu, binPath)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode(), dir
}

// read_file returns Ok(content) for a present file. Mirrors
// TestWASMReadFileOk's shape.
func TestArm64ReadFileOk(t *testing.T) {
	src := `function main(): i32 {
    match (read_file("greeting.txt")) {
        Ok(s) => { return s.len(); },
        Err(_) => { return 0 - 1; }
    }
    return 0 - 2;
}`
	_, code, _ := compileArm64InDir(t, src, map[string]string{
		"greeting.txt": "hello, file\n",
	})
	if code != 12 {
		t.Errorf("got %d, want 12 (len of \"hello, file\\n\"); error path or missing read", code)
	}
}

// Missing files surface as `IoError.NotFound(path)`. The path
// the caller passed must round-trip through the variant payload.
func TestArm64ReadFileNotFound(t *testing.T) {
	src := `function main(): i32 {
    match (read_file("does_not_exist.txt")) {
        Ok(_) => { return 0; },
        Err(err) => {
            match (err) {
                NotFound(p) => { return p.len(); },
                _ => { return 99; }
            }
        }
    }
    return 0 - 1;
}`
	_, code, _ := compileArm64InDir(t, src, nil)
	// len("does_not_exist.txt") = 18
	if code != 18 {
		t.Errorf("got %d, want 18 (len of missing-file path via NotFound payload)", code)
	}
}

// write_file truncates the target and writes `content`. Verify
// by reading the file back from the host side after the program
// returns.
func TestArm64WriteFileOk(t *testing.T) {
	src := `function main(): i32 {
    match (write_file("out.txt", "wrote it\n")) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return 0 - 1;
}`
	_, code, dir := compileArm64InDir(t, src, nil)
	if code != 0 {
		t.Errorf("write_file exit = %d, want 0 (None path)", code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "wrote it\n" {
		t.Errorf("got %q, want %q", got, "wrote it\n")
	}
}

// Round-trip: write a file then read it back; len reports the
// expected byte count. Both helpers in one program.
func TestArm64ReadWriteFileRoundtrip(t *testing.T) {
	src := `function main(): i32 {
    match (write_file("rt.txt", "round trip")) {
        Some(_) => { return 1; },
        None => {}
    }
    match (read_file("rt.txt")) {
        Ok(s) => { return s.len(); },
        Err(_) => { return 2; }
    }
    return 0 - 1;
}`
	_, code, _ := compileArm64InDir(t, src, nil)
	if code != 10 {
		t.Errorf("got %d, want 10 (len of \"round trip\")", code)
	}
}

// Function calls with more arguments than the register-arg
// window (8 on AAPCS64). Args 9+ live on the caller's stack
// at [sp+0..]. The prologue copies them from there into the
// callee's local slots so subsequent OpLoadLocal references
// can read them uniformly.
func TestArm64StackPassedArgs(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"sum_10_args", `function sum10(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32): i32 {
    return a + b + c + d + e + f + g + h + i + j;
}
function main(): i32 {
    return sum10(1, 2, 3, 4, 5, 6, 7, 8, 9, 10);
}`, 55},
		{"sum_12_args_order_check", `function sum12(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32, k: i32, l: i32): i32 {
    return (a * 1) + (b * 2) + (c * 3) + (d * 4) + (e * 5) + (f * 6) + (g * 7) + (h * 8) + (i * 9) + (j * 10) + (k * 11) + (l * 12);
}
function main(): i32 {
    // a..l = 1..12, weighted by their position; sum = 1+4+9+16+25+36+49+64+81+100+121+144 = 650
    return sum12(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12) - 600;
}`, 50},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

// Unsigned float ↔ int conversions. arm64 has dedicated
// `ucvtf` / `fcvtzu` opcodes; this test asserts the IR's
// Unsigned-flagged variants route through them and produce
// correct results for values above the signed boundary
// (u32 > 2^31; u64 > 2^63).
// Mirror of TestX86_64FloatBitCast. Doubles as a regression
// test for the OpConstF32 emit-form fix in this PR — every
// non-zero f32 const here had a bit pattern > 16 bits, which
// the old `mov x0, #<imm>` form rejected (the literal-pool
// `ldr x0, =<imm>` form now lifts that limit).
func TestArm64FloatBitCast(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"round-trip 1.0", `function main(): i32 {
    var x: f32 = 1.0;
    var b: i32 = f32_bits(x);
    var y: f32 = f32_from_bits(b);
    if (y == x) { return 0; }
    return 1;
}`, 0},
		{"round-trip 3.14", `function main(): i32 {
    var x: f32 = 3.14;
    var b: i32 = f32_bits(x);
    var y: f32 = f32_from_bits(b);
    if (y == x) { return 0; }
    return 1;
}`, 0},
		{"1.0 bits = 0x3F800000", `function main(): i32 {
    if (f32_bits(1.0) == 1065353216) { return 0; }
    return 1;
}`, 0},
		{"sign-bit preserved through round-trip", `function main(): i32 {
    var neg: f32 = 0.0 - 1.0;
    var b: i32 = f32_bits(neg);
    var back: f32 = f32_from_bits(b);
    if (back == neg) { return 0; }
    return 1;
}`, 0},
		// arm64's hardware `fneg` already does sign-bit XOR
		// (preserving -0.0); this pins parity with x86_64's
		// OpFNeg fix in the same PR.
		{"-0.0 bits = sign bit", `function main(): i32 {
    var bits_u: u32 = f32_bits(-0.0) as u32;
    if (bits_u == 2147483648 as u32) { return 0; }
    return 1;
}`, 0},
	} {
		_, code := compileAndRunArm64(t, c.src)
		if code != c.want {
			t.Errorf("%s: exit = %d, want %d\n--- src ---\n%s", c.name, code, c.want, c.src)
		}
	}
}

func TestArm64UnsignedFloatConv(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
		want int
	}{
		{"u32_large_to_f64_back", `function main(): i32 {
    var u: u32 = 3000000000 as u32;
    var f: f64 = u as f64;
    var back: u32 = f as u32;
    if (back == u) { return 0; }
    return 1;
}`, 0},
		{"u64_max_to_f64_is_huge", `function main(): i32 {
    var i: i64 = 0 - 1i64;
    var u: u64 = i as u64;
    var f: f64 = u as f64;
    var threshold: f64 = 10000000000000000000.0f64;
    if (f > threshold) { return 0; }
    return 1;
}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

// `stdin().read_line()` — exercises the .bss buffer + byte
// loop + Some/None Option wrap. arm64's runtime used to be
// stdin-only via __fern_read_line; this test now goes through
// the receiver-aware __fern_reader_read_line (stdin() returns
// a real Reader{fd:0} struct). Closes the parity-doc gap.
func TestArm64ReadLine(t *testing.T) {
	gcc, qemu := arm64Tooling(t)

	src := `function main(): i32 {
    match (stdin().read_line()) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return 0 - 1;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Monomorphise generic functions before codegen — the
	// production driver (cmd/fern) always runs this; the e2e
	// harness was missing it which only mattered once OpCallDirect
	// started consulting per-arg types for SysV register allocation
	// under the two-word string ABI.
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	runCase := func(stdin string, want int) {
		t.Helper()
		cmd := runArm64Bin(qemu, binPath)
		cmd.Stdin = strings.NewReader(stdin)
		_, _ = cmd.CombinedOutput()
		if got := cmd.ProcessState.ExitCode(); got != want {
			t.Errorf("stdin=%q: exit = %d, want %d", stdin, got, want)
		}
	}
	runCase("", 0)        // EOF before any byte → None
	runCase("hello\n", 1) // Some(line)
}

// Bare `read_line()` builtin — the stdin-only path through
// __fern_read_line (distinct from stdin().read_line()'s
// receiver-aware __fern_reader_read_line). Exercises the same
// .bss buffer + byte loop + Some/None wrap.
func TestArm64ReadLineBuiltin(t *testing.T) {
	gcc, qemu := arm64Tooling(t)

	src := `function main(): i32 {
    match (read_line()) {
        Some(_) => { return 1; },
        None => { return 0; }
    }
    return 0 - 1;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	runCase := func(stdin string, want int) {
		t.Helper()
		cmd := runArm64Bin(qemu, binPath)
		cmd.Stdin = strings.NewReader(stdin)
		_, _ = cmd.CombinedOutput()
		if got := cmd.ProcessState.ExitCode(); got != want {
			t.Errorf("stdin=%q: exit = %d, want %d", stdin, got, want)
		}
	}
	runCase("", 0)        // EOF before any byte → None
	runCase("hello\n", 1) // Some(line)
}

// Reader / Writer file I/O round-trip. open_writer +
// Writer.write + Writer.close; open_appender; open_reader +
// Reader.read_chunk / Reader.read_line / Reader.close. Mirrors
// TestWASMOpenAppender / TestWASMReaderReadChunk /
// TestWASMStreamingRoundtrip.
func TestArm64ReaderWriter(t *testing.T) {
	for _, c := range []struct {
		name       string
		src        string
		wantStdout string
		wantExit   int
	}{
		{"open_writer_then_append_then_read", `function main(): i32 {
    match (open_writer("ap.txt")) {
        Ok(w) => {
            match (w.write("first")) { Some(_) => { return 1; }, None => {} }
            match (w.close()) { Some(_) => { return 2; }, None => {} }
        },
        Err(_) => { return 3; }
    }
    match (open_appender("ap.txt")) {
        Ok(w) => {
            match (w.write("-second")) { Some(_) => { return 4; }, None => {} }
            match (w.close()) { Some(_) => { return 5; }, None => {} }
        },
        Err(_) => { return 6; }
    }
    match (read_file("ap.txt")) {
        Ok(s) => { write(s); return 0; },
        Err(_) => { return 7; }
    }
    return 0 - 1;
}`, "first-second", 0},
		{"reader_read_chunk", `function main(): i32 {
    match (open_writer("rc.txt")) {
        Ok(w) => {
            match (w.write("hello world")) { Some(_) => { return 1; }, None => {} }
            match (w.close()) { Some(_) => { return 2; }, None => {} }
        },
        Err(_) => { return 3; }
    }
    match (open_reader("rc.txt")) {
        Ok(r) => {
            match (r.read_chunk(5)) { Some(s) => { write(s); write(":"); }, None => { return 4; } }
            match (r.read_chunk(20)) { Some(s) => { write(s); }, None => { return 5; } }
            match (r.read_chunk(20)) { Some(_) => { return 6; }, None => { return 0; } }
        },
        Err(_) => { return 7; }
    }
    return 0 - 1;
}`, "hello: world", 0},
		{"streaming_roundtrip_lines", `function main(): i32 {
    match (open_writer("rt.txt")) {
        Ok(w) => {
            match (w.write("line 1\n")) { Some(_) => { return 1; }, None => {} }
            match (w.write("line 2\n")) { Some(_) => { return 2; }, None => {} }
            match (w.close()) { Some(_) => { return 3; }, None => {} }
        },
        Err(_) => { return 4; }
    }
    match (open_reader("rt.txt")) {
        Ok(r) => {
            match (r.read_line()) { Some(line) => { write(line); }, None => { return 5; } }
            match (r.read_line()) { Some(line) => { write(line); }, None => { return 6; } }
            match (r.read_line()) { Some(_) => { return 7; }, None => {} }
            match (r.close()) { Some(_) => { return 8; }, None => {} }
            return 0;
        },
        Err(_) => { return 9; }
    }
    return 0 - 1;
}`, "line 1\nline 2\n", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			stdout, code, _ := compileArm64InDir(t, c.src, nil)
			if code != c.wantExit {
				t.Errorf("exit = %d, want %d (stdout = %q)", code, c.wantExit, stdout)
			}
			if !strings.Contains(stdout, c.wantStdout) {
				t.Errorf("stdout = %q, want to contain %q", stdout, c.wantStdout)
			}
		})
	}
}

// Wasm-shaped feature parity for the native arm64 backend.
// Each case asserts the program returns 0 (the wasm tests
// returned arbitrary i32 values via runWasm; on native we get
// the low byte of main's return as the exit code, so the
// programs internally compare and short-circuit to 0/N to fit
// the exit-code channel). Same source on x86-64 (see
// TestX86_64FeatureParity).
func TestArm64FeatureParity(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"defer_basic", `
import "core/map";
function inner(trace: Map[string, i32]): i32 {
    trace = trace.insert("body-start", 1);
    defer trace.insert("first-defer", 10);
    defer trace.insert("second-defer", 20);
    trace = trace.insert("body-end", 2);
    return 42;
}
function main(): i32 {
    var trace: Map[string, i32] = map_new(8);
    var r: i32 = inner(trace);
    if (r != 42) { return 1; }
    if (trace.len() != 4) { return 2; }
    if (trace.get_or("body-start", 0) != 1) { return 3; }
    if (trace.get_or("body-end", 0) != 2) { return 4; }
    if (trace.get_or("first-defer", 0) != 10) { return 5; }
    if (trace.get_or("second-defer", 0) != 20) { return 6; }
    return 0;
}`},
		{"fstring_interp", `
import "std/i32";
function main(): i32 {
    var x: i32 = 42;
    var s: string = f"x is {x}";
    if (s.len() == 7) { return 0; }
    return 1;
}`},
		{"for_each_array", `function main(): i32 {
    var xs: i32[] = [1, 2, 3, 4, 5];
    var sum: i32 = 0;
    for x in xs { sum = sum + x; }
    if (sum == 15) { return 0; }
    return 1;
}`},
		{"if_let_match", `function main(): i32 {
    var o: Option[i32] = Some(42);
    if let Some(x) = o {
        if (x == 42) { return 0; }
        return 1;
    } else {
        return 2;
    }
}`},
		{"tuple_multi_return", `function divmod(a: i32, b: i32): (i32, i32) {
    return (a / b, a - (a / b) * b);
}
function main(): i32 {
    var p = divmod(17, 5);
    if (p.0 == 3 && p.1 == 2) { return 0; }
    return 1;
}`},
		{"generic_infer_from_arg", `function id[T](x: T): T { return x; }
function main(): i32 {
    var a = id(42);
    var b = id(7);
    if (a == 42 && b == 7) { return 0; }
    return 1;
}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != 0 {
				t.Errorf("got exit %d, want 0", code)
			}
		})
	}
}

// Programs must declare every stdlib module they use (there is no
// auto-prelude). With the prelude-to-modules migration (PRs #505 → #513)
// every stdlib module declares its own method-source imports —
// std/i32 ↔ std/string ↔ std/array form cyclic dependencies that
// modload's stdlib-cycle gate handles — so the user's explicit
// imports transitively pull in everything the dispatched methods
// need.
//
// Each case below proves a different slice of the no-prelude
// stack:
//
//   - `i32_string_cycle` — std/i32 imports std/string for
//     `pad_start` inside to_string_padded; the cycle resolves.
//   - `array_method_chain` — std/array transitively pulls in
//     std/i32 (for `.abs()` inside abs_each) and std/sort.
//   - `qualified_int_call` — qualified `int.int_to_string_radix`
//     resolves through modload's normal mangling path under
//     no-prelude.
//   - `mixed_stdlib` — multiple explicit imports compose cleanly.
//
// All programs return 0 on success and a small nonzero code on
// the first failing assertion, so the exit-code channel is
// enough to verify correctness end-to-end.
func TestArm64NoPreludeStdlibImports(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"i32_string_cycle", `
import "std/i32";
function main(): i32 {
    var s: string = (42).to_string_padded(6);
    if (s == "000042") { return 0; }
    return 1;
}`},
		{"array_method_chain", `
import "std/array";
function main(): i32 {
    var xs: i32[] = [0 - 3, 4, 0 - 1];
    var ys = xs.abs_each();
    if (ys[0] + ys[1] + ys[2] == 8) { return 0; }
    return 1;
}`},
		{"qualified_int_call", `
import "core/int";
function main(): i32 {
    var s: string = int.int_to_string_radix(255, 16);
    if (s == "ff") { return 0; }
    return 1;
}`},
		{"mixed_stdlib", `
import "std/i32";
import "std/string";
import "std/array";
function main(): i32 {
    var s: string = (0 - 42).to_string();
    if (s != "-42") { return 1; }
    var strs: string[] = ["b", "a", "c"];
    var joined: string = strs.join(",");
    if (joined != "b,a,c") { return 2; }
    return 0;
}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != 0 {
				t.Errorf("got exit %d, want 0", code)
			}
		})
	}
}

// Refcount builtins (`__rc_get` / `__rc_inc` / `__rc_dec`)
// exposed for Phase-1 testing. Validates Phase 1a (rc=1 on
// `__alloc_u8`) and Phase 1b (inc / dec are sentinel-aware and
// don't corrupt the rc word). The program returns 0 iff the
// observed rc progression is exactly 1 → 2 → 1.
func TestArm64RcBuiltins(t *testing.T) {
	src := `
function main(): i32 {
    var arr: u8[] = __alloc_u8(10);
    var r1: i32 = __rc_get(arr);
    __rc_inc(arr);
    var r2: i32 = __rc_get(arr);
    __rc_dec(arr);
    var r3: i32 = __rc_get(arr);
    return r1 + r2 + r3 - 4;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (rc progression off)", code)
	}
}

// Phase 1d transfer inc, refined by #4402 opt 1 (dead-alias dup/drop
// cancellation): a pure borrowed-view alias — never reassigned, never
// returned, never moved — elides its inc AND its exit-sweep dec as a
// net-zero pair, so the rc stays 1. An alias that is still referenced
// under the return keeps the ordinary transfer inc (rc 2).
func TestArm64RcAliasInc(t *testing.T) {
	dead := `
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var alias: u8[] = arr;
    return __rc_get(arr) - 1;
}`
	if _, code := compileAndRunArm64(t, dead); code != 0 {
		t.Errorf("dead alias: got exit %d, want 0 (borrowed view elides the inc — rc stays 1)", code)
	}
	live := `
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var alias: u8[] = arr;
    return __rc_get(arr) - 2 + alias.len() - 8;
}`
	if _, code := compileAndRunArm64(t, live); code != 0 {
		t.Errorf("returned alias: got exit %d, want 0 (transfer inc kept — rc 2)", code)
	}
}

// Phase 1d-ii (+ Phase 1d-viii): FieldAccess and Index of
// array type count as aliases. With Phase 1d-viii's lit-
// element inc, the struct- / array-lit constructor ALSO inc's
// the captured array — so `inner` ends up with rc=3 after
// the full chain: 1 (alloc) + 1 (lit-element store) + 1
// (alias read).
func TestArm64RcAliasIncFieldAndIndex(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{"field_access", `
struct Holder { items: u8[] }
function main(): i32 {
    var inner: u8[] = __alloc_u8(8);
    var h: Holder = Holder { items: inner };
    var alias: u8[] = h.items;
    // Precise drops (RC-Perceus) release the now-dead struct h at its last
    // use; reference it in the return so it stays live through the check —
    // this measures the fully-aliased rc (inner + h.items + alias).
    // h.items.len()-8 == 0, so the result is unchanged.
    return __rc_get(inner) - 3 + h.items.len() - 8;
}`},
		{"index_load", `
function main(): i32 {
    var inner: u8[] = __alloc_u8(8);
    var matrix: u8[][] = [inner];
    var alias: u8[] = matrix[0];
    // Precise drops (RC-Perceus) release the now-dead matrix at its last
    // use; reference it in the return so it stays live through the check —
    // this measures the fully-aliased rc (inner + the array element + alias),
    // which is what the test asserts. matrix.len()-1 == 0, so the result is
    // unchanged.
    return __rc_get(inner) - 3 + matrix.len() - 1;
}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != 0 {
				t.Errorf("got exit %d, want 0 (lit + alias should bump rc to 3)", code)
			}
		})
	}
}

// TestArm64HeapAddressFits32Bits is a diagnostic probe that
// exits non-zero iff the bump heap's first allocation lands
// at an address that doesn't fit in i32. The runtime's
// __fern_alloc currently uses a 0x10000000 hint without
// MAP_FIXED — qemu-aarch64 honors the hint exactly, but the
// native-arm64 kernel may relocate the mmap into the standard
// high mmap region (above 4 GiB). When that happens, any
// stdlib code that casts a heap pointer to i32 silently
// truncates and traps on the bad address.
//
// Currently expected to pass on every host (the post-fix
// stdlib doesn't truncate pointers anymore), but the test
// stays as a regression watch — if a future change lands a
// new `as i32` on a pointer path, the probe surfaces it
// before the fernsmith corpus does.
func TestArm64HeapAddressFits32Bits(t *testing.T) {
	src := `function main(): i32 {
    var p: usize = __alloc(8);
    var pi: i64 = p as i64;
    var hi: i32 = (pi >> (32 as i64)) as i32;
    return hi;
}`
	out, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("first alloc has high bits set (hi=%d) — heap is above 4 GiB; stdlib `pointer as i32` casts will truncate.\noutput:\n%s",
			code, out)
	}
}

// Phase 1d-iii: `y = x;` reassignment also bumps the rc on x.
// The motivating parser.fern shape is `nfuncs = into.funcs;`
// followed by an in-loop `nfuncs = nfuncs.append(...);` — the
// first assignment is an alias (FieldAccess RHS), the second
// rebinds with a fresh push result. Here we test the explicit
// `y = x;` form directly.
func TestArm64RcAliasIncReassign(t *testing.T) {
	src := `
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var other: u8[] = __alloc_u8(8);
    other = arr;
    return __rc_get(arr) - 2;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (assign-alias should bump rc to 2)", code)
	}
}

// Phase 1d-vii: a closure that captures an array bumps the
// rc on the captured value. The local function `f` captures
// `arr` from main's scope; OpMakeClosure pushes each capture
// value and the IR's emit inserts a __fern_rc_inc on each
// alias-shaped array capture. The resulting closure's env
// co-owns the array reference.
func TestArm64RcClosureCaptureInc(t *testing.T) {
	src := `
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    function f(): u8[] { return arr; }
    var fn_alias = f;
    return __rc_get(arr) - 2;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (closure capture should bump rc to 2)", code)
	}
}

// Phase 1d-vi: reassigning an existing array variable dec's
// the OLD value it held before the new value lands. Three
// fresh arrays + two reassignments to the same slot let us
// observe the sequence:
//
//	arr1 = arr2 → arr2's rc 1→2; original arr1's rc 1→0
//	arr1 = arr3 → arr3's rc 1→2; arr2 (= old arr1) rc 2→1
//
// Mid-function read of arr2 sees rc=1 (post-overwrite dec);
// read of arr3 sees rc=2 (post-inc). Sum = 3 = 1 + 2.
func TestArm64RcDecOnOverwrite(t *testing.T) {
	src := `
function main(): i32 {
    var arr1: u8[] = __alloc_u8(8);
    var arr2: u8[] = __alloc_u8(8);
    var arr3: u8[] = __alloc_u8(8);
    arr1 = arr2;
    arr1 = arr3;
    return __rc_get(arr2) + __rc_get(arr3) - 3;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (arr2 rc=1 post-overwrite, arr3 rc=2 post-inc, sum=3)", code)
	}
}

// Phase 2d-borrow: function parameters are BORROWED — the call
// site no longer inc's the argument and the callee no longer
// dec's the parameter at exit. Helper `peek(arr)` therefore
// observes the SAME rc the caller holds (1), not the bumped rc=2
// of the old owned-parameter model. The rc is unchanged across
// the call: mid == after == 1.
func TestArm64RcDecAtExit(t *testing.T) {
	src := `
function peek(arr: u8[]): i32 { return __rc_get(arr); }
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var mid: i32 = peek(arr);
    var after: i32 = __rc_get(arr);
    return (mid - 1) + (after - 1);
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (borrowed param: mid=1, after=1)", code)
	}
}

// Phase 2d-borrow: passing an array as a function-call argument
// is a BORROW — no caller-side inc, no callee-side exit dec. The
// rc is therefore untouched across the call: it stays exactly 1,
// the same as before. (Pre-borrow this was an inc+dec round-trip
// that also netted to 1; the observable result is unchanged, but
// no rc traffic is emitted now.)
func TestArm64RcAliasIncCallArg(t *testing.T) {
	src := `
function f(arr: u8[]): i32 { return 0; }
function main(): i32 {
    var arr: u8[] = __alloc_u8(8);
    var _: i32 = f(arr);
    return __rc_get(arr) - 1;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (borrowed arg: rc stays 1, no inc/dec)", code)
	}
}

// Phase 3: the rc-underflow detector on arm64 (BSS counter
// __fern_rc_underflow bumped by __fern_rc_dec, read by
// __fern_rc_underflow_count). Mirrors TestX86_64RcUnderflowDetector
// — pins the mechanism and that map self-assign is drift-free.
func TestArm64RcUnderflowDetector(t *testing.T) {
	selfAssign := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("a", 9);
    return __rc_underflow_count() * 100
         + (m.len() - 2)
         + (m.get_or("a", 0) - 9);
}`
	if _, code := compileAndRunArm64(t, selfAssign); code != 0 {
		t.Errorf("got %d, want 0 (arm64 map self-assign: 0 underflow, correct contents)", code)
	}

	overRelease := `function main(): i32 {
    var a: u8[] = __alloc_u8(8);
    __rc_dec(a);
    __rc_dec(a);
    return __rc_underflow_count();
}`
	if _, code := compileAndRunArm64(t, overRelease); code != 1 {
		t.Errorf("got %d, want 1 (arm64 double-dec over-release count)", code)
	}
}

// Phase 3 step 3: drop handlers, native parity. Mirrors the wasm
// TestWASMRcDropArrayElements + x86's TestX86_64RcDropArrayElements.
// The arm64 __fern_drop_arr_ptr carries __fern_rc_dec's
// low-address guard so an array-typed slot holding a non-pointer
// (enum tag, never-taken-branch garbage) is passed through rather
// than faulting.
func TestArm64RcDropArrayElements(t *testing.T) {
	fires := `function consume(inner: u8[]): i32 {
    var outer: u8[][] = [inner];
    return 0;
}
function main(): i32 {
    var inner: u8[] = __alloc_u8(4);
    var before: i32 = __rc_get(inner);
    var ignore: i32 = consume(inner);
    var after: i32 = __rc_get(inner);
    return (before - 1) + (after - 1);
}`
	if _, code := compileAndRunArm64(t, fires); code != 0 {
		t.Errorf("got %d, want 0 (drop must dec the nested element back to rc 1)", code)
	}

	noUnder := `function build(): i32 {
    var inner: i32[] = [1, 2, 3];
    var a: i32[][] = [inner];
    var b: i32[][] = [[4, 5], [6]];
    return a[0][1] + b[1][0];
}
function main(): i32 {
    return (build() - 8) + __rc_underflow_count();
}`
	if _, code := compileAndRunArm64(t, noUnder); code != 0 {
		t.Errorf("got %d, want 0 (nested-array drop: correct values, 0 underflow)", code)
	}
}

// Phase 3 step 3: struct drop handlers, native parity. Mirrors
// TestX86_64RcDropStructFields. A user struct with pointer-shaped
// rc-tracked fields drops those fields on its last reference
// (gated by __fern_rc_is_unique) before dec'ing the box.
func TestArm64RcDropStructFields(t *testing.T) {
	fires := `struct Holder { items: u8[] }
function consume(inner: u8[]): i32 {
    var h: Holder = Holder { items: inner };
    return 0;
}
function main(): i32 {
    var inner: u8[] = __alloc_u8(4);
    var before: i32 = __rc_get(inner);
    var ignore: i32 = consume(inner);
    var after: i32 = __rc_get(inner);
    return (before - 1) + (after - 1) + __rc_underflow_count();
}`
	if _, code := compileAndRunArm64(t, fires); code != 0 {
		t.Errorf("got %d, want 0 (struct field drop must dec the array back to rc 1)", code)
	}

	aliased := `struct Holder { items: i32[] }
function main(): i32 {
    var inner: i32[] = [1, 2, 3];
    var h1: Holder = Holder { items: inner };
    var h2: Holder = h1;
    return h2.items[2] + __rc_underflow_count() - 3;
}`
	if _, code := compileAndRunArm64(t, aliased); code != 0 {
		t.Errorf("got %d, want 0 (aliased struct: no double field-drop, 0 underflow)", code)
	}

	nested := `struct Grid { rows: i32[][] }
struct Inner { v: i32[] }
struct Outer { inner: Inner }
function build(): i32 {
    var a: i32[] = [1, 2, 3];
    var g: Grid = Grid { rows: [a] };
    var arr: i32[] = [7, 8];
    var o: Outer = Outer { inner: Inner { v: arr } };
    return g.rows[0][1] + o.inner.v[0] + __rc_underflow_count();
}
function main(): i32 {
    return build() - 9;
}`
	if _, code := compileAndRunArm64(t, nested); code != 0 {
		t.Errorf("got %d, want 0 (nested struct/array fields: correct values, 0 underflow)", code)
	}
}

// Phase 3 step 3: enum drop handlers (uniform case), native
// parity. Mirrors TestX86_64RcDropEnumPayload. A heap-boxed enum
// whose payload-carrying variants share an identical droppable
// signature (e.g. a union of structs) drops the payloads on its
// last reference before dec'ing the box.
func TestArm64RcDropEnumPayload(t *testing.T) {
	fresh := `struct W { a: i32[] }
struct V2 { b: i32 }
type U = W | V2;
function mk(): U { return W { a: [1, 2, 3] }; }
function build(): i32 {
    var u: U = mk();
    match (u) { W(w) => { return w.a[1] + __rc_underflow_count(); }, V2(x) => { return x.b; } }
    return 0 - 1;
}
function main(): i32 { return build() - 2; }`
	if _, code := compileAndRunArm64(t, fresh); code != 0 {
		t.Errorf("got %d, want 0 (union payload drop: value 2, 0 underflow)", code)
	}

	aliased := `struct W { a: i32 }
struct V2 { b: i32 }
type U = W | V2;
function main(): i32 {
    var w: W = W { a: 7 };
    var u: U = w;
    return w.a + __rc_underflow_count() - 7;
}`
	if _, code := compileAndRunArm64(t, aliased); code != 0 {
		t.Errorf("got %d, want 0 (aliased struct widened to union: 0 underflow)", code)
	}

	nonUniform := `enum E { Arr(i32[]), Num(i32) }
function main(): i32 {
    var e: E = Arr([1, 2, 3]);
    var f: E = Num(9);
    match (e) {
        Arr(a) => { return a.len() + __rc_underflow_count() - 3; },
        Num(_) => { return 0 - 1; }
    }
    return 0 - 2;
}`
	if _, code := compileAndRunArm64(t, nonUniform); code != 0 {
		t.Errorf("got %d, want 0 (non-uniform enum: correct value, 0 underflow)", code)
	}
}

// Phase 2: arr.append(v) checks rc + cap. When rc==1 and cap >
// len, the helper mutates in place — the returned pointer
// equals the input pointer. First push of a 3-element literal
// must copy (cap=3, oldLen=3, no spare cap); the SECOND push
// goes through the in-place path because the copy bumped cap
// to max(2*newLen, 4) = 8 and only 1 element is occupied beyond
// the original 3.
func TestArm64ArrayPushInPlaceFastPath(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20];
    xs = xs.append(30);          // copy: cap=2, len=2, no spare
    var addr_before: usize = xs as usize;
    xs = xs.append(40);          // in-place: cap=6, len=3, spare!
    var addr_after: usize = xs as usize;
    if (addr_before != addr_after) { return 1; }
    if (xs.len() != 4) { return 2; }
    if (xs[3] != 40) { return 3; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (in-place fast path should reuse buffer)", code)
	}
}

// Phase 2: aliased arrays force the copy path even when cap is
// available. The shared rc>1 means a mutate-in-place would
// corrupt the other holder's view.
func TestArm64ArrayPushAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20];
    xs = xs.append(30);          // copy, cap now 6
    var ys = xs;               // alias, rc=2
    ys = ys.append(40);          // must COPY (rc>1)
    if (xs.len() != 3) { return 1; }   // xs unchanged
    if (xs[0] != 10) { return 2; }
    if (ys.len() != 4) { return 3; }
    if (ys[3] != 40) { return 4; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased push must copy)", code)
	}
}

// Phase 2b: `arr[i] = v` on a writable local-ident array
// routes through __fern_arr_cow_inplace. On rc==1 the helper
// returns arr unchanged (in-place mutation); the buffer
// pointer is unchanged after the assignment.
func TestArm64ArrayIndexSetInPlaceFastPath(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var addr_before: usize = xs as usize;
    xs = xs.with(1, 999);
    var addr_after: usize = xs as usize;
    if (addr_before != addr_after) { return 1; }
    if (xs[1] != 999) { return 2; }
    if (xs[0] != 10) { return 3; }
    if (xs[2] != 30) { return 4; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (arr[i]=v in-place when rc==1)", code)
	}
}

// Phase 2b: aliased array `arr[i] = v` must copy on rc>1 —
// otherwise the other holder's view would silently mutate.
func TestArm64ArrayIndexSetAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var ys = xs;            // alias, rc=2
    ys = ys.with(0, 999);            // must COPY
    if (xs[0] != 10) { return 1; }   // xs unchanged
    if (xs[1] != 20) { return 2; }
    if (xs[2] != 30) { return 3; }
    if (ys[0] != 999) { return 4; }
    if (ys[1] != 20) { return 5; }
    if (ys[2] != 30) { return 6; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased arr[i]=v must copy)", code)
	}
}

// Phase 2b on a u8-stride array — mirrors the int_to_string
// pattern (scratch[i] = digit) that hit the wasm raw-_start
// path's __fern_rc_dec low-address guard before the helper
// internalised rc bookkeeping.
func TestArm64ArrayIndexSetU8Stride(t *testing.T) {
	src := `function main(): i32 {
    var buf: u8[] = __alloc_u8(4);
    buf = buf.with(0, 65 as u8);
    buf = buf.with(1, 66 as u8);
    buf = buf.with(2, 67 as u8);
    buf = buf.with(3, 68 as u8);
    return (buf[0] as i32) + (buf[1] as i32) + (buf[2] as i32) + (buf[3] as i32) - 266;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (u8 arr[i]=v preserves all writes)", code)
	}
}

// Phase 2b extension: `obj.field[i] = v` for a writable local-
// ident struct holding an array field. CoW applies to the
// array buffer; the field's pointer is updated to the new
// buffer on rc>1.
func TestArm64ArrayIndexSetStructField(t *testing.T) {
	src := `struct State { items: i32[] }
function main(): i32 {
    var s: State = State{items: [10, 20, 30]};
    s = State { ...s, items: s.items.with(1, 999) };
    if (s.items[0] != 10) { return 1; }
    if (s.items[1] != 999) { return 2; }
    if (s.items[2] != 30) { return 3; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (struct.field[i]=v in-place when rc==1)", code)
	}
}

// Aliased struct-field array: writing through s.field must
// copy when the underlying buffer is shared with another
// holder. The caller's `arr` should stay unchanged.
func TestArm64ArrayIndexSetStructFieldAliasedCopies(t *testing.T) {
	src := `struct State { items: i32[] }
function main(): i32 {
    var arr: i32[] = [10, 20, 30];
    var s: State = State{items: arr};
    s = State { ...s, items: s.items.with(1, 999) };
    if (arr[0] != 10) { return 1; }
    if (arr[1] != 20) { return 2; }
    if (arr[2] != 30) { return 3; }
    if (s.items[0] != 10) { return 4; }
    if (s.items[1] != 999) { return 5; }
    if (s.items[2] != 30) { return 6; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased struct.field[i]=v must copy)", code)
	}
}

// Phase 2b extension: `a.b.field[i] = v` for nested struct
// field chains. rootIdentOfFieldChain walks the chain back to
// the root ident; the helper handles arbitrary chain depths.
func TestArm64ArrayIndexSetNestedStructField(t *testing.T) {
	src := `struct Inner { items: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    var o: Outer = Outer{inner: Inner{items: [10, 20, 30]}};
    o = Outer { ...o, inner: Inner { ...o.inner, items: o.inner.items.with(1, 999) } };
    if (o.inner.items[0] != 10) { return 1; }
    if (o.inner.items[1] != 999) { return 2; }
    if (o.inner.items[2] != 30) { return 3; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (a.b.field[i]=v in-place when rc==1)", code)
	}
}

// Aliased nested struct field — writing through o.inner.items
// must copy when the underlying buffer is shared with the
// outer `arr` local.
func TestArm64ArrayIndexSetNestedStructFieldAliasedCopies(t *testing.T) {
	src := `struct Inner { items: i32[] }
struct Outer { inner: Inner }
function main(): i32 {
    var arr: i32[] = [10, 20, 30];
    var o: Outer = Outer{inner: Inner{items: arr}};
    o = Outer { ...o, inner: Inner { ...o.inner, items: o.inner.items.with(1, 999) } };
    if (arr[1] != 20) { return 1; }  // arr unchanged
    if (o.inner.items[1] != 999) { return 2; }  // o updated
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased a.b.field[i]=v must copy)", code)
	}
}

// Phase 2b extension: `mat[i][j] = v` for arr-of-arr targets.
// Verifies the basic in-place mutation works when mat[i]'s
// rc is 1.
func TestArm64ArrayIndexSetMat(t *testing.T) {
	src := `function main(): i32 {
    var mat: i32[][] = [[1, 2, 3], [4, 5, 6]];
    mat = mat.with(0, mat[0].with(1, 999));
    if (mat[0][0] != 1) { return 1; }
    if (mat[0][1] != 999) { return 2; }
    if (mat[0][2] != 3) { return 3; }
    if (mat[1][0] != 4) { return 4; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (mat[i][j]=v in-place when inner rc==1)", code)
	}
}

// Phase 2b extension: aliased inner array via Phase 1d-ii inc
// (`var inner = mat[0]`) makes the inner's rc 2; the write
// must copy and leave the alias unchanged.
func TestArm64ArrayIndexSetMatInnerAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var mat: i32[][] = [[1, 2], [3, 4]];
    var inner = mat[0];     // Phase 1d-ii inc: inner.rc = 2
    mat = mat.with(0, mat[0].with(1, 999));        // CoW the inner; inner alias unchanged
    if (inner[1] != 2) { return 1; }
    if (mat[0][1] != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (mat[i][j]=v with aliased inner must copy)", code)
	}
}

// Phase 2c: Map.set is now value-returning. Callers can use
// the `m = m.insert(k, v)` idiom for explicit value semantics;
// the existing `m.insert(k, v)` statement form still works
// (return discarded).
func TestArm64MapSetReturnsMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    if (m.get_or("a", 0) != 1) { return 1; }
    if (m.get_or("b", 0) != 2) { return 2; }
    if (m.get_or("c", 0) != 3) { return 3; }
    if (m.len() != 3) { return 4; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (Map.set returns Map)", code)
	}
}

// Phase 2b extension: `obj.mat[i][j] = v` — outer rooted in a
// FieldAccess chain rather than a bare ident. outerRootIdent
// walks the chain back to the root local-ident; the rest of
// the nested CoW emit handles arbitrary outer expressions via
// re-evaluation.
func TestArm64ArrayIndexSetObjMatInnerAliasedCopies(t *testing.T) {
	src := `struct State { mat: i32[][] }
function main(): i32 {
    var inner: i32[] = [1, 2, 3];
    var s: State = State{mat: [inner, [4, 5, 6]]};
    s = State { ...s, mat: s.mat.with(0, s.mat[0].with(1, 999)) };
    if (inner[1] != 2) { return 1; }
    if (s.mat[0][1] != 999) { return 2; }
    if (s.mat[0][0] != 1) { return 3; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (obj.mat[i][j]=v with shared inner must copy)", code)
	}
}

// Phase 2b: explicit value-returning `arr.with(i, v)` method.
// Self-assign idiom — same shape as the `arr[i] = v` desugar
// would emit, but expression-position so it composes.
func TestArm64ArraySetSelfAssign(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    xs = xs.with(1, 999);
    if (xs[0] != 10) { return 1; }
    if (xs[1] != 999) { return 2; }
    if (xs[2] != 30) { return 3; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (xs = xs.with(1, 999))", code)
	}
}

// Phase 2b: aliased `arr.with` must copy. The original holder
// stays unchanged.
func TestArm64ArraySetAliasedCopies(t *testing.T) {
	src := `function main(): i32 {
    var xs: i32[] = [10, 20, 30];
    var ys = xs;                    // Phase 1d-i: xs.rc = 2
    ys = ys.with(0, 999);            // rc>1 → copy. xs unchanged.
    if (xs[0] != 10) { return 1; }
    if (ys[0] != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased arr.with must copy)", code)
	}
}

// Phase 2c: m.without(k) returns (Map[K,V], bool).
// Tests tuple destructuring, bool-field access, and statement-position discard.
func TestArm64MapDeleteReturnsMapBool(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    // Bool field access on call result.
    if (!m.without("b").1) { return 1; }   // "b" present → true
    if (m.without("z").1)  { return 2; }   // "z" missing → false
    // Tuple destructuring: m2 is the updated map, ok is the found-flag.
    var (m2, ok) = m.without("a");
    if (!ok) { return 3; }
    if (m2.has("a")) { return 4; }
    if (!m2.has("c")) { return 5; }
    if (m2.len() != 1) { return 6; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (Map.delete returns (Map, bool))", code)
	}
}

// Phase 2c: m.cleared() returns Map[K,V].
func TestArm64MapClearReturnsMap(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("x", 10);
    m = m.insert("y", 20);
    if (m.len() != 2) { return 1; }
    m = m.cleared();
    if (m.len() != 0) { return 2; }
    if (m.has("x")) { return 3; }
    // Re-insert after clear must work.
    m = m.insert("z", 99);
    if (m.len() != 1) { return 4; }
    if (m.get_or("z", 0) != 99) { return 5; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (Map.clear returns Map)", code)
	}
}

// Phase 2d: Map.set copy-on-write — arm64 sibling of
// TestX86_64MapSetAliasedCopies. An aliased map (var m2 = m1)
// has rc=2, so m2.insert(...) copies and leaves m1 intact.
func TestArm64MapSetAliasedCopies(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);                 // in-place (rc==1)
    var m2 = m1;                    // alias → rc=2
    m2 = m2.insert("a", 999);          // rc>1 → copy; m1 unchanged
    if (m1.get_or("a", 0) != 1)   { return 1; }
    if (m2.get_or("a", 0) != 999) { return 2; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased Map.set must copy)", code)
	}
}

// Phase 2d: Map.delete / Map.clear copy-on-write — arm64 sibling
// of TestX86_64MapDeleteClearAliasedCopies.
func TestArm64MapDeleteClearAliasedCopies(t *testing.T) {
	src := `
import "core/map";
function main(): i32 {
    var m1: Map[string, i32] = map_new(8);
    m1 = m1.insert("a", 1);
    m1 = m1.insert("b", 2);
    var m2 = m1;                       // alias → rc=2
    var (m3, ok) = m2.without("a");     // rc>1 → copy; m1/m2 intact
    if (!ok)            { return 1; }
    if (m1.len() != 2)  { return 2; }
    if (!m1.has("a"))   { return 3; }
    if (m3.len() != 1)  { return 4; }
    if (m3.has("a"))    { return 5; }
    var m4 = m1;                       // alias → rc=2
    m4 = m4.cleared();                   // rc>1 → copy; m1 intact
    if (m1.len() != 2)  { return 6; }
    if (m4.len() != 0)  { return 7; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (aliased Map.delete/clear must copy)", code)
	}
}

// Pointer-typed tuple element — struct, array, or nested tuple in
// a tuple slot must round-trip through ptrW-aligned storage.
// Bug: exprType returned nil for *ast.StructLit / *ast.ArrayLit /
// *ast.TupleLit, so the TupleLit slot-sizing fell back to 4 bytes
// while the load side knew the static type was 8 bytes on arm64.
// Pointer stored at offset 4, read from offset 8 → segfault.
func TestArm64TupleStructElem(t *testing.T) {
	src := `struct Inner { x: i32, y: i32 }
function main(): i32 {
    var t: (i32, Inner) = (1, Inner { x: 2, y: 3 });
    if (t.0 != 1) { return 1; }
    var inner: Inner = t.1;
    if (inner.x != 2) { return 2; }
    if (inner.y != 3) { return 3; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (struct in tuple slot)", code)
	}
}

func TestArm64TupleArrayElem(t *testing.T) {
	src := `function main(): i32 {
    var t: (i32, i32[]) = (1, [10, 20, 30]);
    if (t.0 != 1) { return 1; }
    var arr: i32[] = t.1;
    if (arr.len() != 3) { return 2; }
    if (arr[0] != 10) { return 3; }
    if (arr[2] != 30) { return 4; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (array in tuple slot)", code)
	}
}

func TestArm64TupleNestedTuple(t *testing.T) {
	src := `function main(): i32 {
    var t: (i32, (i32, i32)) = (1, (2, 3));
    var (a, b) = t;
    if (a != 1) { return 1; }
    var (c, d) = b;
    if (c != 2) { return 2; }
    if (d != 3) { return 3; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (nested tuple destructure)", code)
	}
}

// Chained numeric tuple field access — `t.1.0` where the inner
// is itself a tuple. The lexer previously ate `1.0` as a single
// float token, so the second `.0` got lost and the parser failed
// with "expected Ident, got 1.0". Fix tracks the previous-token
// kind and suppresses the `.<digit>` → float upgrade right after
// a `.` punctuator.
func TestArm64LexerChainedTupleNumericAccess(t *testing.T) {
	src := `function main(): i32 {
    var t: (i32, (i32, i32)) = (1, (2, 3));
    if (t.0 != 1) { return 1; }
    if (t.1.0 != 2) { return 2; }
    if (t.1.1 != 3) { return 3; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (chained t.1.0 numeric access)", code)
	}
}

// Empty `Map {}` with a destination annotation must inherit
// K / V from the destination rather than defaulting to
// `Map[i32, i32]`. Previously the checker rejected `var m:
// Map[string, i32] = Map {};` with E003 "cannot assign Map[i32,
// i32] to variable of type Map[string, i32]" because settleNumeric
// only walked entries (no-op for empty) and the surrounding
// assignable check saw the pre-settle default.
func TestArm64EmptyMapDestinationInference(t *testing.T) {
	src := `
import "core/map";
function take(m: Map[string, i32]): i32 { return m.len(); }
function mkEmpty(): Map[i32, string] { return Map {}; }
function main(): i32 {
    // Var declaration: K=string, V=i32
    var a: Map[string, i32] = Map {};
    if (a.len() != 0) { return 1; }
    a = a.insert("k", 42);
    if (a.get_or("k", 0) != 42) { return 2; }
    // Var declaration: K=i32, V=string
    var b: Map[i32, string] = Map {};
    if (b.len() != 0) { return 3; }
    b = b.insert(7, "hello");
    if (!b.has(7)) { return 4; }
    // Function argument
    if (take(Map {}) != 0) { return 5; }
    // Return statement
    var r = mkEmpty();
    if (r.len() != 0) { return 6; }
    // Default Map[i32, i32] still works
    var d: Map[i32, i32] = Map {};
    if (d.len() != 0) { return 7; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (empty Map literal inherits K/V from destination)", code)
	}
}

// Enum variant in a tuple slot — payload-less (`Green`) and
// payload-bearing constructor (`Some(42)`). exprType returned nil
// for a bare variant ident and for a variant constructor call, so
// the enclosing TupleLit sized the variant element at 4 bytes while
// the load side resolved EnumType (8 bytes on arm64). Pointer stored
// at offset 4, read from offset 8 → segfault. Same family as the
// struct / array / nested-tuple tuple-element fix.
func TestArm64EnumVariantInTuple(t *testing.T) {
	src := `enum Color { Red, Green, Blue }
function main(): i32 {
    // payload-less variant, scalar-first to expose the offset
    var t: (i32, Color) = (1, Green);
    if (t.0 != 1) { return 1; }
    match (t.1) {
        Red => { return 2; },
        Green => { },
        Blue => { return 3; }
    }
    // payload-bearing variant constructor
    var u: (i32, Option[i32]) = (5, Some(42));
    if (u.0 != 5) { return 4; }
    match (u.1) {
        Some(v) => { if (v != 42) { return 5; } },
        None => { return 6; }
    }
    // variant first, scalar second
    var w: (Color, i32) = (Blue, 99);
    if (w.1 != 99) { return 7; }
    match (w.0) {
        Blue => { return 0; },
        _ => { return 8; }
    }
    return 9;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (enum variant in tuple slot)", code)
	}
}

// Map with a pointer-shaped value type (tuple / struct / array).
// `__map_get_impl` returns `Option[usize]`; the payload functions
// previously sized usize as 4 bytes, so on natives the 8-byte V
// pointer got truncated and mis-offset when wrapped in Some. The
// consumer read `Option[V]` at the pointer offset (8) → garbage →
// segfault. Fix: usize is pointer-width in payloadSlotSize /
// payloadStoreOpFor / payloadLoadOpFor, and `m.get` reboxes the
// helper's Option[usize] into a consumer-shaped Option[V].
func TestArm64MapPointerShapedValues(t *testing.T) {
	src := `
import "core/map";
struct P { x: i32, y: i32 }
function main(): i32 {
    // tuple value
    var mt: Map[string, (i32, i32)] = Map {};
    mt = mt.insert("a", (3, 4));
    match (mt.get("a")) {
        Some(p) => { if (p.0 + p.1 != 7) { return 1; } },
        None => { return 2; }
    }
    // struct value
    var ms: Map[string, P] = Map {};
    ms = ms.insert("a", P { x: 3, y: 4 });
    match (ms.get("a")) {
        Some(s) => { if (s.x + s.y != 7) { return 3; } },
        None => { return 4; }
    }
    // array value
    var ma: Map[i32, i32[]] = Map {};
    ma = ma.insert(1, [10, 20, 30]);
    match (ma.get(1)) {
        Some(arr) => { if (arr[0] + arr[2] != 40) { return 5; } },
        None => { return 6; }
    }
    // i32 value (regression guard — must still work after usize fix)
    var mi: Map[string, i32] = Map {};
    mi = mi.insert("a", 42);
    match (mi.get("a")) {
        Some(v) => { if (v != 42) { return 7; } },
        None => { return 8; }
    }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (Map pointer-shaped values)", code)
	}
}

// Chained field access where a struct field is tuple-typed:
// `r.pos.0`. targetTupleType only walked tuple-of-tuple; a
// struct-field-of-tuple target fell through to the struct path,
// fieldOwner returned "", and codegen errored with "field access
// on unresolved struct". Fix resolves the field's declared tuple
// type via exprType.
func TestArm64StructTupleFieldAccess(t *testing.T) {
	src := `struct Rec { pos: (i32, i32), name: string }
struct Nested { t: (i32, (i32, i32)) }
function main(): i32 {
    var r: Rec = Rec { pos: (3, 4), name: "p" };
    if (r.pos.0 != 3) { return 1; }
    if (r.pos.1 != 4) { return 2; }
    // deeper: struct field tuple containing a tuple
    var n: Nested = Nested { t: (1, (2, 3)) };
    if (n.t.0 != 1) { return 3; }
    if (n.t.1.0 != 2) { return 4; }
    if (n.t.1.1 != 3) { return 5; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (struct tuple-field chained access)", code)
	}
}

// Unsigned comparison condition codes. arm64 emitted signed
// `lt`/`le`/`gt`/`ge` for every `<`/`<=`/`>`/`>=`, ignoring the
// op's Unsigned flag — so a u32 like 4294967295 (bit 31 set, reads
// negative under signed compare) ordered wrong against small
// values. Fix uses `lo`/`ls`/`hi`/`hs` for unsigned operands.
// x86 / wasm were already correct, so this was a native-arm
// divergence.
func TestArm64UnsignedComparison(t *testing.T) {
	src := `function main(): i32 {
    var big: u32 = 4294967295u32;     // -1 if misread as signed
    if (!(big > 0u32)) { return 1; }
    if (!(big > 1000000u32)) { return 2; }
    if (big < 5u32) { return 3; }
    if (!(big >= 4294967295u32)) { return 4; }
    if (big <= 100u32) { return 5; }
    var b64: u64 = 18446744073709551615u64;
    if (!(b64 > 9u64)) { return 6; }
    if (b64 < 9u64) { return 7; }
    var u: u8 = 200u8;
    if (!(u > 100u8)) { return 8; }
    // loop bound driven by unsigned compare
    var i: u32 = 4294967293u32;
    var c: i32 = 0;
    while (i > 4294967290u32) { c = c + 1; i = i - 1u32; }
    if (c != 3) { return 9; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (unsigned comparison condition codes)", code)
	}
}

// Unary minus on non-i32 numeric types. Two bugs: (1) the checker
// rejected `-x` for any integer wider/narrower than i32 (requireNumber
// only matched the bare i32 NumberType); (2) OpFNeg was emitted with
// no width, so the backends took the f32 sign-flip path and corrupted
// f64 values (`-5.0` came out non-negative). The integer path also
// truncated i64 to a 32-bit `0 - x`. Fix: requireInteger in the
// checker, width-tagged OpFNeg / OpSub in the IR.
func TestArm64UnaryMinusWideTypes(t *testing.T) {
	src := `function main(): i32 {
    var a: i64 = -5i64;
    if (a != 0i64 - 5i64) { return 1; }
    var b: f64 = -5.0;
    if (!(b < 0.0)) { return 2; }
    var c: f64 = -b;            // negate an f64 value
    if (c != 5.0) { return 3; }
    var f: f32 = -2.5f32;
    if (!(f < 0.0f32)) { return 4; }
    // -0.0 keeps its sign bit (IEEE-754); f64_bits != 0
    var z: f64 = -0.0;
    if (f64_bits(z) == 0i64) { return 5; }
    // unary minus inside an arithmetic expression
    var g: i64 = 10i64 + -3i64;
    if (g != 7i64) { return 6; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (unary minus on wide / non-i32 types)", code)
	}
}

// Scientific-notation float literals end-to-end. The lexer now
// accepts `[eE][+-]?digits` exponents; this confirms the values
// parse and compute correctly through codegen.
func TestArm64ScientificNotation(t *testing.T) {
	src := `function main(): i32 {
    var a: f64 = 1e3;
    if (a != 1000.0) { return 1; }
    var b: f64 = 1.5e3;
    if (b != 1500.0) { return 2; }
    var c: f64 = 1500.0e-3;
    if (c != 1.5) { return 3; }
    var d: f64 = 1.5e+3;
    if (d != 1500.0) { return 4; }
    var e: f64 = 2.5E2;
    if (e != 250.0) { return 5; }
    var f: f32 = 1.5e2f32;
    if (f != 150.0f32) { return 6; }
    var big: f64 = 1.8e19;
    if (!(big > 1.7e19)) { return 7; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (scientific-notation float literals)", code)
	}
}

// Sub-i32 arithmetic wraps to the declared width. `+` `-` `*` `<<`
// can push a u8 result past its width; scalar locals and struct
// fields store full-width (only array elements narrow via store8),
// so the result must be masked (unsigned) or sign-extended (signed)
// back to width. Otherwise a `u8` var would hold 256 and downstream
// casts/compares (which assume "every store narrows") would misread
// it.
func TestArm64SubI32ArithmeticWraps(t *testing.T) {
	src := `struct S { v: u8 }
function main(): i32 {
    var a: u8 = 255u8;
    a = a + 1u8;
    if ((a as i32) != 0) { return 1; }          // unsigned add wrap
    var b: u8 = 0u8;
    b = b - 1u8;
    if ((b as i32) != 255) { return 2; }         // unsigned sub underflow
    var c: u8 = 16u8;
    c = c * 16u8;
    if ((c as i32) != 0) { return 3; }           // mul (strength-reduced) wrap
    var s: S = S { v: 200u8 };
    var h: u8 = s.v + 100u8;
    if ((h as i32) != 44) { return 4; }          // field operand wrap
    // in-range arithmetic is unaffected
    var k: u8 = 100u8;
    k = k + 50u8;
    if ((k as i32) != 150) { return 5; }
    return 0;
}`
	if _, code := compileAndRunArm64(t, src); code != 0 {
		t.Errorf("got exit %d, want 0 (sub-i32 arithmetic wraps to width)", code)
	}
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
