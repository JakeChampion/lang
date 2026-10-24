package e2eharness

import "testing"

func ComponentCoreSection(t *testing.T, b []byte) []byte {
	t.Helper()
	i := 8
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
