// Package langstring captures the two-word string
// representation the language is migrating to under the SSO
// arc (see docs/SSO-PLAN.md). A LangString is the Go-side
// view of the same `(data, len)` pair an operand-stack slot
// will hold once the migration completes:
//
//	data  : pointer-width word (heap pointer OR inline bytes)
//	len   : pointer-width word (length, with top bit flagging
//	        inline storage)
//
// The flag bit lives in `len.top_bit` so the length-on-stack
// access path stays a single load + mask on the readers.
// Inline form packs `data`'s bytes first, then `len`'s low
// bytes, so a 7-byte (wasm32) / 15-byte (native) string fits
// in the two registers / slots without touching the heap.
//
// This package is the AUTHORITATIVE source for the SSO
// constants — backends and the IR layer ALL import from here
// rather than re-deriving the inline cap / flag-bit shape.
// Step 2 of the SSO plan; no IR / codegen wired through yet.
package langstring

// InlineFlagNative is the top bit of `len` on native targets
// (8-byte words). When set, `data` holds the first 8 bytes of
// inline storage and `len`'s low 56 bits hold a 7-bit length
// plus the remaining 7 bytes — total 15 inline bytes.
const InlineFlagNative uint64 = 1 << 63

// InlineFlagWasm is the top bit of `len` on wasm32. When set,
// `data` holds the first 4 inline bytes and `len`'s low 24
// bits hold a 3-byte tail plus a length — total 7 inline
// bytes.
const InlineFlagWasm uint32 = 1 << 31

// InlineCap returns the inline-storage capacity in bytes for
// a target with pointer width `ptrW` (4 on wasm32, 8 on
// native). Returns 0 for unknown ptrW.
func InlineCap(ptrW int) int {
	switch ptrW {
	case 4:
		// data (4 bytes) + low 3 bytes of len = 7. The other
		// byte of len holds the length nibble + flag bit.
		return 7
	case 8:
		// data (8 bytes) + low 7 bytes of len = 15. The high
		// byte of len holds the length nibble + flag bit.
		return 15
	}
	return 0
}

// IsInlineWasm reports whether a wasm32 `len` word's flag bit
// is set. Pair with `IsInlineNative` on natives. A reader that
// doesn't know the target picks via `ptrW`.
func IsInlineWasm(len uint32) bool {
	return len&InlineFlagWasm != 0
}

// IsInlineNative reports whether a native `len` word's flag
// bit is set.
func IsInlineNative(len uint64) bool {
	return len&InlineFlagNative != 0
}

// LengthWasm extracts the byte length from a wasm32 `len`
// word, masking the flag bit off. Caller has already
// established inline-ness via `IsInlineWasm`; this function
// works equally well for heap-form strings (the flag bit is
// zero there, so the mask is a no-op).
func LengthWasm(len uint32) uint32 {
	return len &^ InlineFlagWasm
}

// LengthNative is the native sibling of LengthWasm.
func LengthNative(len uint64) uint64 {
	return len &^ InlineFlagNative
}

// FitsInlineNative reports whether `n` bytes fit in the
// native inline form (≤15).
func FitsInlineNative(n int) bool {
	return n >= 0 && n <= InlineCap(8)
}

// FitsInlineWasm reports whether `n` bytes fit in the wasm32
// inline form (≤7).
func FitsInlineWasm(n int) bool {
	return n >= 0 && n <= InlineCap(4)
}

// PackInlineNative packs up to 15 bytes into a (data, len)
// pair for the native two-word inline form. Bytes 0..7 go
// into `data`, bytes 8..14 go into `len`'s low 56 bits, and
// the length nibble (4 bits, fits 0..15) goes into bits 56..59
// of `len`. The top bit of `len` is set to flag inline
// storage.
//
// Panics if len(b) > 15 — caller is expected to have checked
// `FitsInlineNative` first.
func PackInlineNative(b []byte) (data uint64, length uint64) {
	if len(b) > InlineCap(8) {
		panic("langstring: PackInlineNative called with >15 bytes")
	}
	for i := 0; i < len(b) && i < 8; i++ {
		data |= uint64(b[i]) << (8 * i)
	}
	for i := 8; i < len(b); i++ {
		length |= uint64(b[i]) << (8 * (i - 8))
	}
	// Length lives in bits 56..63 of `length` BELOW the flag
	// bit. 0..15 fits in 4 bits — bits 56..59. The flag bit
	// at 63 is the top bit.
	length |= uint64(len(b)) << 56
	length |= InlineFlagNative
	return data, length
}

