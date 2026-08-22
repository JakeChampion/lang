package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostCheckerCodeSequenceX86_64 pins the ORDER of the self-host
// checker's diagnostics and HOW MANY TIMES it reports each one.
//
// Nothing else does. Every other gate over checker output reduces to a set
// before comparing: the codes differential takes "the sorted, de-duplicated set
// of diagnostic codes", the hint-text differential groups into "code -> sorted
// unique messages", and the driver test asserts only that a wantDiag substring
// APPEARS on stderr. A change that reorders diagnostics, or reports one twice,
// is green in all three and plainly visible to a user.
//
// The gap is load-bearing for the SH-022 walker migration (docs/SELF-HOST-AUDIT.md).
// Folding a diagnostic collector onto astwalk swaps its traversal for the shared
// one, and the two need not visit siblings in the same order. Worse, getting the
// DESCENT wrong duplicates a report rather than dropping one:
// e049_expr_lambdas hands each lambda body to e049_check_assigns and must
// therefore PRUNE at ExprLambda, because astwalk descends into lambda bodies by
// default — with the plain fold it would report every nested lambda once per
// enclosing one. The nested-lambda rows below are that case.
//
// What this pins is CURRENT BEHAVIOUR, not a claim that the order is the right
// one. A row that moves means a change altered emission order or multiplicity:
// decide whether the new sequence is correct, then move the row. It is a
// regression detector, not an oracle — the codes differential remains the gate
// for WHICH codes are right.
//
// It earned its place on the first run: the nested-lambda rows exposed a real
// gap. e049_check_assigns descends into if/while/for/match/defer bodies but a
// nested lambda arrives as a StmtVar INITIALISER and hit its `_ => {}` arm,
// while e049_expr_lambdas prunes at the enclosing lambda — so `s = "y"` inside
// a lambda inside a lambda was accepted outright. The self-host printed nothing
// where the Go checker reports E049, and for the both-assign shape printed ONE
// where the Go checker reports two. The second of those is invisible to the
// codes differential by construction: same set, different count.
func TestSelfHostCheckerCodeSequenceX86_64(t *testing.T) {
	checkerBin, runner, _ := buildCheckerCodesBin(t)

	cases := []struct {
		name string
		src  string
		// want is the codes in emission order, duplicates kept, comma
		// separated. "" means the program is clean.
		want string
	}{
		// Two independent leaks in one function: multiplicity, no ordering
		// question. A collector that starts reporting a leak twice fails here
		// while every set-based gate stays green.
		{"two-leaks", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(): void { var a: Ticket = Ticket { id: 1 }; var b: Ticket = Ticket { id: 2 }; }\nfunction main(): i32 { return 0; }\n", "E067,E067"},
		// Two leaks in two different functions: pins the order the checker
		// walks top-level declarations in.
		{"leak-in-each-of-two-fns", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(): void { var a: Ticket = Ticket { id: 1 }; }\nfunction g(): void { var b: Ticket = Ticket { id: 2 }; }\nfunction main(): i32 { return 0; }\n", "E067,E067"},
		// A lambda nested in a lambda, the INNER one assigning a captured
		// string. This is the row that catches a lost prune in
		// e049_expr_lambdas: descending into the lambda body as well as
		// handing it to e049_check_assigns reports the inner E049 twice.
		{"nested-lambda-capture-assign", "function main(): i32 { var s: string = \"x\"; var f = function(): i32 { var g = function(): i32 { s = \"y\"; return 0; }; return g(); }; return f(); }\n", "E049"},
		// Both lambdas assigning: two E049s, and the count is what a lost
		// prune inflates.
		{"nested-lambda-both-assign", "function main(): i32 { var s: string = \"x\"; var f = function(): i32 { s = \"a\"; var g = function(): i32 { s = \"y\"; return 0; }; return g(); }; return f(); }\n", "E049,E049"},
		// Two sibling lambdas, each assigning a captured string: two E049s
		// with no nesting, so a prune change moves the nested rows above but
		// not this one — which is what separates "order changed" from
		// "descent changed".
		{"sibling-lambdas-capture-assign", "function main(): i32 { var s: string = \"x\"; var f = function(): i32 { s = \"a\"; return 0; }; var g = function(): i32 { s = \"b\"; return 0; }; return f() + g(); }\n", "E049,E049"},
		// E036 (a variant declared in two enums, referenced unqualified). These
		// three guard vref_expr, whose ExprLambda arm calls the SCOPE-THREADED
		// vref_stmts rather than itself — precisely so a variant name shadowed
		// by a local `var` is not flagged. Converting it to a plain fold_expr
		// would walk the lambda body with the OUTER scope and report the
		// shadowed case: the middle row goes from clean to E036, and nothing
		// else here would notice.
		{"two-ambiguous-variant-refs", "enum A { Red, Blue }\nenum B { Red, Green }\nfunction main(): i32 { var x = Red; var y = Red; return 0; }\n", "E036,E036"},
		{"lambda-shadows-variant-name", "enum A { Red, Blue }\nenum B { Red, Green }\nfunction main(): i32 { var f = function(): i32 { var Red: i32 = 1; return Red; }; return 0; }\n", ""},
		{"lambda-does-not-shadow", "enum A { Red, Blue }\nenum B { Red, Green }\nfunction main(): i32 { var f = function(): i32 { var z = Red; return 0; }; return 0; }\n", "E036"},
		// E043 / E005 from slit_diags, which is POST-order: it recurses into a
		// literal's field_values BEFORE emitting its own diagnostics, where
		// astwalk's fold is pre-order. These rows pin the relative order, which
		// a straight fold conversion would reverse.
		//
		// The nested row also records a live DIVERGENCE that no other gate can
		// see. For `Out { bad2: In { bad1: 1 } }` the self-host reports all four
		// diagnostics — the inner literal's unknown and missing fields as well
		// as the outer's — while the Go checker reports only the outer two,
		// having stopped descending once the outer field name was unknown. Both
		// sides yield the code SET {E043, E005}, so the codes differential and
		// the hint-text differential are blind to it by construction.
		//
		// Which side is right is genuinely unclear and is NOT settled here: the
		// inner literal does have an unknown field and does miss one, and
		// slit_diags reaches both from the literal's own type_name without
		// needing an expected type, so the extra reports may be the better
		// answer. This pins today's behaviour so the difference is visible and
		// cannot drift further while someone decides.
		{"struct-lit-unknown-and-missing", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p: P = P { x: 1, zz: 2 }; return 0; }\n", "E043,E005"},
		{"struct-lit-nested-bad-fields", "struct In { a: i32 }\nstruct Out { x: In }\nfunction main(): i32 { var o: Out = Out { bad2: In { bad1: 1 } }; return 0; }\n", "E043,E005,E043,E005"},
		{"struct-lit-two-siblings-missing", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var a: P = P { x: 1 }; var b: P = P { x: 2 }; return 0; }\n", "E005,E005"},
		// E044 (a lambda capturing a variable of unsupported type). e044_expr
		// prunes at ExprLambda for the same reason e049_expr_lambdas does — its
		// own comment says "stmts_mention already recursed into any nested
		// lambda, so descending again would double-report". The nested row is
		// that guard: the outer lambda captures x transitively through the
		// inner one, so BOTH are reported and the count is exactly two. Lose
		// the prune and the inner one is counted again.
		{"two-sibling-lambdas-bad-capture", "function f[T](x: T): i32 {\n  var g = () => x;\n  var h = () => x;\n  return 0;\n}\nfunction main(): i32 { return f(1); }\n", "E044,E044"},
		// UNDER-REPORT, pinned so it cannot drift: the Go checker reports E044
		// twice here — the inner lambda captures x directly, the outer captures
		// it transitively — and the self-host reports once. Same code SET
		// {E044} either way, so the codes and hint-text differentials cannot
		// see it, and nothing currently fails.
		//
		// Root cause is the same family as the nested-lambda E049 gap (#7363):
		// e044_stmts walks only the enclosing function's statements, and
		// e044_expr prunes at the outer lambda, so the inner one is never
		// reached. The outer IS reported, because e044_lambda_check asks
		// stmts_mention whether the body mentions the suspect and a nested
		// lambda's mention counts. The prune's own comment worries about
		// double-reporting, but that is about the SAME lambda twice; reaching
		// the inner one yields two DIFFERENT lambdas, which is what Go does.
		//
		// Not fixed here because the fix is not the mechanical sweep E049's
		// was: e044_lambda_check reports at the STATEMENT's position against a
		// suspects/labels list e044_stmts computed for the enclosing scope, so
		// reaching a nested lambda needs a decision about whether it reuses
		// those suspects minus accumulated param shadowing, or recomputes them
		// for the inner scope. That choice changes which diagnostics appear.
		{"nested-lambda-bad-capture", "function f[T](x: T): i32 {\n  var g = function(): i32 { var h = () => x; return 0; };\n  return 0;\n}\nfunction main(): i32 { return f(1); }\n", "E044"},
		// E032 from e032_expr, which prunes at ExprLambda and hands the body to
		// e032_stmts — the same prune-and-delegate shape as vref_expr. The
		// lambda row exercises that path. These also pin how two SEPARATE
		// passes interleave: the Go checker emits both E032s before either
		// E038, so the sequence records pass grouping, not just per-pass order.
		// PASS-ORDER divergence, pinned: the self-host runs the E038 pass before
		// the E032 pass and the Go checker runs them the other way round. Same
		// multiset both ways, so every set-based gate is blind to it, and it is
		// user-visible — it decides which error you are shown first.
		//
		// This is NOT an SH-022 concern: it is the order check_module sequences
		// its passes in, not the order any one walk visits nodes, so folding a
		// collector cannot change it. Recorded here because this gate is where
		// it became visible, and pinned so it cannot drift while someone
		// decides whether the self-host should match the oracle's order.
		{"two-bad-use-bindings", "function add(x: i32, y: i32): i32 { return x + y; }\nfunction main(): i32 {\n    use n <- add(1);\n    use m <- add(2);\n    return n + m;\n}\n", "E038,E038,E032,E032"},
		{"use-inside-lambda", "function add(x: i32, y: i32): i32 { return x + y; }\nfunction main(): i32 {\n    var f = function(): i32 { use n <- add(1); return n; };\n    return f();\n}\n", "E038,E032"},
		// E060 / E062 from e060_e062_stmts. Captured against the UNCONVERTED hand
		// walk first, which is how the three divergences below were found rather
		// than inferred: it listed nine expression kinds and dropped the rest on a
		// `_ => {}` arm, so a downcast it never reached was simply accepted.
		//
		// Folding it onto astwalk fixed all three. The first two rows already
		// matched the Go checker and still do — they are the control that separates
		// "coverage widened" from "behaviour changed".
		{"two-bad-downcasts", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nstruct Square { s: i32 }\nstruct Tri { t: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    var d: dyn Shape = Circle { r: 3 };\n    match (d as? Square) { Some(sq) => { return sq.s; }, None => {} }\n    match (d as? Tri) { Some(tr) => { return tr.t; }, None => {} }\n    return 0;\n}\n", "E060,E060"},
		// Was "" — the walk stopped at ExprLambda, so a bad downcast inside a
		// lambda body was accepted outright. Go reports E060 here.
		{"downcast-inside-lambda", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nstruct Square { s: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    var d: dyn Shape = Circle { r: 3 };\n    var f = function(): i32 {\n        match (d as? Square) { Some(sq) => { return sq.s; }, None => { return 0; } }\n    };\n    return f();\n}\n", "E060"},
		// Was "" for the other half of the same gap: e060_collect_dyn_locals never
		// entered a lambda either, so a `dyn` local DECLARED inside one was not in
		// the name set and nothing downstream could flag its downcast.
		{"dyn-local-inside-lambda", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nstruct Square { s: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    var f = function(): i32 {\n        var d: dyn Shape = Circle { r: 3 };\n        match (d as? Square) { Some(sq) => { return sq.s; }, None => { return 0; } }\n    };\n    return f();\n}\n", "E060"},
		// Was "E060,E062" — the hand walk visited a struct literal's field_values
		// before its `...base`, where source order and astwalk both put the base
		// first. Two DIFFERENT codes, one in each slot, is what makes the order
		// visible to this gate at all: with the same code in both, the sequence is
		// unchanged and only the messages move, which every gate here is blind to.
		{"structlit-base-before-fields", "trait Shape { function area(self: Self): i32; }\ntrait A { function m(self: Self): i32; }\ntrait B { function m(self: Self): i32; }\nstruct Circle { r: i32 }\nstruct Square { s: i32 }\nstruct S { v: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nimpl A for S { function m(self: Self): i32 { return self.v; } }\nimpl B for S { function m(self: Self): i32 { return self.v; } }\nstruct Box { a: i32, b: i32 }\nfunction cs(o: Option[Square]): i32 { return 0; }\nfunction boxify(n: i32): Box { return Box { a: n, b: n }; }\nfunction main(): i32 {\n    var d: dyn Shape = Circle { r: 3 };\n    var e: dyn A + B = S { v: 1 };\n    var q: Box = Box { ...boxify(e.m()), a: cs(d as? Square) };\n    return q.a;\n}\n", "E062,E060"},
		{"ambiguous-dyn-method", "trait A { function m(self: Self): i32; }\ntrait B { function m(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function m(self: Self): i32 { return self.v; } }\nimpl B for S { function m(self: Self): i32 { return self.v; } }\nfunction main(): i32 {\n    var d: dyn A + B = S { v: 1 };\n    return d.m();\n}\n", "E062"},
		// Mixed codes in one program: pins the relative order of two DIFFERENT
		// diagnostics, which is what a reordered traversal disturbs.
		{"mixed-capture-and-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction main(): i32 { var s: string = \"x\"; var f = function(): i32 { s = \"y\"; return 0; }; var tk: Ticket = Ticket { id: 1 }; return f(); }\n", "E067,E049"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			out := runCheckerDriver(t, cmd, tc.name)

			var codes []string
			for _, d := range driverDiags(out) {
				codes = append(codes, d.code)
			}
			got := strings.Join(codes, ",")
			if got != tc.want {
				t.Errorf("%s: diagnostic sequence = %q, want %q\n"+
					"    A change here means emission ORDER or MULTIPLICITY moved, which no\n"+
					"    other checker gate can see. Decide whether the new sequence is right,\n"+
					"    then move this row.", tc.name, got, tc.want)
			}
		})
	}
}
