package ir

import "testing"

// A parameter the body never mentions cannot be retained by any means, so
// the three counted-retain summaries must credit it.
//
// They required at least one occurrence, which read the vacuous case as
// "unknown" and left the caller's conservative taint in place. On the
// native single-word string ABI that taint is a deliberate never-reclaim
// — computeFreeEligible taints a string ident passed to a user function
// unless paramCountedRetain clears it, "a leak at worst, never a
// use-after-free" — so an unused parameter leaked its argument
// unconditionally. #7798: three lines, one allocation, zero frees on
// x86-64 while arm64 was clean.
//
// Zero occurrences is the STRONGEST form of the property being asked
// about, not the absence of evidence for it.
func TestUnusedParameterIsCountedRetain(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		fn   string
		want []bool
	}{
		{
			name: "unused string parameter",
			src:  `function ignore(s: string): i32 { return 7; }`,
			fn:   "ignore",
			want: []bool{true},
		},
		{
			name: "both string parameters unused",
			src:  `function ignore2(s: string, t: string): i32 { return 7; }`,
			fn:   "ignore2",
			want: []bool{true, true},
		},
		{
			// The mixed case: only the unused one is credited by
			// vacuity, and the read one by the pure-read rule — so
			// this passes for two different reasons and would still
			// catch a change that credited everything.
			name: "one read, one unused",
			src:  `function half(a: string, b: string): i32 { return a.len(); }`,
			fn:   "half",
			want: []bool{true, true},
		},
		{
			name: "unused array parameter",
			src:  `function ignoreArr(a: i32[]): i32 { return 7; }`,
			fn:   "ignoreArr",
			want: []bool{true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := paramCountedFor(t, tc.src, tc.fn)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d flags, want %d", len(got), len(tc.want))
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("parameter %d: got counted=%v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// The mirror: a parameter that IS retained uncounted must stay
// uncredited, so the caller keeps the taint that stops it freeing a
// value the callee kept a reference to. This is the direction whose
// failure mode is a use-after-free rather than a leak.
func TestUncountedRetentionStaysUncredited(t *testing.T) {
	// `keep` stores its parameter into a global-ish container it
	// returns, by way of an array the caller receives — an
	// uncounted retention the intraprocedural escape analysis
	// cannot see through.
	src := `function keep(s: string): string[] { var xs: string[] = []; return xs.append(s); }`
	got := paramCountedFor(t, src, "keep")
	if len(got) != 1 {
		t.Fatalf("got %d flags, want 1", len(got))
	}
	if got[0] {
		t.Error("a parameter stored into a returned container must not be credited")
	}
}
