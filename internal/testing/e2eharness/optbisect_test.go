package e2eharness

import "testing"

func TestParseOptTrace(t *testing.T) {
	out := "opt-decision 30 inline f\nnoise\nopt-decision 10 reuse g\nopt-decision 30 inline f\nopt-decision x inline h\n"
	got := ParseOptTrace(out)
	want := []OptDecision{{10, "reuse", "g"}, {30, "inline", "f"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ParseOptTrace = %v, want %v", got, want)
	}
}

func TestBisectOptDecisions(t *testing.T) {
	ds := []OptDecision{{10, "inline", "a"}, {20, "reuse", "b"}, {30, "inline", "c"}, {40, "fuse", "d"}, {50, "reuse", "e"}}
	for _, bad := range ds {
		calls := 0
		got, ok := BisectOptDecisions(ds, func(bound uint64) bool {
			calls++
			return bound > bad.Hash
		})
		if !ok || got != bad {
			t.Errorf("culprit %v: got %v, %v", bad, got, ok)
		}
		if calls > 4 {
			t.Errorf("culprit %v: %d builds, want at most 4 for 5 decisions", bad, calls)
		}
	}
	if _, ok := BisectOptDecisions(ds, func(uint64) bool { return false }); ok {
		t.Error("a failure that survives every decision declined was pinned on one")
	}
	if _, ok := BisectOptDecisions(nil, func(uint64) bool { return true }); ok {
		t.Error("a program with no decisions was pinned on one")
	}
}
