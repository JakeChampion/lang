package checker

import (
	"strings"
	"testing"
)

// E079 refuses `?` inside a `defer` / `errdefer` action (#9470).
//
// The shape is circular by construction: `?` leaves by the failure edge, and
// that edge replays the function's deferred actions — one of which carries
// this `?`, whose failure edge replays them again. Both compilers recursed
// until they died on it, native with a Go stack overflow and the self-host
// with a SIGSEGV, while the checker accepted the program.
//
// Refused rather than given a meaning, because `?` propagates a failure to the
// caller and a deferred action has no caller to propagate to. The interpreter
// reached the shape first and silently discarded the failure, which is the
// behaviour this rule exists to stop being the answer.
func TestDeferTryOpRefused(t *testing.T) {
	const prelude = `function g(v: i32): Option[i32] {
	if (v < 100) { return Some(v + 1); }
	return None;
}
`
	cases := []struct {
		name string
		body string
		// want is a substring of the diagnostic's MESSAGE. checkSrc renders
		// `type error at L:C: <msg>` with no code, so the E079 code itself is
		// pinned by the conformance case diag_e079, which goes through the CLI.
		want string // "" = the defer-`?` rule must not fire
		// clean additionally requires no diagnostic at all.
		clean bool
	}{
		{"defer", `function f(): Option[i32] {
	var n: i32 = 0;
	defer n = g(n)?;
	return Some(n);
}`, "`?` is not allowed inside a `defer` action", false},
		{"errdefer", `function f(): Option[i32] {
	var n: i32 = 0;
	errdefer n = g(n)?;
	return Some(n);
}`, "`?` is not allowed inside an `errdefer` action", false},
		// A `?` in a lambda inside the action leaves the LAMBDA on its own
		// exits, not the function whose defer replays the action, so the walk
		// must not descend into it.
		//
		// The lambda is a LITERAL in the action. A lambda bound to a local and
		// merely called from the action does not exercise this: the action's
		// subtree is then just a call of that name, the walk never meets a
		// lambda, and the case passes with the pruning removed. Verified in
		// both directions — with `firstTryOp`'s lambda arm deleted this program
		// gains E079 and the bound-local spelling does not change at all.
		//
		// This spelling is not a clean accept for an unrelated reason: the
		// lambda reaches `out.set`, which takes an i32 (E038). The assertion is
		// only that the pruning keeps E079 away.
		//
		// Native only: the self-host reports nothing at all for this program
		// (#9518), so the codes differential cannot carry it yet.
		{"try_in_lambda_literal_in_defer_is_not_this_rule", `function f(out: Cell[i32]): i32 {
	defer out.set((x: i32) => g(x)?);
	return 0;
}`, "", false},
		// The positive half: a `?` in a lambda literal in the action, passed
		// where an Option-returning function is wanted, is accepted outright.
		// The lambda's own return type is what `?` propagates to (#9515).
		{"try_in_lambda_literal_in_defer_is_accepted", `function run(h: (i32) => Option[i32]): void { var r: Option[i32] = h(1); }
function f(): i32 {
	defer run((x: i32) => { var y: i32 = g(x)?; return Some(y + 1); });
	return 0;
}`, "", true},
		// A `defer` nested INSIDE a lambda body is the same circular shape one
		// level down — it registers on the lambda's own exits, and the `?` in
		// its action propagates out of the lambda, which is what replays it.
		// The self-host mirror missed this: its walk entered no expression, so
		// the lambda body stayed out of reach.
		{"defer_inside_a_lambda_body", `function f(out: Cell[i32]): i32 {
	var h: (i32) => i32 = (x: i32) => {
		var n: i32 = x;
		defer n = g(n)?;
		return n;
	};
	out.set(h(1));
	return 0;
}`, "`?` is not allowed inside a `defer` action", false},
		// The operator is unaffected everywhere else in a function that also
		// holds a defer — the rule is about the action, not the function.
		{"try_outside_the_defer", `function f(out: Cell[i32]): Option[i32] {
	var n: i32 = g(0)?;
	defer out.set(n);
	return Some(n);
}`, "", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := checkSrc(t, prelude+c.body)
			const rule = "is not allowed inside"
			if c.clean && got != "" {
				t.Errorf("want no diagnostic, got:\n%s", got)
			}
			if c.want == "" {
				if strings.Contains(got, rule) {
					t.Errorf("accepted shape drew the defer-`?` rule:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("want a diagnostic containing %q, got:\n%s", c.want, got)
			}
		})
	}
}

// A `?` refused by E079 has no failure edge, so it is not one of an
// unannotated lambda's inferred returns. Counting it as one (#9515) typed the
// lambda from an Option return nothing can reach, and the binding then drew a
// return-type mismatch beside E079 where the self-host reports E042.
func TestDeferTryOpIsNotAnInferredReturn(t *testing.T) {
	got := checkSrc(t, `function g(v: i32): Option[i32] {
	if (v < 100) { return Some(v + 1); }
	return None;
}
function main(): i32 {
	var h: (i32) => i32 = (x: i32) => { var n: i32 = x; defer n = g(n)?; return n; };
	return h(1);
}`)
	if !strings.Contains(got, "is not allowed inside a `defer` action") {
		t.Errorf("want E079, got:\n%s", got)
	}
	if strings.Contains(got, "mismatch") {
		t.Errorf("the refused `?` was inferred as a return:\n%s", got)
	}
}
