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
