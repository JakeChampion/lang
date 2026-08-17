package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// genericASTFoldIRCases pin the shape `astwalk.fern` adopted in #6993: a
// GENERIC fold over a recursive union (the AST node type), parameterised by a
// fn-typed callback, with the callback supplied as a nested named function that
// CAPTURES the caller's context.
//
// Nothing covered this before. The self-host compiler's own sources used no
// first-class functions at all, so the fixpoint — which only ever proves the
// compiler reproduces itself — had no traversal of this shape to reproduce, and
// `internal/e2eselfhost` is the gate that runs programs the compiler does not
// contain. These are those programs.
//
// Three properties are load-bearing and each has a case:
//   - the callback is threaded through MUTUAL recursion (expr half ↔ stmt
//     half), so the fn value survives being passed on rather than only being
//     called where it was bound;
//   - the visitor's CAPTURE decides the answer, so a lowering that dropped the
//     env and resolved the name against the module would produce a different
//     number rather than the same one by luck;
//   - the accumulator is a heap value (array / struct array), so the fold's
//     thread-through is exercised against rc rather than a scalar.
//
// runCaptureStrictIR rather than runCapture: a per-function bail reaches these
// answers too, so an exit code alone cannot show the shape stayed on the IR
// path (#6602).
var genericASTFoldIRCases = []struct {
	name string
	src  string
	exit int
}{
	// The baseline: a generic fold whose callback is a top-level function, no
	// capture involved. Separates "generic + fn-typed param" from "closure".
	{"generic-fold-top-level-visitor", `struct ENum { v: i32 }
struct EAdd { left: Expr, right: Expr }
type Expr = ENum | EAdd;

function fold_expr[T](e: Expr, acc: T, visit: (Expr, T) => T): T {
    acc = visit(e, acc);
    match (e) {
        ENum(_) => { return acc; },
        EAdd(b) => {
            acc = fold_expr(b.left, acc, visit);
            return fold_expr(b.right, acc, visit);
        }
    }
    return acc;
}

function count_node(e: Expr, acc: i32): i32 { return acc + 1i32; }

function main(): i32 {
    var e: Expr = EAdd { left: ENum { v: 1i32 }, right: EAdd { left: ENum { v: 2i32 }, right: ENum { v: 3i32 } } };
    return fold_expr(e, 0i32, count_node);
}`, 5},
	// The adopted shape: the visitor is a nested named function closing over
	// `want`, and the two calls differ only in the captured value — so a
	// dropped capture cannot produce both answers.
	{"capturing-visitor-decides-the-answer", `struct ENum { v: i32 }
struct EAdd { left: Expr, right: Expr }
type Expr = ENum | EAdd;

function fold_expr[T](e: Expr, acc: T, visit: (Expr, T) => T): T {
    acc = visit(e, acc);
    match (e) {
        ENum(_) => { return acc; },
        EAdd(b) => {
            acc = fold_expr(b.left, acc, visit);
            return fold_expr(b.right, acc, visit);
        }
    }
    return acc;
}

function count_over(e: Expr, want: i32): i32 {
    function hit(n: Expr, acc: i32): i32 {
        match (n) {
            ENum(x) => {
                if (x.v > want) { return acc + 1i32; }
                return acc;
            },
            _ => { return acc; }
        }
        return acc;
    }
    return fold_expr(e, 0i32, hit);
}

function main(): i32 {
    var e: Expr = EAdd { left: ENum { v: 1i32 }, right: EAdd { left: ENum { v: 5i32 }, right: ENum { v: 9i32 } } };
    return count_over(e, 0i32) * 10i32 + count_over(e, 5i32);
}`, 31},
	// The callback crosses a mutual recursion (expr half ↔ stmt half) and the
	// accumulator is a `string[]`, which is what astwalk's own folds do. The
	// visited tree hides one match inside a lambda body, so the stmt half has
	// to be reached through an expression node.
	{"callback-through-mutual-recursion", `struct ENum { v: i32 }
struct EIdent { name: string }
struct ECall { callee: Expr, args: Expr[] }
struct ELambda { body: Stmt[] }
type Expr = ENum | EIdent | ECall | ELambda;

struct SExpr { value: Expr }
struct SReturn { value: Expr }
type Stmt = SExpr | SReturn;

function fold_expr[T](e: Expr, acc: T, visit: (Expr, T) => T): T {
    acc = visit(e, acc);
    match (e) {
        ENum(_) => { return acc; },
        EIdent(_) => { return acc; },
        ECall(c) => {
            acc = fold_expr(c.callee, acc, visit);
            var i: i32 = 0;
            while (i < c.args.len()) {
                acc = fold_expr(c.args[i], acc, visit);
                i = i + 1;
            }
            return acc;
        },
        ELambda(lm) => {
            var i: i32 = 0;
            while (i < lm.body.len()) {
                acc = fold_stmt(lm.body[i], acc, visit);
                i = i + 1;
            }
            return acc;
        }
    }
    return acc;
}

function fold_stmt[T](st: Stmt, acc: T, visit: (Expr, T) => T): T {
    match (st) {
        SExpr(s) => { return fold_expr(s.value, acc, visit); },
        SReturn(r) => { return fold_expr(r.value, acc, visit); }
    }
    return acc;
}

function collect(body: Stmt[], want: string): string[] {
    function hit(e: Expr, acc: string[]): string[] {
        match (e) {
            ECall(c) => {
                match (c.callee) {
                    EIdent(id) => {
                        if (id.name == want) { return acc.append(id.name); }
                        return acc;
                    },
                    _ => { return acc; }
                }
            },
            _ => { return acc; }
        }
        return acc;
    }
    var acc: string[] = [];
    var i: i32 = 0;
    while (i < body.len()) {
        acc = fold_stmt(body[i], acc, hit);
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    var inner: Stmt[] = [SReturn { value: ECall { callee: EIdent { name: "open" }, args: [ENum { v: 1i32 }] } }];
    var body: Stmt[] = [
        SExpr { value: ECall { callee: EIdent { name: "open" }, args: [ECall { callee: EIdent { name: "read" }, args: [] }] } },
        SExpr { value: ELambda { body: inner } }
    ];
    return collect(body, "open").len() * 10i32 + collect(body, "read").len();
}`, 21},
	// The accumulator is a STRUCT array built inside the callback — astwalk's
	// `CallSite[]` — so the fold threads a heap value the visitor grows.
	{"struct-array-accumulator", `struct ENum { v: i32 }
struct EAdd { left: Expr, right: Expr }
type Expr = ENum | EAdd;

struct Hit { v: i32 }

function fold_expr[T](e: Expr, acc: T, visit: (Expr, T) => T): T {
    acc = visit(e, acc);
    match (e) {
        ENum(_) => { return acc; },
        EAdd(b) => {
            acc = fold_expr(b.left, acc, visit);
            return fold_expr(b.right, acc, visit);
        }
    }
    return acc;
}

function hits_over(e: Expr, want: i32): Hit[] {
    function hit(n: Expr, acc: Hit[]): Hit[] {
        match (n) {
            ENum(x) => {
                if (x.v > want) { return acc.append(Hit { v: x.v }); }
                return acc;
            },
            _ => { return acc; }
        }
        return acc;
    }
    var seed: Hit[] = [];
    return fold_expr(e, seed, hit);
}

function main(): i32 {
    var e: Expr = EAdd { left: ENum { v: 2i32 }, right: EAdd { left: ENum { v: 7i32 }, right: ENum { v: 4i32 } } };
    var hs: Hit[] = hits_over(e, 3i32);
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < hs.len()) { sum = sum + hs[i].v; i = i + 1; }
    return sum + hs.len();
}`, 13},
}

// TestSelfHostGenericASTFoldIRX86_64 drives the production x86-64 IR path.
func TestSelfHostGenericASTFoldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range genericASTFoldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostGenericASTFoldWasmIR — the wasm leg. A funcref type is
// STRUCTURAL there, so a callback dispatched through a type its signature does
// not match traps rather than taking a slow path; the register backends cannot
// see that.
func TestSelfHostGenericASTFoldWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host generic-AST-fold wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range genericASTFoldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(wat) == 0 {
				t.Fatal("self-host wasm compiler emitted 0 bytes")
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watFile)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := cmd.ProcessState.ExitCode(); got != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}

// TestSelfHostGenericASTFoldIRArm64 — the arm64 counterpart. The lowering is
// shared, so arm64 picks it up unchanged; running it is what proves that.
func TestSelfHostGenericASTFoldIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 generic-AST-fold gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range genericASTFoldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
