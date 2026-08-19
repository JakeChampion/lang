package constfold

import "testing"

// The substituter's STATEMENT switch had the same hole its expression switch
// was already patched for (#5477): a const referenced from a form it did not
// visit survived as a bare Ident and reached the checker as "E001: undefined
// identifier" for a const plainly in scope.
//
// Each case here fails on the un-patched walk and is a shape real code writes:
// found while porting native's assert elision to the self-host, where
// `parser.ORIGIN_ASSERT` inside a match arm would not resolve (#7157).
func TestSubstituteReachesEveryStatementForm(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			// The statement match — MatchExpr was handled, this was not.
			name: "match-arm-body",
			body: "match (n) { 0 => { return LIMIT; }, _ => { return 0; } }\n    return 0;",
		},
		{
			name: "match-scrutinee",
			body: "match (LIMIT) { 0 => { return 1; }, _ => { return LIMIT; } }\n    return 0;",
		},
		{
			name: "match-guard",
			body: "match (n) { x when x > LIMIT => { return LIMIT; }, _ => { return 0; } }\n    return 0;",
		},
		{
			name: "match-literal-arm",
			body: "match (n) { LIMIT => { return LIMIT; }, _ => { return 0; } }\n    return 0;",
		},
		{
			name: "defer-action",
			body: "defer { var d: i32 = LIMIT; }\n    return LIMIT;",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "const LIMIT: i32 = 7;\n" +
				"function main(): i32 {\n    var n: i32 = 0;\n    " + tc.body + "\n}\n"
			prog := fold(t, src)
			if n := countIdents(prog, "LIMIT"); n != 0 {
				t.Errorf("%d unsubstituted `LIMIT` reference(s) left; the const survives to the checker as an undefined identifier", n)
			}
		})
	}
}
