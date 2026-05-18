// Package leb128 is the Go-side mirror of internal/stdlib/std/wasm/leb128.lang.
//
// Same function set, same semantics, same wire format — the two
// implementations stay in lock-step so that when the compiler
// later runs in Lang, the produced bytes match what the Go
// driver produces today. The package is part of the Phase 1
// foundation laid out in docs/TOOLCHAIN-SELF-HOSTING.md.
//
// Spec: https://webassembly.github.io/spec/core/binary/values.html
// (the "Integers" subsection — ULEB128 for unsigned counts /
// indices, SLEB128 for signed immediates).
//
// Calling convention mirrors the Lang side: each function takes
// a byte slice + a value, appends the encoded bytes, and
// returns the (possibly reallocated) slice. No I/O, no state.
package leb128

// UlebU32 appends the ULEB128 encoding of v to buf and returns
// the extended buffer. 1-5 bytes for any uint32.
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

// UlebU64 is UlebU32 widened to 64 bits. 1-10 bytes.
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

// SlebI32 appends the SLEB128 encoding of v to buf. 1-5 bytes.
// Terminator: the remaining value matches the sign-extension of
// the just-emitted byte's bit-6 (a final 0 with bit-6 clear
// stops a positive run; a final -1 with bit-6 set stops a
// negative run).
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

// SlebI64 is SlebI32 widened to 64 bits. 1-10 bytes.
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

// UlebSizeU32 returns the number of bytes UlebU32 will emit for
// v. Used to pre-size buffers when the length is needed before
// the body is encoded (wasm section headers, for example,
// carry the body's size as a uleb prefix).
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

// UlebSizeU64 is UlebSizeU32 widened to 64 bits.
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
