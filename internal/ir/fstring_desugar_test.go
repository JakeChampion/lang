package ir

import "testing"

// An f-string IS its desugaring. The checker builds the equivalent
// `+`-chain onto `FString.Desugared` and the lowering reads that node
// alone — `Parts` survives only for the formatter — so the two spellings
// must reach the backend as the same ops.
//
// They did not. Every ownership classifier in this package switches on the
// raw expression, and none had an `*ast.FString` arm, so an f-string RHS
// hit each one's conservative default: `rhsTainted` said tainted, and the
// destination local lost `freeEligible`, so the store's release of the
// superseded string was suppressed. `s = f"{x}-y"` in a loop leaked one
// buffer per iteration on x86-64 and wasm while the hand-written concat
// beside it was clean (#8697).
//
// Comparing whole op streams rather than probing for the one missing
// `__fern_str_dec` is deliberate: the gap was four classifiers wide, and
// any future one that forgets f-strings shows up here as a divergence
// without anyone having to predict which op it costs.
//
// `k` is read after the loop on purpose. Left dead there, the two
// spellings differ legitimately — the f-string form gets an early
// post-loop drop of the receiver that the concat form does not — and that
// placement difference is not what these cases are about.
func TestFStringLowersLikeItsDesugaring(t *testing.T) {
	// `to_string` on a local struct keeps the fixtures free of a stdlib
	// import: f-string interpolation dispatches to it by name.
	const preamble = `struct K { n: i32 }
impl K { function to_string(self: Self): string { return "k"; } }
function sink(s: string): i32 { return s.len(); }
`
	cases := []struct {
		name      string
		fstring   string
		desugared string
	}{
		{
			// The #8697 shape: an outer local reassigned in a loop.
			name:      "reassignment in a loop",
			fstring:   `s = f"{k}-iteration";`,
			desugared: `s = k.to_string() + "-iteration";`,
		},
		{
			name:      "literal on both sides of the interpolant",
			fstring:   `s = f"a{k}b";`,
			desugared: `s = "a" + k.to_string() + "b";`,
		},
		{
			name:      "argument position",
			fstring:   `n = n + sink(f"{k}!");`,
			desugared: `n = n + sink(k.to_string() + "!");`,
		},
		{
			name:      "fresh binding inside the loop",
			fstring:   `var t: string = f"{k}-iteration"; n = n + t.len();`,
			desugared: `var t: string = k.to_string() + "-iteration"; n = n + t.len();`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := func(stmt string) string {
				return preamble + `function main(): i32 {
  var s: string = "";
  var k: K = K { n: 1 };
  var i: i32 = 0;
  var n: i32 = 0;
  while (i < 3) { ` + stmt + ` i = i + 1; }
  return s.len() + n + k.n;
}`
			}
			got := lowerSource(t, body(tc.fstring)).String()
			want := lowerSource(t, body(tc.desugared)).String()
			if got != want {
				t.Errorf("f-string and its desugaring lower differently\n--- f-string ---\n%s\n--- desugared ---\n%s", got, want)
			}
		})
	}
}
