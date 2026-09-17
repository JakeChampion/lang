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
		want string // "" = must be accepted
	}{
		{"defer", `function f(): Option[i32] {
	var n: i32 = 0;
	defer n = g(n)?;
	return Some(n);
}`, "`?` is not allowed inside a `defer` action"},
		{"errdefer", `function f(): Option[i32] {
	var n: i32 = 0;
	errdefer n = g(n)?;
	return Some(n);
}`, "`?` is not allowed inside an `errdefer` action"},
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
		// It is not a clean accept. A `?` in any lambda draws E042 today, the
		// checker reading the ENCLOSING function's return type rather than the
		// lambda's (#9515), and the lambda in argument position draws E038 with
		// it. The assertion is that neither brings E079 — which is what the
		// pruning decides, and all that is observable until #9515 is fixed.
		//
		// Native only: the self-host reports nothing at all for this program
		// (#9518), so the codes differential cannot carry it yet.
		{"try_in_lambda_literal_in_defer_is_not_this_rule", `function f(out: Cell[i32]): i32 {
	defer out.set((x: i32) => g(x)?);
	return 0;
}`, ""},
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
}`, "`?` is not allowed inside a `defer` action"},
		// The operator is unaffected everywhere else in a function that also
		// holds a defer — the rule is about the action, not the function.
		{"try_outside_the_defer", `function f(out: Cell[i32]): Option[i32] {
	var n: i32 = g(0)?;
	defer out.set(n);
	return Some(n);
}`, ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := checkSrc(t, prelude+c.body)
			const rule = "is not allowed inside"
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
