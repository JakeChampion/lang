package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The checked-source producer (semsource.fern) is the typed frontend import of
// the self-hosted pre-RC pipeline: checked syntax in, verified semantic values
// out. The print driver pins the produced graphs, types and parameter modes;
// the executable driver runs produced functions through the unit planner and
// physical RC lowering inside an otherwise AST-lowered program on every target.

const semsourcePrintFixture = `
function alias(xs: i32[][], own ys: i32[]): i32[] {
    var a: i32[] = xs[0];
    var b: i32[] = a;
    if (ys[0] > 0) { b = ys; }
    return b;
}
function shadow(n: i32): i32 {
    var n: i32 = n + 1;
    { var n: i32 = n * 2; }
    return n;
}
function loop_phi(limit: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < limit) {
        i = i + 1;
        if (i == 3) { continue; }
        if (total > 10) { break; }
        total = total + i;
    }
    return total;
}
function short_circuit(a: boolean, b: i32): boolean {
    return a && b > 0 || !a;
}
function nested(rows: i32[][]): (i32, i32[]) {
    var t: (i32, i32[]) = (rows[0][1], rows[1]);
    var e: i32[] = [];
    if (t.0 == 0) { return (0, e); }
    return t;
}
function view(s: string): string { return s; }
function refused_call(n: i32): i32 { return abs(n); }
function refused_literal(): f64 { return 1.5; }
function refused_width(n: i64): i64 { return n + 1; }
function refused_fallthrough(n: i32): i32 { if (n > 0) { return 1; } }
function refused_destructure(): i32 { var (a, b) = (1, 2); return a + b; }
function refused_division(n: i32): i32 { return n / 2; }
function refused_global(): i32 { return loop_phi(2); }
function callee(xs: i32[], own ys: i32[]): i32[] { return ys; }
function caller(n: i32): i32[] {
    var a: i32[] = [n];
    var b: i32[] = callee(a, [n, n]);
    callee(b, a);
    return callee(b, b);
}
function noop() { return; }
function refused_void_call(): i32 { noop(); return 1; }
function refused_method_call(xs: i32[]): i32 { return xs.len(); }
function refused_transitive(n: i32): i32 { return refused_call(n); }
struct P { n: i32, xs: i32[] }
struct Q { name: string, p: P }
struct G[T] { v: T }
function make(n: i32): P { return P { n: n, xs: [n, n + 1] }; }
function wrap(own p: P, tag: string): Q { return Q { name: tag + "!", p: p }; }
function unwrap(q: Q): i32 {
    var p: P = q.p;
    if (q.name == "x!") { return p.xs[0]; }
    return p.n;
}
function greet(s: string): string { return "hello, " + s; }
function refused_update(p: P): P { return P { ...p, n: 0 }; }
function refused_generic_record(n: i32): i32 { var g: G[i32] = G { v: n }; return g.v; }
function refused_string_method(s: string): i32 { return s.len(); }
enum Shape { Dot, Line(i32), Full(i32[]), Pair(i32, i32[]) }
function shape(n: i32): Shape {
    if (n == 0) { return Dot; }
    if (n == 1) { return Line(n); }
    if (n == 2) { return Full([n, n + 1]); }
    return Pair(n, [n]);
}
function measure(s: Shape): i32 {
    match (s) {
        Dot => { return 0; },
        Line(n) => { return n; },
        Pair(_, ys) => { return ys[0]; },
        _ => {}
    }
    var total: i32 = 1;
    if let Full(xs) = s { total = total + xs[0]; }
    return total;
}
function refused_guard(s: Shape): i32 {
    match (s) {
        Line(n) when n > 0 => { return n; },
        _ => { return 0; }
    }
}
function refused_qualified(s: Shape): i32 {
    match (s) {
        Shape.Dot => { return 1; },
        _ => { return 0; }
    }
}
struct Leaf { n: i32 }
struct Twig { xs: i32[] }
type Node = Leaf | Twig;
struct Nest { kid: Tree, n: i32 }
struct Tip { n: i32 }
type Tree = Nest | Tip;
function mk_node(n: i32): Node {
    if (n == 0) { return Leaf { n: 7 }; }
    return Twig { xs: [n, n + 1] };
}
function node_size(nd: Node): i32 {
    match (nd) {
        Leaf(l) => { return l.n; },
        Twig(t) => { return t.xs[1]; }
    }
    return 0 - 1;
}
function recursive_union(t: Tree): i32 {
    match (t) {
        Tip(p) => { return p.n; },
        Nest(nst) => { return nst.n; }
    }
    return 0;
}
function scalar_match(n: i32): i32 {
    match (n) {
        1 => { return 1; },
        _ => { return 0; }
    }
}
`

