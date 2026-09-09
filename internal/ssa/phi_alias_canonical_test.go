package ssa

import "testing"

func TestPhiAliasCanonicalizationMatchesChainResolver(t *testing.T) {
	f := NewFunc("aliases")
	values := []Value{{}}
	for range 5 {
		values = append(values, f.NewValue())
	}
	// Every functional graph on four alias nodes, with either a live terminal
	// or an invalid terminal too: 6^4 maps. Includes prefixes, fan-in, self and
	// multi-node cycles. Compare the pre-existing resolver independently.
	for encoded := 0; encoded < 6*6*6*6; encoded++ {
		sub := make(ValueAliases)
		n := encoded
		for id := 1; id <= 4; id++ {
			sub[int32(id)] = values[n%len(values)]
			n /= len(values)
		}
		want := make([]Value, len(values))
		for i, value := range values {
			want[i] = resolveValue(value, sub)
		}
		canonicalizePhiAliases(f, sub)
		for i, value := range values {
			if got := resolveValue(value, sub); got != want[i] {
				t.Fatalf("map %d value %v: canonical %v, original %v", encoded, value, got, want[i])
			}
		}
	}
}
