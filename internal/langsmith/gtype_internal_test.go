package langsmith

import "testing"

// TestGtypeNamesCoversEveryBuiltin pins the static `gtypeNames`
// table to the iota const block. Adding a new builtin gtype is
// three slots — the const, an `allTypes` entry, and an entry in
// `gtypeNames`. Forgetting the third one would silently make
// `gtype.String()` and `typeName()` emit "?dyn<N>" for that
// builtin instead of its source-level name. This test catches
// the omission at `go test` time. IMPROVEMENTS.md #11.
func TestGtypeNamesCoversEveryBuiltin(t *testing.T) {
	for i := gtype(0); i < numTypes; i++ {
		if gtypeNames[i] == "" {
			t.Errorf("gtypeNames[%d] is empty; missing entry for builtin gtype #%d", int(i), int(i))
		}
	}
	// allTypes and gtypeNames must agree on length.
	if len(allTypes) != len(gtypeNames) {
		t.Errorf("len(allTypes)=%d != len(gtypeNames)=%d", len(allTypes), len(gtypeNames))
	}
	// Spot-check a couple entries so a sloppy reorder of the
	// const block (which would silently realign every entry)
	// gets caught against the source-level spelling we promise
	// users in generated programs.
	if got, want := tI32.String(), "i32"; got != want {
		t.Errorf("tI32.String() = %q, want %q", got, want)
	}
	if got, want := tResI32I32.String(), "Result[i32, i32]"; got != want {
		t.Errorf("tResI32I32.String() = %q, want %q", got, want)
	}
}

// TestGtypeStringOnDynamicReturnsPlaceholder documents that
// `gtype.String()` (value receiver, no generator state) returns
// a "?dyn<N>" placeholder for nominal types beyond the builtin
// range. Callers that emit type names into generated source
// must route through `Generator.typeName(t)` instead.
func TestGtypeStringOnDynamicReturnsPlaceholder(t *testing.T) {
	dyn := numTypes + 3
	got := dyn.String()
	want := "?dyn" + itoa(int(dyn))
	if got != want {
		t.Errorf("gtype(%d).String() = %q, want %q", int(dyn), got, want)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