const semsourcePrintDriver = `import "./semsource"; import "./ssa"; import "./ssaunits"; import "./typeinfo";
import "./parser"; import "./lexer"; import "./util";
function main(): i32 {
    var src: string = "";
    match (read_file(args()[1])) { Ok(text) => { src = text; }, Err(_) => { return 2; } }
    var mod = parser.parse_module(lexer.tokenize(src));
    for p in semsource.build_module(mod) {
        if (!p.ok) { print("refused " + p.why); continue; }
        var out: string = "";
        var i: i32 = 0;
        while (i < p.func.values.len()) {
            if (i > 0) { out = out + " "; }
            out = out + "v" + util.i32_to_string(i) + ":" + typeinfo.spelling(p.func.values[i]);
            i = i + 1;
        }
        var modes: string = "";
        for m in p.modes { modes = modes + " " + util.i32_to_string(m); }
        print("modes" + modes + " result " + typeinfo.spelling(p.func.result));
        print(out);
        var plan = ssaunits.plan(p.func, p.modes);
        if (!plan.ok) { print("plan " + plan.why); }
        print(ssa.print_func(p.func.graph));
    }
    return 0;
}
`

func TestSelfHostSemanticSourcePrint(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "semsource_print.fern"), []byte(semsourcePrintDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "fixture.fern")
	if err := os.WriteFile(fixture, []byte(semsourcePrintFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "semsource_print.fern", "semsource-print")
	got, err := runX86_64Bin(runner, driver, fixture).CombinedOutput()
	if err != nil {
		t.Fatalf("print driver: %v\n%s", err, got)
	}
	want, err := os.ReadFile("testdata/semsource_print.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("produced graphs differ from testdata/semsource_print.golden:\n%s", got)
	}
}

