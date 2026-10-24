package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var genericASTFoldIRCases = []struct {
	name string
	src  string
	exit int
}{
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
    let e: Expr = EAdd { left: ENum { v: 1i32 }, right: EAdd { left: ENum { v: 2i32 }, right: ENum { v: 3i32 } } };
    return fold_expr(e, 0i32, count_node);
}`, 5},
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
    let e: Expr = EAdd { left: ENum { v: 1i32 }, right: EAdd { left: ENum { v: 5i32 }, right: ENum { v: 9i32 } } };
    return count_over(e, 0i32) * 10i32 + count_over(e, 5i32);
}`, 31},
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
            let i: i32 = 0;
            while (i < c.args.len()) {
                acc = fold_expr(c.args[i], acc, visit);
                i = i + 1;
            }
            return acc;
        },
        ELambda(lm) => {
            let i: i32 = 0;
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
    let acc: string[] = [];
    let i: i32 = 0;
    while (i < body.len()) {
        acc = fold_stmt(body[i], acc, hit);
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    let inner: Stmt[] = [SReturn { value: ECall { callee: EIdent { name: "open" }, args: [ENum { v: 1i32 }] } }];
    let body: Stmt[] = [
        SExpr { value: ECall { callee: EIdent { name: "open" }, args: [ECall { callee: EIdent { name: "read" }, args: [] }] } },
        SExpr { value: ELambda { body: inner } }
    ];
    return collect(body, "open").len() * 10i32 + collect(body, "read").len();
}`, 21},
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
    let seed: Hit[] = [];
    return fold_expr(e, seed, hit);
}

function main(): i32 {
    let e: Expr = EAdd { left: ENum { v: 2i32 }, right: EAdd { left: ENum { v: 7i32 }, right: ENum { v: 4i32 } } };
    let hs: Hit[] = hits_over(e, 3i32);
    let sum: i32 = 0;
    let i: i32 = 0;
    while (i < hs.len()) { sum = sum + hs[i].v; i = i + 1; }
    return sum + hs.len();
}`, 13},

	{"pruned-fold-descent-decides-the-answer", `struct ENum { v: i32 }
struct EAdd { left: Expr, right: Expr }
struct ELam { body: Expr }
type Expr = ENum | EAdd | ELam;

function descend_all(e: Expr): boolean { return true; }

function not_lambda(e: Expr): boolean {
    match (e) {
        ELam(_) => { return false; },
        _ => { return true; }
    }
    return true;
}

function fold_expr[T](e: Expr, acc: T, visit: (Expr, T) => T): T {
    return fold_expr_pruned(e, acc, visit, descend_all);
}

function fold_expr_pruned[T](e: Expr, acc: T, visit: (Expr, T) => T, descend: (Expr) => boolean): T {
    acc = visit(e, acc);
    if (!descend(e)) { return acc; }
    match (e) {
        ENum(_) => { return acc; },
        EAdd(nd) => {
            acc = fold_expr_pruned(nd.left, acc, visit, descend);
            return fold_expr_pruned(nd.right, acc, visit, descend);
        },
        ELam(nd) => { return fold_expr_pruned(nd.body, acc, visit, descend); }
    }
    return acc;
}

function sum_num(e: Expr, acc: i32): i32 {
    match (e) {
        ENum(x) => { return acc + x.v; },
        _ => { return acc; }
    }
    return acc;
}

function main(): i32 {
    let e: Expr = EAdd { left: ENum { v: 3i32 }, right: ELam { body: ENum { v: 4i32 } } };
    return fold_expr(e, 0i32, sum_num) * 10i32 + fold_expr_pruned(e, 0i32, sum_num, not_lambda);
}`, 73},
	{"prune-computes-its-own-subtree", `struct EIdent { name: string }
struct EAdd { left: Expr, right: Expr }
struct ELam { param: string, body: Expr }
type Expr = EIdent | EAdd | ELam;

function descend_all(e: Expr): boolean { return true; }

function not_lambda(e: Expr): boolean {
    match (e) {
        ELam(_) => { return false; },
        _ => { return true; }
    }
    return true;
}

function fold_expr[T](e: Expr, acc: T, visit: (Expr, T) => T): T {
    return fold_expr_pruned(e, acc, visit, descend_all);
}

function fold_expr_pruned[T](e: Expr, acc: T, visit: (Expr, T) => T, descend: (Expr) => boolean): T {
    acc = visit(e, acc);
    if (!descend(e)) { return acc; }
    match (e) {
        EIdent(_) => { return acc; },
        EAdd(nd) => {
            acc = fold_expr_pruned(nd.left, acc, visit, descend);
            return fold_expr_pruned(nd.right, acc, visit, descend);
        },
        ELam(nd) => { return fold_expr_pruned(nd.body, acc, visit, descend); }
    }
    return acc;
}

function free_of(e: Expr, acc: string[]): string[] {
    match (e) {
        EIdent(id) => { return acc.append(id.name); },
        ELam(lm) => {
            let inner: string[] = free_vars(lm.body);
            let i: i32 = 0;
            while (i < inner.len()) {
                if (inner[i] != lm.param) { acc = acc.append(inner[i]); }
                i = i + 1;
            }
            return acc;
        },
        _ => { return acc; }
    }
    return acc;
}

function free_vars(e: Expr): string[] {
    let seed: string[] = [];
    return fold_expr_pruned(e, seed, free_of, not_lambda);
}

function main(): i32 {
    let lam: Expr = ELam { param: "y", body: EAdd { left: EIdent { name: "x" }, right: EIdent { name: "y" } } };
    let e: Expr = EAdd { left: EIdent { name: "z" }, right: lam };
    let seed: string[] = [];
    return free_vars(e).len() * 10i32 + fold_expr(e, seed, free_of).len();
}`, 24},
	{"prune-with-a-capturing-visitor", `struct EIdent { name: string }
struct EAdd { left: Expr, right: Expr }
struct ELam { body: Expr }
type Expr = EIdent | EAdd | ELam;

function descend_all(e: Expr): boolean { return true; }

function not_lambda(e: Expr): boolean {
    match (e) {
        ELam(_) => { return false; },
        _ => { return true; }
    }
    return true;
}

function fold_expr[T](e: Expr, acc: T, visit: (Expr, T) => T): T {
    return fold_expr_pruned(e, acc, visit, descend_all);
}

function fold_expr_pruned[T](e: Expr, acc: T, visit: (Expr, T) => T, descend: (Expr) => boolean): T {
    acc = visit(e, acc);
    if (!descend(e)) { return acc; }
    match (e) {
        EIdent(_) => { return acc; },
        EAdd(nd) => {
            acc = fold_expr_pruned(nd.left, acc, visit, descend);
            return fold_expr_pruned(nd.right, acc, visit, descend);
        },
        ELam(nd) => { return fold_expr_pruned(nd.body, acc, visit, descend); }
    }
    return acc;
}

function count_named(e: Expr, want: string): i32 {
    function hit(n: Expr, acc: i32): i32 {
        match (n) {
            EIdent(id) => {
                if (id.name == want) { return acc + 1i32; }
                return acc;
            },
            _ => { return acc; }
        }
        return acc;
    }
    return fold_expr_pruned(e, 0i32, hit, not_lambda);
}

function count_named_all(e: Expr, want: string): i32 {
    function hit(n: Expr, acc: i32): i32 {
        match (n) {
            EIdent(id) => {
                if (id.name == want) { return acc + 1i32; }
                return acc;
            },
            _ => { return acc; }
        }
        return acc;
    }
    return fold_expr(e, 0i32, hit);
}

function main(): i32 {
    let e: Expr = EAdd { left: EIdent { name: "x" }, right: EAdd { left: EIdent { name: "x" }, right: ELam { body: EIdent { name: "y" } } } };
    return count_named(e, "x") * 10i32 + count_named_all(e, "y") * 3i32 + count_named(e, "y");
}`, 23},
	{"three-instantiations-sharing-a-binding-name", `struct ENum { v: i32 }
struct EStr { s: string }
struct EAdd { left: Expr, right: Expr }
type Expr = ENum | EStr | EAdd;

struct Hit { v: i32 }

function descend_all(e: Expr): boolean { return true; }

function fold_expr[T](e: Expr, acc: T, visit: (Expr, T) => T): T {
    return fold_expr_pruned(e, acc, visit, descend_all);
}

function fold_expr_pruned[T](e: Expr, acc: T, visit: (Expr, T) => T, descend: (Expr) => boolean): T {
    acc = visit(e, acc);
    if (!descend(e)) { return acc; }
    match (e) {
        ENum(nd) => { return acc; },
        EStr(nd) => { return acc; },
        EAdd(nd) => {
            acc = fold_expr_pruned(nd.left, acc, visit, descend);
            return fold_expr_pruned(nd.right, acc, visit, descend);
        }
    }
    return acc;
}

function count_node(e: Expr, acc: i32): i32 { return acc + 1i32; }

function name_of(e: Expr, acc: string[]): string[] {
    match (e) {
        EStr(nd) => { return acc.append(nd.s); },
        _ => { return acc; }
    }
    return acc;
}

function hit_of(e: Expr, acc: Hit[]): Hit[] {
    match (e) {
        ENum(nd) => { return acc.append(Hit { v: nd.v }); },
        _ => { return acc; }
    }
    return acc;
}

function main(): i32 {
    let e: Expr = EAdd { left: ENum { v: 5i32 }, right: EStr { s: "a" } };
    let s0: string[] = [];
    let h0: Hit[] = [];
    let ns: string[] = fold_expr(e, s0, name_of);
    let hs: Hit[] = fold_expr(e, h0, hit_of);
    return fold_expr(e, 0i32, count_node) * 10i32 + ns.len() * 2i32 + hs[0].v;
}`, 37},

	{"stmt-visitor-and-expr-visitor-in-one-walk", `struct ENum { v: i32 }
struct EIdent { name: string }
struct EAdd { left: Expr, right: Expr }
struct ELambda { body: Stmt[] }
type Expr = ENum | EIdent | EAdd | ELambda;

struct SVar { name: string, init: Expr }
struct SExpr { value: Expr }
struct SAssign { target: string, value: Expr }
type Stmt = SVar | SExpr | SAssign;

function descend_all(e: Expr): boolean { return true; }
function descend_none(e: Expr): boolean { return false; }

function fold_expr_nodes[T](e: Expr, acc: T, visit_stmt: (Stmt, T) => T, visit_expr: (Expr, T) => T, descend: (Expr) => boolean): T {
    acc = visit_expr(e, acc);
    if (!descend(e)) { return acc; }
    match (e) {
        ENum(_) => { return acc; },
        EIdent(_) => { return acc; },
        EAdd(b) => {
            acc = fold_expr_nodes(b.left, acc, visit_stmt, visit_expr, descend);
            return fold_expr_nodes(b.right, acc, visit_stmt, visit_expr, descend);
        },
        ELambda(lm) => {
            let i: i32 = 0;
            while (i < lm.body.len()) {
                acc = fold_stmt_nodes(lm.body[i], acc, visit_stmt, visit_expr, descend);
                i = i + 1;
            }
            return acc;
        }
    }
    return acc;
}

function fold_stmt_nodes[T](st: Stmt, acc: T, visit_stmt: (Stmt, T) => T, visit_expr: (Expr, T) => T, descend: (Expr) => boolean): T {
    acc = visit_stmt(st, acc);
    match (st) {
        SVar(v) => { return fold_expr_nodes(v.init, acc, visit_stmt, visit_expr, descend); },
        SExpr(s) => { return fold_expr_nodes(s.value, acc, visit_stmt, visit_expr, descend); },
        SAssign(a) => { return fold_expr_nodes(a.value, acc, visit_stmt, visit_expr, descend); }
    }
    return acc;
}

function reads_of(e: Expr, acc: string[]): string[] {
    match (e) {
        EIdent(id) => { return acc.append(id.name); },
        _ => { return acc; }
    }
    return acc;
}

function target_of(st: Stmt, acc: string[]): string[] {
    match (st) {
        SAssign(a) => { return acc.append(a.target); },
        _ => { return acc; }
    }
    return acc;
}

function binders_of(st: Stmt, acc: string[]): string[] {
    match (st) {
        SVar(v) => { return acc.append(v.name); },
        _ => { return acc; }
    }
    return acc;
}

function binds_nothing(e: Expr, acc: string[]): string[] { return acc; }

function reads(body: Stmt[]): string[] {
    let acc: string[] = [];
    let i: i32 = 0;
    while (i < body.len()) { acc = fold_stmt_nodes(body[i], acc, target_of, reads_of, descend_all); i = i + 1; }
    return acc;
}

function binders(body: Stmt[], into_lambdas: boolean): string[] {
    let acc: string[] = [];
    let i: i32 = 0;
    while (i < body.len()) {
        if (into_lambdas) { acc = fold_stmt_nodes(body[i], acc, binders_of, binds_nothing, descend_all); }
        else { acc = fold_stmt_nodes(body[i], acc, binders_of, binds_nothing, descend_none); }
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    let inner: Stmt[] = [SVar { name: "q", init: EIdent { name: "w" } }];
    let body: Stmt[] = [
        SVar { name: "x", init: ENum { v: 1i32 } },
        SAssign { target: "y", value: EAdd { left: EIdent { name: "x" }, right: EIdent { name: "z" } } },
        SExpr { value: ELambda { body: inner } }
    ];
    return reads(body).len() * 20i32 + binders(body, true).len() * 5i32 + binders(body, false).len();
}`, 91},
	{"no-op-stmt-visitor-through-the-generic-wrappers", `struct ENum { v: i32 }
struct EIdent { name: string }
struct EAdd { left: Expr, right: Expr }
type Expr = ENum | EIdent | EAdd;

struct SExpr { value: Expr }
struct SRet { value: Expr }
type Stmt = SExpr | SRet;

struct Hit { v: i32 }

function descend_all(e: Expr): boolean { return true; }

function fold_expr_nodes[T](e: Expr, acc: T, visit_stmt: (Stmt, T) => T, visit_expr: (Expr, T) => T, descend: (Expr) => boolean): T {
    acc = visit_expr(e, acc);
    if (!descend(e)) { return acc; }
    match (e) {
        ENum(_) => { return acc; },
        EIdent(_) => { return acc; },
        EAdd(b) => {
            acc = fold_expr_nodes(b.left, acc, visit_stmt, visit_expr, descend);
            return fold_expr_nodes(b.right, acc, visit_stmt, visit_expr, descend);
        }
    }
    return acc;
}

function fold_stmt_nodes[T](st: Stmt, acc: T, visit_stmt: (Stmt, T) => T, visit_expr: (Expr, T) => T, descend: (Expr) => boolean): T {
    acc = visit_stmt(st, acc);
    match (st) {
        SExpr(s) => { return fold_expr_nodes(s.value, acc, visit_stmt, visit_expr, descend); },
        SRet(r) => { return fold_expr_nodes(r.value, acc, visit_stmt, visit_expr, descend); }
    }
    return acc;
}

function fold_stmt_pruned[T](st: Stmt, acc: T, visit: (Expr, T) => T, descend: (Expr) => boolean): T {
    return fold_stmt_nodes(st, acc, (s: Stmt, a: T) => a, visit, descend);
}

function fold_stmt[T](st: Stmt, acc: T, visit: (Expr, T) => T): T {
    return fold_stmt_pruned(st, acc, visit, descend_all);
}

function count_node(e: Expr, acc: i32): i32 { return acc + 1i32; }
function name_of(e: Expr, acc: string[]): string[] {
    match (e) { EIdent(id) => { return acc.append(id.name); }, _ => { return acc; } }
    return acc;
}
function hit_of(e: Expr, acc: Hit[]): Hit[] {
    match (e) { ENum(n) => { return acc.append(Hit { v: n.v }); }, _ => { return acc; } }
    return acc;
}

function main(): i32 {
    let st: Stmt = SRet { value: EAdd { left: EIdent { name: "a" }, right: EAdd { left: ENum { v: 2i32 }, right: ENum { v: 3i32 } } } };
    let seed_names: string[] = [];
    let seed_hits: Hit[] = [];
    let nodes: i32 = fold_stmt(st, 0i32, count_node);
    let names: string[] = fold_stmt(st, seed_names, name_of);
    let hits: Hit[] = fold_stmt(st, seed_hits, hit_of);
    let sum: i32 = 0;
    let i: i32 = 0;
    while (i < hits.len()) { sum = sum + hits[i].v; i = i + 1; }
    return nodes * 10i32 + names.len() * 5i32 + sum;
}`, 60},
}

func TestSelfHostGenericASTFoldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range genericASTFoldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
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

func TestSelfHostGenericASTFoldWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host generic-AST-fold wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range genericASTFoldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
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

func TestSelfHostGenericASTFoldIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 generic-AST-fold gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range genericASTFoldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
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
