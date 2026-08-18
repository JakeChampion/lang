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
	// --- descent control (#6993 slice two) ----------------------------------
	//
	// `astwalk.fold_expr` is now a thin wrapper over a `fold_expr_pruned` that
	// takes a SECOND fn-typed parameter deciding whether a visited node's
	// children are walked. Three things about that shape are new and none of
	// them are covered by the cases above: a generic forwarding a fn value to
	// another generic, two fn-typed parameters live in one call, and a
	// predicate whose answer changes the result.
	//
	// The prune is a plain `(Expr) => boolean` rather than a `(T, boolean)`
	// return or a `Visit[T]` box because it is asked once per AST node, and a
	// boxed answer would be an allocation per node on the compiler's hottest
	// walk.

	// The predicate decides the answer: the same tree and the same visitor,
	// folded once unpruned and once pruned at the lambda. A `descend` that was
	// ignored, or whose value was dropped crossing the generic boundary, gives
	// the same number twice and cannot produce 73.
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
    var e: Expr = EAdd { left: ENum { v: 3i32 }, right: ELam { body: ENum { v: 4i32 } } };
    return fold_expr(e, 0i32, sum_num) * 10i32 + fold_expr_pruned(e, 0i32, sum_num, not_lambda);
}`, 73},
	// astwalk's `collect_idents_expr` shape exactly: the visitor's contribution
	// for a lambda is computed by folding the lambda's own body — mutual
	// recursion between a top-level visitor and the generic fold — and the prune
	// is what stops the walk re-adding, at the outer level, the very names that
	// subtraction removed. Pruned reports 2 free variables (`z`, `x`); the
	// unpruned fold over the identical visitor reports 4, `y` among them, which
	// is the capture bug the arm exists to prevent.
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
            var inner: string[] = free_vars(lm.body);
            var i: i32 = 0;
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
    var seed: string[] = [];
    return fold_expr_pruned(e, seed, free_of, not_lambda);
}

function main(): i32 {
    var lam: Expr = ELam { param: "y", body: EAdd { left: EIdent { name: "x" }, right: EIdent { name: "y" } } };
    var e: Expr = EAdd { left: EIdent { name: "z" }, right: lam };
    var seed: string[] = [];
    return free_vars(e).len() * 10i32 + fold_expr(e, seed, free_of).len();
}`, 24},
	// Prune and CAPTURE composed: a nested capturing visitor and a top-level
	// prune predicate reach the same call. Two `want` values through the same
	// fold prove the capture is live (2 and 0 — a dropped env cannot give both),
	// and the unpruned leg proves the prune is (`y` is 1 there, 0 here).
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
    var e: Expr = EAdd { left: EIdent { name: "x" }, right: EAdd { left: EIdent { name: "x" }, right: ELam { body: EIdent { name: "y" } } } };
    return count_named(e, "x") * 10i32 + count_named_all(e, "y") * 3i32 + count_named(e, "y");
}`, 23},
	// The monomorphisation profile astwalk actually has: ONE generic fold, three
	// instantiations (i32, string[], struct-array), and a match binding the SAME
	// NAME in every arm — fold_expr's `ExprSlice` and `ExprStructLit` arms both
	// bind `sl`. Native mis-lowered that combination, its clone sharing one
	// pattern-binding array across instantiations while each got its own body;
	// this leg holds the self-host compiler to the same standard.
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
    var e: Expr = EAdd { left: ENum { v: 5i32 }, right: EStr { s: "a" } };
    var s0: string[] = [];
    var h0: Hit[] = [];
    var ns: string[] = fold_expr(e, s0, name_of);
    var hs: Hit[] = fold_expr(e, h0, hit_of);
    return fold_expr(e, 0i32, count_node) * 10i32 + ns.len() * 2i32 + hs[0].v;
}`, 37},
	// --- the statement visitor (#6993 slice three) --------------------------
	//
	// The expression visitor cannot express every traversal: an assignment's
	// TARGET and a `var` / `for` / match-arm BINDER are names carried on the
	// STATEMENT, so a consumer contributing one is never handed a node for it.
	// astwalk grew fold_expr_nodes / fold_stmt_nodes taking a second visitor
	// for those, and the older folds became wrappers passing a no-op.

	// Both visitors run in one walk and each decides part of the answer: the
	// assign target reaches the reads set only through the STATEMENT visitor,
	// and the two binder folds differ only in their descent rule, so a walk
	// that dropped either cannot produce 91.
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
            var i: i32 = 0;
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
    var acc: string[] = [];
    var i: i32 = 0;
    while (i < body.len()) { acc = fold_stmt_nodes(body[i], acc, target_of, reads_of, descend_all); i = i + 1; }
    return acc;
}

function binders(body: Stmt[], into_lambdas: boolean): string[] {
    var acc: string[] = [];
    var i: i32 = 0;
    while (i < body.len()) {
        if (into_lambdas) { acc = fold_stmt_nodes(body[i], acc, binders_of, binds_nothing, descend_all); }
        else { acc = fold_stmt_nodes(body[i], acc, binders_of, binds_nothing, descend_none); }
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    var inner: Stmt[] = [SVar { name: "q", init: EIdent { name: "w" } }];
    var body: Stmt[] = [
        SVar { name: "x", init: ENum { v: 1i32 } },
        SAssign { target: "y", value: EAdd { left: EIdent { name: "x" }, right: EIdent { name: "z" } } },
        SExpr { value: ELambda { body: inner } }
    ];
    return reads(body).len() * 20i32 + binders(body, true).len() * 5i32 + binders(body, false).len();
}`, 91},
	// The layering astwalk itself now has: a 3-arg fold over a 4-arg pruned
	// fold over the 5-arg walk, with the no-op statement visitor spelled as an
	// arrow lambda mentioning the enclosing generic's own T, at three
	// instantiations (i32 / string[] / a struct array).
	//
	// That lambda is why this case has to RUN, and on wasm especially. Its
	// trampoline is a bare-typevar pass-through, so it took the #5464 erased
	// widening and came out (param i64) (result i64) against the arity-keyed
	// all-i32 call_indirect type: it compiled clean under FERN_STRICT_IR and
	// trapped with an indirect-call type mismatch at the first call.
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
    var st: Stmt = SRet { value: EAdd { left: EIdent { name: "a" }, right: EAdd { left: ENum { v: 2i32 }, right: ENum { v: 3i32 } } } };
    var seed_names: string[] = [];
    var seed_hits: Hit[] = [];
    var nodes: i32 = fold_stmt(st, 0i32, count_node);
    var names: string[] = fold_stmt(st, seed_names, name_of);
    var hits: Hit[] = fold_stmt(st, seed_hits, hit_of);
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < hits.len()) { sum = sum + hits[i].v; i = i + 1; }
    return nodes * 10i32 + names.len() * 5i32 + sum;
}`, 60},
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