const semsourceRCProgram = `
@noinline function pick(k: i32): i32[] {
    var rows: i32[][] = [[1, 2], [3, 4], [5, 6]];
    var chosen: i32[] = rows[0];
    var i: i32 = 1;
    while (i <= k) {
        if (i == 2) { chosen = rows[i]; break; }
        chosen = rows[1];
        i = i + 1;
    }
    return chosen;
}
@noinline function pair(n: i32, flag: boolean): i32[] {
    var xs: i32[] = [n, 0];
    var count: i32 = 0;
    var t: (i32, i32[]) = (n, xs);
    if (flag && n > 2 || n == 0) {
        var n: i32 = -n;
        t = (n * 2, [n, n + 1]);
        count = t.1[0];
    } else {
        count = t.0 + 1;
    }
    var swapped: (i32, i32[]) = (count, t.1);
    return [swapped.0, swapped.1[1], swapped.1[0]];
}
@noinline function boxed(n: i32): (i32, i32[]) { return (n, [n, n + 1]); }
@noinline function boxed_local(n: i32): (i32, i32[]) {
    var xs: i32[] = [n];
    var t: (i32, i32[]) = (n, xs);
    var c: i32 = t.1[0];
    if (c < 0) { var e: i32[] = []; return (0, e); }
    return t;
}
@noinline function boxed_carry(n: i32): (i32[], boolean) {
    var xs: i32[] = [n, n * 2];
    return (xs, n > 0);
}
@noinline function carry(limit: i32): i32[] {
    var last: i32[] = [7];
    var i: i32 = 0;
    while (i < limit) {
        last = [i];
        if (i == 1) { break; }
        i = i + 1;
    }
    return last;
}
@noinline function count_even(limit: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    var odd: boolean = false;
    while (i < limit) {
        i = i + 1;
        odd = !odd;
        if (odd) { continue; }
        var j: i32 = 0;
        while (true) {
            if (j >= 2) { break; }
            total = total + i;
            j = j + 1;
        }
    }
    return total;
}
@noinline function fill(n: i32): i32[] {
    var out: i32[] = [n, n + 1, n + 2];
    return out;
}
@noinline function first_of(xs: i32[]): i32 { return xs[0]; }
@noinline function keep(own xs: i32[], k: i32): i32[] {
    if (k > 0) { return xs; }
    return [k];
}
@noinline function chain(n: i32): i32[] {
    var a: i32[] = fill(n);
    var b: i32[] = keep(a, n);
    var c: i32[] = keep(fill(n + 1), 0);
    return [first_of(b) + first_of(c), b[0]];
}
@noinline function twice(n: i32): i32 {
    var a: i32[] = fill(n);
    var x: i32 = first_of(a) + first_of(keep(a, 1)) + first_of(a);
    fill(x);
    return x;
}
@noinline function grow(limit: i32): i32[] {
    var cur: i32[] = [0];
    var i: i32 = 0;
    while (i < limit) {
        cur = keep(fill(i), i);
        i = i + 1;
    }
    return cur;
}
@noinline function count_down(n: i32): i32 {
    if (n <= 0) { return 0; }
    return 1 + count_down(n - 1);
}
struct P { n: i32, xs: i32[] }
struct Q { name: string, p: P }
@noinline function make(n: i32): P { return P { n: n, xs: [n, n + 1] }; }
@noinline function wrap(own p: P, tag: string): Q { return Q { name: tag + "!", p: p }; }
struct S2 { a: i32, b: i32 }
@noinline function mk_s2(n: i32): S2 { return S2 { a: n, b: n + 1 }; }
@noinline function unwrap(q: Q): i32 {
    var p: P = q.p;
    if (q.name == "x!") { return p.xs[0]; }
    return p.n + p.xs[1];
}
@noinline function tally(n: i32): i32 {
    var q: Q = wrap(make(n), "x");
    var r: Q = wrap(make(n + 1), "y");
    var keep: Q = q;
    if (n > 3) { keep = r; }
    return unwrap(q) + unwrap(keep) + unwrap(r);
}
enum Shape { Dot, Line(i32), Full(i32[]), Pair(i32, i32[]) }
struct Holder { s: Shape, n: i32 }
struct Leaf { n: i32 }
struct Twig { xs: i32[] }
type Node = Leaf | Twig;
@noinline function mk_node(n: i32): Node {
    if (n == 0) { return Leaf { n: 7 }; }
    return Twig { xs: [n, n + 1] };
}
@noinline function node_size(nd: Node): i32 {
    match (nd) {
        Leaf(l) => { return l.n; },
        Twig(t) => { return t.xs[1]; }
    }
    return 0 - 1;
}
struct Tip { v: i32 }
struct Fork { l: Tree, r: Tree }
type Tree = Tip | Fork;
enum Chain { End, Link(i32, Chain) }
@noinline function leaf(n: i32): Tree { return Tip { v: n }; }
@noinline function fork(a: Tree, b: Tree): Tree { return Fork { l: a, r: b }; }
@noinline function tree_sum(t: Tree): i32 {
    match (t) {
        Tip(x) => { return x.v; },
        Fork(y) => { return tree_sum(y.l) + tree_sum(y.r); }
    }
    return 0;
}
@noinline function build_sum(n: i32): i32 {
    var a: Tree = leaf(n);
    var b: Tree = leaf(n + 1);
    var t: Tree = fork(a, b);
    var c: Tree = leaf(n + 2);
    var u: Tree = fork(t, c);
    return tree_sum(u) + tree_sum(t);
}
@noinline function chain_len(c: Chain): i32 {
    match (c) { End => { return 0; }, Link(v, rest) => { return v + chain_len(rest); } }
    return 0;
}
@noinline function chain_build(n: i32): i32 {
    var c: Chain = End;
    var d: Chain = Link(n, c);
    var e: Chain = Link(n + 1, d);
    return chain_len(e) + chain_len(d);
}
@noinline function node_sum(limit: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    var kept: Node = Leaf { n: 0 };
    while (i < limit) {
        var nd: Node = mk_node(i);
        total = total + node_size(nd);
        if (i == 1) { kept = nd; }
        i = i + 1;
    }
    return total + node_size(kept);
}
@noinline function shape(n: i32): Shape {
    if (n == 0) { return Dot; }
    if (n == 1) { return Line(n); }
    if (n == 2) { return Full([n, n + 1]); }
    return Pair(n, [n]);
}
@noinline function measure(s: Shape): i32 {
    match (s) {
        Dot => { return 0; },
        Line(n) => { return n; },
        Full(xs) => { return xs[1]; },
        Pair(a, ys) => { return a + ys[0]; }
    }
    return 0 - 1;
}
@noinline function sum_shapes(limit: i32): i32 {
    var total: i32 = 0;
    var i: i32 = 0;
    var last: Shape = Dot;
    while (i < limit) {
        var s: Shape = shape(i);
        match (s) {
            Line(n) => { total = total + n * 10; },
            _ => { total = total + measure(s); }
        }
        if (i == 2) { last = s; }
        i = i + 1;
    }
    return total + measure(last);
}
@noinline function consume(own s: Shape): i32 {
    if let Full(xs) = s { return xs[0]; }
    return 7;
}
@noinline function boxed_shape(n: i32): i32 { return consume(shape(n)) + consume(Full([n])); }
@noinline function hold(n: i32): i32 {
    var h: Holder = Holder { s: shape(n), n: n };
    return measure(h.s) + h.n;
}
@noinline function greet(n: i32): i32 {
    var s: string = "ab";
    var i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    if (s == "abcc") { return 1; }
    if (s != "ab") { return 2; }
    return 0;
}
function main(): i32 {
    var a: i32[] = pick(0);
    var b: i32[] = pick(1);
    var c: i32[] = pick(5);
    print_int(a[0]); print(""); print_int(b[1]); print(""); print_int(c[0]); print("");
    var p: i32[] = pair(3, true);
    var q: i32[] = pair(1, false);
    var r: i32[] = pair(0, false);
    print_int(p[0]); print(""); print_int(p[1]); print(""); print_int(q[0]); print("");
    print_int(q[1]); print(""); print_int(r[1]); print("");
    var m: (i32, i32[]) = boxed(4);
    print_int(m.1[1]); print("");
    var ml: (i32, i32[]) = boxed_local(5);
    var mc: (i32[], boolean) = boxed_carry(6);
    print_int(ml.1[0]); print(""); print_int(mc.0[1]); print("");
    boxed_local(1);
    boxed_carry(2);
    print_int(carry(2)[0]); print("");
    var d: i32[] = carry(5);
    var e: i32[] = carry(0);
    print_int(d[0] + e[0]); print("");
    print_int(count_even(5)); print(""); print_int(count_even(0)); print("");
    var r: i32[] = chain(3);
    print_int(r[0]); print(""); print_int(r[1]); print("");
    print_int(twice(2)); print(""); print_int(count_down(4)); print("");
    var g: i32[] = grow(3);
    var h: i32[] = grow(0);
    print_int(g[0]); print(""); print_int(h[0]); print("");
    print_int(tally(1)); print(""); print_int(tally(5)); print("");
    print_int(greet(2)); print(""); print_int(greet(0)); print(""); print_int(greet(1)); print("");
    print_int(sum_shapes(4)); print(""); print_int(boxed_shape(3)); print(""); print_int(boxed_shape(0)); print("");
    print_int(hold(1)); print(""); print_int(hold(2)); print("");
    print_int(node_sum(3)); print(""); print_int(node_sum(1)); print("");
    print_int(build_sum(1)); print(""); print_int(build_sum(0)); print("");
    print_int(chain_build(2)); print(""); print_int(chain_build(0)); print("");
    var s2: S2 = mk_s2(4);
    print_int(s2.a); print("");
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`

