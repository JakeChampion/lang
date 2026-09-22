package e2e

import (
	"strings"
	"testing"
)

// The failure paths of the corpus coverage gate in fixture_selfhost_test.go.
//
// That gate reads a compile's stderr and passes when every module reports
// `0 declined`. Its whole value is that it FAILS when the register path's
// coverage narrows, and a corpus where nothing declines exercises only the
// passing path — so the interesting half would never run. Worse, the obvious
// shapes of this check are quietly vacuous: an empty report satisfies "no
// tally says non-zero", and the per-function decline line (`FERN_SSA: <fn>:
// <why>`) contains no such word as "declined", so a check grepping for one
// matches nothing however badly coverage regresses.
//
// These pin both answers against synthetic reports, which is the only way to
// know the gate can speak.
func TestSSACoverageProblems(t *testing.T) {
	const tallyClean = "FERN_SSA: module: 812 of 812 functions through the SSA backend, 0 declined"
	const tallyDirty = "FERN_SSA: module: 810 of 812 functions through the SSA backend, 2 declined"

	for _, tc := range []struct {
		name   string
		report string
		// want is a substring the single expected problem must contain; empty
		// means the report must produce no problem at all.
		want string
	}{
		{"a clean tally passes", tallyClean, ""},
		{
			"a declining tally fails and quotes the tally",
			tallyDirty + "\nFERN_SSA: checker__widen_ty: dyn_dispatch\n",
			"2 declined",
		},
		{
			// The report names what declined; the message has to carry it or
			// the failure says a function regressed without saying which.
			"a declining tally names the declined function",
			tallyDirty + "\nFERN_SSA: checker__widen_ty: dyn_dispatch\n",
			"checker__widen_ty",
		},
		{
			// The vacuity guard. An empty report is what a missing env or a
			// changed default backend looks like, and it must not read as
			// success.
			"an empty report fails rather than passing vacuously",
			"",
			"checked nothing",
		},
		{
			"stderr with no FERN_SSA line at all fails the same way",
			"self-host: some unrelated warning\n",
			"checked nothing",
		},
		{
			// A slow function is reported under the same prefix. Counting it
			// as a decline would fail every large program.
			"a timing line is not a decline",
			tallyClean + "\nFERN_SSA: time irlower__lower_expr: lift 240 ms, emit 90 ms, 12 ops, 4 slots\n",
			"",
		},
		{
			// One module per unit on the per-module path: a clean unit must not
			// excuse a declining sibling.
			"a clean module does not excuse a declining one",
			tallyClean + "\n" + tallyDirty + "\n",
			"2 declined",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ssaCoverageProblems("x86-64-linux", "fixture", tc.report)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("want no problem, got %d:\n%s", len(got), strings.Join(got, "\n"))
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want exactly 1 problem, got %d:\n%s", len(got), strings.Join(got, "\n"))
			}
			if !strings.Contains(got[0], tc.want) {
				t.Errorf("problem = %q, want it to contain %q", got[0], tc.want)
			}
		})
	}
}
