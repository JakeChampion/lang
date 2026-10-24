package checker

import (
	"strings"
	"testing"
)

func TestDeferTryOpRefused(t *testing.T) {
	const prelude = `function g(v: i32): Option[i32] {
	if (v < 100) { return Some(v + 1); }
	return None;
}
`
	cases := []struct {
		name  string
		body  string
		want  string
		clean bool
	}{
		{"defer", `function f(): Option[i32] {
	let n: i32 = 0;
	defer n = g(n)?;
	return Some(n);
}`, "`?` is not allowed inside a `defer` action", false},
		{"errdefer", `function f(): Option[i32] {
	let n: i32 = 0;
	errdefer n = g(n)?;
	return Some(n);
}`, "`?` is not allowed inside an `errdefer` action", false},
		{"try_in_lambda_literal_in_defer_is_not_this_rule", `function f(out: Cell[i32]): i32 {
	defer out.set((x: i32) => g(x)?);
	return 0;
}`, "", false},
		{"try_in_lambda_literal_in_defer_is_accepted", `function run(h: (i32) => Option[i32]): void { let r: Option[i32] = h(1); }
function f(): i32 {
	defer run((x: i32) => { let y: i32 = g(x)?; return Some(y + 1); });
	return 0;
}`, "", true},
		{"defer_inside_a_lambda_body", `function f(out: Cell[i32]): i32 {
	let h: (i32) => i32 = (x: i32) => {
		let n: i32 = x;
		defer n = g(n)?;
		return n;
	};
	out.set(h(1));
	return 0;
}`, "`?` is not allowed inside a `defer` action", false},
		{"try_outside_the_defer", `function f(out: Cell[i32]): Option[i32] {
	let n: i32 = g(0)?;
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

func TestDeferTryOpIsNotAnInferredReturn(t *testing.T) {
	got := checkSrc(t, `function g(v: i32): Option[i32] {
	if (v < 100) { return Some(v + 1); }
	return None;
}
function main(): i32 {
	let h: (i32) => i32 = (x: i32) => { let n: i32 = x; defer n = g(n)?; return n; };
	return h(1);
}`)
	if !strings.Contains(got, "is not allowed inside a `defer` action") {
		t.Errorf("want E079, got:\n%s", got)
	}
	if strings.Contains(got, "mismatch") {
		t.Errorf("the refused `?` was inferred as a return:\n%s", got)
	}
}
