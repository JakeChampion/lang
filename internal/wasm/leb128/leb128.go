package leb128

func UlebU32(buf []byte, v uint32) []byte {
	for {
		lo := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(buf, lo)
		}
		buf = append(buf, lo|0x80)
	}
}

func UlebU64(buf []byte, v uint64) []byte {
	for {
		lo := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(buf, lo)
		}
		buf = append(buf, lo|0x80)
	}
}

func SlebI32(buf []byte, v int32) []byte {
	for {
		b := v & 0x7f
		v >>= 7
		signSet := (b & 0x40) != 0
		done := (v == 0 && !signSet) || (v == -1 && signSet)
		if done {
			return append(buf, byte(b))
		}
		buf = append(buf, byte(b)|0x80)
	}
}

func SlebI64(buf []byte, v int64) []byte {
	for {
		b := v & 0x7f
		v >>= 7
		signSet := (b & 0x40) != 0
		done := (v == 0 && !signSet) || (v == -1 && signSet)
		if done {
			return append(buf, byte(b))
		}
		buf = append(buf, byte(b)|0x80)
	}
}

func UlebSizeU32(v uint32) int {
	n := 1
	for {
		v >>= 7
		if v == 0 {
			return n
		}
		n++
	}
}

func UlebSizeU64(v uint64) int {
	n := 1
	for {
		v >>= 7
		if v == 0 {
			return n
		}
		n++
	}
}