// tally(1): unwrap(q) = xs[0] = 1; keep = q → 1; unwrap(r) = n + xs[1] = 2 + 3 = 5 → 7.
// tally(5): 5 + unwrap(r) (6 + 7 = 13) + 13 → 31.
// sum_shapes(4): Dot 0, Line(1) 10, Full([2, 3]) 3, Pair(3, [3]) 6, plus the kept Full: 22.
// boxed_shape(3): Pair takes the wildcard 7, Full([3]) 3: 10; boxed_shape(0): 7 + 0.
// node_sum(3): Leaf 7, Twig([1,2]) 2, Twig([2,3]) 3, plus the kept Twig 2 = 14.
// node_sum(1): the Leaf 7 only, plus the kept Leaf { n: 0 } = 7.
// build_sum(1): u is Fork(Fork(Tip 1, Tip 2), Tip 3) = 6, plus its shared
// subtree t = 3 → 9; build_sum(0): 3 + 1 = 4. A Fork field is typed by the
// union that owns Fork, so Tree reaches itself and only a helper can drop it.
// chain_build(2): Link(3, Link(2, End)) = 5, plus the shared tail d = 2 → 7;
// chain_build(0): 1 + 0 = 1. Chain is the recursive DECLARED enum, the layout
// the compiler's own sources never exercise.
const semsourceRCWant = "1\n4\n5\n-3\n-2\n2\n0\n1\n5\n5\n12\n1\n8\n12\n0\n3\n3\n6\n4\n2\n0\n7\n31\n1\n0\n2\n22\n10\n7\n2\n5\n14\n7\n9\n4\n7\n1\n"

