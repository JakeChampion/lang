// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/wit_user_world_test.go.
package e2eharness

import "testing"

// ExtractComponentType returns the payload of the `component-type` custom
// section embedded in a core module (the bytes DecodeWorldBytes consumes).
func ExtractComponentType(t *testing.T, core []byte) []byte {
	t.Helper()
	readU := func(b []byte) (uint64, int) {
		var v uint64
		var s uint
		for i, c := range b {
			v |= uint64(c&0x7f) << s
			if c&0x80 == 0 {
				return v, i + 1
			}
			s += 7
		}
		return 0, 0
	}
	pos := 8 // core module header
	for pos < len(core) {
		id := core[pos]
		pos++
		size, n := readU(core[pos:])
		if n == 0 {
			break
		}
		pos += n
		body := core[pos : pos+int(size)]
		pos += int(size)
		if id != 0 { // custom section
			continue
		}
		nameLen, k := readU(body)
		if string(body[k:k+int(nameLen)]) == "component-type" {
			return body[k+int(nameLen):]
		}
	}
	t.Fatal("no component-type custom section found")
	return nil
}