// PackInlineWasm is the wasm32 sibling of PackInlineNative.
// Bytes 0..3 go into `data`, bytes 4..6 go into the low 24
// bits of `len`, bits 24..30 hold the length (0..7 fits in
// 3 bits, room left over), and bit 31 is the inline flag.
//
// Panics if len(b) > 7.
func PackInlineWasm(b []byte) (data uint32, length uint32) {
	if len(b) > InlineCap(4) {
		panic("langstring: PackInlineWasm called with >7 bytes")
	}
	for i := 0; i < len(b) && i < 4; i++ {
		data |= uint32(b[i]) << (8 * i)
	}
	for i := 4; i < len(b); i++ {
		length |= uint32(b[i]) << (8 * (i - 4))
	}
	length |= uint32(len(b)) << 24
	length |= InlineFlagWasm
	return data, length
}

// UnpackInlineNative reverses PackInlineNative: returns the
// inline bytes given a `(data, len)` pair whose flag bit is
// set. Panics if `len.flag` is zero — caller should have
// checked `IsInlineNative` first.
func UnpackInlineNative(data, length uint64) []byte {
	if !IsInlineNative(length) {
		panic("langstring: UnpackInlineNative called on non-inline (data, len)")
	}
	n := int((length >> 56) & 0xF) // 4 bits of length, 0..15
	out := make([]byte, n)
	for i := 0; i < n && i < 8; i++ {
		out[i] = byte(data >> (8 * i))
	}
	for i := 8; i < n; i++ {
		out[i] = byte(length >> (8 * (i - 8)))
	}
	return out
}

// UnpackInlineWasm reverses PackInlineWasm.
func UnpackInlineWasm(data, length uint32) []byte {
	if !IsInlineWasm(length) {
		panic("langstring: UnpackInlineWasm called on non-inline (data, len)")
	}
	n := int((length >> 24) & 0x7) // 3 bits of length, 0..7
	out := make([]byte, n)
	for i := 0; i < n && i < 4; i++ {
		out[i] = byte(data >> (8 * i))
	}
	for i := 4; i < n; i++ {
		out[i] = byte(length >> (8 * (i - 4)))
	}
	return out
}

// TinyInlineCapWasm is the inline-storage capacity (in bytes) of the
// single-i32 "tiny" SSO form used by the wasm backend while the
// operand-stack ABI is still a single pointer-shaped slot per string.
// Three bytes is the largest count that fits alongside a 3-bit length
// field and the top-bit inline flag inside one i32. The downstream
// two-word layout will lift this to InlineCap(4)=7 once the operand-
// stack ABI widens; until then this constant gates which literals /
// concat results can avoid the data-segment / heap detour.
const TinyInlineCapWasm = 3

// PackTinyWasm packs up to TinyInlineCapWasm (3) bytes of `b` into a
// single i32 inline-tagged value. Returns `(packed, true)` on a fit;
// `(0, false)` when len(b) exceeds the cap so the caller can fall
// back to a heap-form representation.
//
// Layout (LSB→MSB):
//
//	bits  0..7   byte 0 (zero when length < 1)
//	bits  8..15  byte 1 (zero when length < 2)
//	bits 16..23  byte 2 (zero when length < 3)
//	bits 24..26  length (0..3, encoded as 3 bits for headroom)
//	bits 27..30  reserved (zero)
//	bit  31      inline flag (1) — matches InlineFlagWasm so the
//	             top-bit-test path used by readers stays identical
//	             between the two-word and the single-i32 forms.
func PackTinyWasm(b []byte) (uint32, bool) {
	if len(b) > TinyInlineCapWasm {
		return 0, false
	}
	var v uint32
	for i := 0; i < len(b); i++ {
		v |= uint32(b[i]) << (8 * i)
	}
	v |= uint32(len(b)) << 24
	v |= InlineFlagWasm
	return v, true
}

// IsTinyInlineWasm reports whether `v` is encoded in the single-i32
// inline form (top-bit set). Identical to IsInlineWasm on the length
// word; the helper exists with its own name so the wasm backend's
// callers self-document the encoding they expect.
func IsTinyInlineWasm(v uint32) bool {
	return v&InlineFlagWasm != 0
}

// LengthTinyWasm extracts the byte length from a single-i32 inline-
// encoded value. Defined only when IsTinyInlineWasm(v) is true; the
// caller is expected to have checked.
func LengthTinyWasm(v uint32) uint32 {
	return (v >> 24) & 0x7
}

// UnpackTinyWasm reverses PackTinyWasm: returns the inline bytes for
// a single-i32 inline-encoded value. Panics when the flag bit is
// zero — that's a heap-form pointer and the caller should have
// dispatched the other way.
func UnpackTinyWasm(v uint32) []byte {
	if !IsTinyInlineWasm(v) {
		panic("langstring: UnpackTinyWasm called on non-inline value")
	}
	n := int(LengthTinyWasm(v))
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = byte(v >> (8 * i))
	}
	return out
}