const semsourceRCDriver = `import "./semsource"; import "./ssarc"; import "./ssaunits"; import "./ssa";
import "./parser"; import "./lexer"; import "./irlower"; import "./ir";
import "./ircore"; import "./checker"; import "./asmcore"; import "./asm_ir"; import "./asm_arm64_ir"; import "./wasm_ir";
function main(): i32 {
    var av = args();
    var src: string = "";
    match (read_file(av[2])) { Ok(text) => { src = text; }, Err(_) => { return 2; } }
    var mod = checker.annotate_module(parser.parse_module(lexer.tokenize(src)));
    var tab = irlower.struct_tab(mod.structs);
    var base = ircore.wp_fn_sigs(mod.funcs, tab);
    var produced = semsource.build_module(mod);
    var bodies: irlower.LowerResult[] = [];
    var helpers: irlower.LowerResult[] = [];
    var at: i32 = 0;
    for fd in mod.funcs {
        if (fd.name == "main") { bodies = bodies.append(irlower.LowerResult { ok: false, why: "", ops: [], n_locals: 0, n_params: 0, erased_wide: false, arr_slots: [], i64_slots: [], f64_slots: [], str_slots: [], alias_incs: [], name: "", result_kind: irlower.result_from_decl() }); at = at + 1; continue; }
        var p = produced[at];
        if (!p.ok) { eprint(fd.name + ": " + p.why); return 4; }
        var plan = ssaunits.plan(p.func, p.modes);
        if (!plan.ok) { eprint(fd.name + ": " + plan.why); return 5; }
        var lowered = ssarc.lower(p.func, p.modes, plan);
        if (!lowered.ok) { eprint(fd.name + ": " + lowered.why); return 6; }
        eprint("produced " + fd.name + "\n");
        base = ssarc.caller_sigs(base, fd.name, p.func);
        for h in ssarc.drop_helpers(p.func) { helpers = helpers.append(h); }
        bodies = bodies.append(lowered);
        at = at + 1;
    }
    var g = ircore.lower_gated(mod, tab, base, [], av[1] == "wasm32-wasi");
    if (!g.ok) { eprint("ast lowering failed"); return 3; }
    var cache: irlower.LowerResult[] = [];
    at = 0;
    for fd in mod.funcs {
        if (fd.name == "main") { cache = cache.append(g.cache[at]); } else { cache = cache.append(bodies[at]); }
        at = at + 1;
    }
    // The per-type drop helpers are bodies with no declaration, so they go on
    // the cache tail past mod.funcs, deduped by symbol.
    cache = ssarc.merge_helpers(cache, helpers);
    if (av[1] == "x86-64-linux") {
        print(asm_ir.emit_module_ir_unit_flat(mod, true, false, "", [], mod.funcs, tab, 0, 0 - 1, cache, base));
    } else if (av[1] == "arm64-linux") {
        strbuf_reset();
        var state = asmcore.new_state();
        state = asmcore.EmitState { ...state, struct_decls: tab, funcs: mod.funcs };
        state = asm_arm64_ir.emit_body(mod, state, false, cache, base);
        state = asm_arm64_ir.emit_ir_runtime(state, false);
        print(strbuf_take());
    } else { print(wasm_ir.emit_ir_module_mode(mod, cache, 0, base)); }
    return 0;
}
`

