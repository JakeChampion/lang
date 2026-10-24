package e2eharness

import "testing"

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
	pos := 8
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
		if id != 0 {
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
