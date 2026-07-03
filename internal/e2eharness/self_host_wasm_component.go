// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_wasm_component_test.go.
package e2eharness

import "testing"

// ComponentCoreSection returns the bytes of the first core-module section
// (component section id 1) of a component binary.
func ComponentCoreSection(t *testing.T, b []byte) []byte {
	t.Helper()
	i := 8 // skip preamble
	for i < len(b) {
		sid := b[i]
		i++
		size := 0
		shift := 0
		for {
			x := b[i]
			i++
			size |= int(x&0x7f) << shift
			if x&0x80 == 0 {
				break
			}
			shift += 7
		}
		if sid == 1 {
			return b[i : i+size]
		}
		i += size
	}
	t.Fatal("no core-module section in component")
	return nil
}