func TestSelfHostSemanticSourceRC(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "semsource_rc.fern"), []byte(semsourceRCDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(dir, "program.fern")
	if err := os.WriteFile(program, []byte(semsourceRCProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "semsource_rc.fern", "semsource-rc")
	for _, target := range []string{"arm64-linux", "x86-64-linux", "x86-64-sanitize", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			emitTarget, mode := target, "FERN_LEAKCHECK=1"
			if target == "x86-64-sanitize" {
				emitTarget, mode = "x86-64-linux", "FERN_SANITIZE=1"
			}
			cmd := runX86_64Bin(runner, driver, emitTarget, program)
			cmd.Env = append(os.Environ(), mode)
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("semantic lowering: %v\n%s", err, diagnostics.String())
			}
			for _, name := range []string{"pick", "pair", "boxed", "carry", "count_even", "fill", "first_of", "keep", "chain", "twice", "count_down", "grow", "make", "wrap", "unwrap", "tally", "greet", "boxed_local", "boxed_carry", "shape", "measure", "sum_shapes", "consume", "boxed_shape", "hold", "mk_node", "node_size", "node_sum", "leaf", "fork", "tree_sum", "build_sum", "chain_len", "chain_build", "mk_s2"} {
				if !strings.Contains(diagnostics.String(), "produced "+name+"\n") {
					t.Fatalf("%s was not produced:\n%s", name, diagnostics.String())
				}
			}
			run := physicalRCRun(t, gcc, runner, dir, "semsource", target, output)
			got, err := run.CombinedOutput()
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					t.Fatalf("program exit %d:\n%s", exit.ExitCode(), got)
				}
				t.Fatalf("program: %v\n%s", err, got)
			}
			if !strings.HasPrefix(string(got), semsourceRCWant) {
				t.Fatalf("program output:\n%s", got)
			}
			if target != "wasm32-wasi" {
				var allocs, frees, live int64
				summary := leakSummaryLine(string(got))
				if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
					t.Fatal(err)
				}
				if allocs == 0 || allocs != frees || live != 0 {
					t.Fatalf("unbalanced: %s", summary)
				}
			}
		})
	}
}
