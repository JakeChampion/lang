package fernstring

import (
	"bytes"
	"testing"
)

// Native inline capacity is 15: 8 bytes in `data` + 7 bytes
// in `len`. The flag bit + length nibble take the high byte.
func TestInlineCapMatchesDocs(t *testing.T) {
	if got := InlineCap(8); got != 15 {
		t.Errorf("InlineCap(8) = %d, want 15", got)
	}
	if got := InlineCap(4); got != 7 {
		t.Errorf("InlineCap(4) = %d, want 7", got)
	}
	if got := InlineCap(2); got != 0 {
		t.Errorf("InlineCap(unknown) = %d, want 0", got)
	}
}

// Round-trip every length 0..15 through PackInlineNative /
// UnpackInlineNative to confirm both halves of the layout
// pack + unpack the same bytes.
func TestPackUnpackInlineNativeRoundTrip(t *testing.T) {
	for n := 0; n <= 15; n++ {
		in := make([]byte, n)
		for i := 0; i < n; i++ {
			in[i] = byte(i + 1) // distinguishable per-byte
		}
		data, length := PackInlineNative(in)
		if !IsInlineNative(length) {
			t.Errorf("len(%d): flag bit not set on length", n)
		}
		out := UnpackInlineNative(data, length)
		if !bytes.Equal(in, out) {
			t.Errorf("len(%d): round-trip mismatch in=%v out=%v", n, in, out)
		}
	}
}

// Wasm32 sibling: round-trip every length 0..7.
func TestPackUnpackInlineWasmRoundTrip(t *testing.T) {
	for n := 0; n <= 7; n++ {
		in := make([]byte, n)
		for i := 0; i < n; i++ {
			in[i] = byte(i + 1)
		}
		data, length := PackInlineWasm(in)
		if !IsInlineWasm(length) {
			t.Errorf("len(%d): wasm flag bit not set", n)
		}
		out := UnpackInlineWasm(data, length)
		if !bytes.Equal(in, out) {
			t.Errorf("len(%d): wasm round-trip mismatch in=%v out=%v", n, in, out)
		}
	}
}

// FitsInline* recognises in-cap lengths, rejects over-cap.
func TestFitsInline(t *testing.T) {
	cases := []struct {
		n          int
		fitsNative bool
		fitsWasm   bool
	}{
		{0, true, true},
		{7, true, true},
		{8, true, false},
		{15, true, false},
		{16, false, false},
		{-1, false, false},
		{1000, false, false},
	}
	for _, c := range cases {
		if got := FitsInlineNative(c.n); got != c.fitsNative {
			t.Errorf("FitsInlineNative(%d) = %v, want %v", c.n, got, c.fitsNative)
		}
		if got := FitsInlineWasm(c.n); got != c.fitsWasm {
			t.Errorf("FitsInlineWasm(%d) = %v, want %v", c.n, got, c.fitsWasm)
		}
	}
}

// IsInline* returns false when the flag bit is zero (the
// heap-form encoding) and true when it's set.
func TestIsInline(t *testing.T) {
	if IsInlineNative(0) {
		t.Errorf("IsInlineNative(0) = true, want false (heap-form length)")
	}
	if !IsInlineNative(InlineFlagNative | 7) {
		t.Errorf("IsInlineNative(flag|7) = false, want true")
	}
	if IsInlineWasm(0) {
		t.Errorf("IsInlineWasm(0) = true, want false")
	}
	if !IsInlineWasm(InlineFlagWasm | 5) {
		t.Errorf("IsInlineWasm(flag|5) = false, want true")
	}
}

// Length* extracts the byte length: from the high length-nibble
// for inline-form (matching PackInline*'s encoding) and as-is
// for heap-form (length lives in low bits, flag clear). The
// Masking just the flag bit returns a jumbled
// `(bytes_4_6 | (length << 24))` value for real inline-encoded
// inputs; this pins the behaviour against actual
// PackInline*-shaped values.
func TestLengthMasksFlag(t *testing.T) {
	// Inline-form: length lives in bits 56..59 (native) /
	// 24..26 (wasm). PackInlineNative for "A".."LMNO" emits
	// length=4 in bits 56..59 → 4 << 56.
	if got := LengthNative(InlineFlagNative | (12 << 56)); got != 12 {
		t.Errorf("LengthNative(inline flag|12<<56) = %d, want 12", got)
	}
	if got := LengthNative(0x100); got != 0x100 {
		t.Errorf("LengthNative(0x100) = %d, want 0x100 (heap-form length untouched)", got)
	}
	if got := LengthWasm(InlineFlagWasm | (5 << 24)); got != 5 {
		t.Errorf("LengthWasm(inline flag|5<<24) = %d, want 5", got)
	}
	if got := LengthWasm(0x100); got != 0x100 {
		t.Errorf("LengthWasm(0x100) = %d, want 0x100", got)
	}

	// End-to-end: PackInline* / LengthInline* should round-trip
	// for every length in range. The pre-fix LengthWasm
	// silently miscomputed against the byte-carrying inline
	// values these produce (e.g. "AB" packed as data=0x4241,
	// length=InlineFlag|(2<<24); LengthWasm returned 2<<24
	// instead of 2).
	for n := 0; n <= 7; n++ {
		in := make([]byte, n)
		for i := 0; i < n; i++ {
			in[i] = byte(i + 1)
		}
		_, length := PackInlineWasm(in)
		if got := LengthWasm(length); int(got) != n {
			t.Errorf("PackInlineWasm/LengthWasm round-trip for n=%d: got %d, want %d", n, got, n)
		}
	}
	for n := 0; n <= 15; n++ {
		in := make([]byte, n)
		for i := 0; i < n; i++ {
			in[i] = byte(i + 1)
		}
		_, length := PackInlineNative(in)
		if got := LengthNative(length); int(got) != n {
			t.Errorf("PackInlineNative/LengthNative round-trip for n=%d: got %d, want %d", n, got, n)
		}
	}
}

// Pack panics when given more bytes than the inline cap.
func TestPackInlineNativePanicsOnOverflow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic for 16-byte input")
		}
	}()
	PackInlineNative(make([]byte, 16))
}

func TestPackInlineWasmPanicsOnOverflow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic for 8-byte input")
		}
	}()
	PackInlineWasm(make([]byte, 8))
}

// PackInlineWasm's concrete bit layout for the two-word
// (data, len) encoding. Pins the encoding the wasm two-word
// ABI flip will rely on (see docs/SSO-TWOWORD-EXEC.md) so a
// future drift in either byte boundaries or the flag /
// length nibble position fails the test deliberately.
//
// "AB" — 2 bytes, fits in `data` alone.
// "ABCD" — 4 bytes, fills `data` exactly, len has no inline
// bytes.
// "ABCDEFG" — 7 bytes (the wasm cap), `data` holds 4 bytes,
// `len`'s low 24 bits hold the remaining 3.
func TestPackInlineWasmConcreteLayout(t *testing.T) {
	cases := []struct {
		in       string
		wantData uint32
		wantLen  uint32
	}{
		{
			in:       "",
			wantData: 0,
			wantLen:  InlineFlagWasm | (0 << 24),
		},
		{
			// "A" = 0x41, data byte 0 only; len carries length + flag.
			in:       "A",
			wantData: 0x41,
			wantLen:  InlineFlagWasm | (1 << 24),
		},
		{
			// "AB" = 0x41 (byte 0) | 0x42 << 8 (byte 1) = 0x4241.
			in:       "AB",
			wantData: 0x4241,
			wantLen:  InlineFlagWasm | (2 << 24),
		},
		{
			// "ABCD" = bytes 0..3 in data, no spillover to len.
			in:       "ABCD",
			wantData: 0x44434241,
			wantLen:  InlineFlagWasm | (4 << 24),
		},
		{
			// "ABCDE" = data full + 1 byte in len's low 8.
			in:       "ABCDE",
			wantData: 0x44434241,
			wantLen:  InlineFlagWasm | (5 << 24) | 0x45,
		},
		{
			// "ABCDEFG" = 7 bytes, the wasm32 inline cap.
			// data bytes 0..3 = 0x44434241; len bytes 4..6 = 0x474645.
			in:       "ABCDEFG",
			wantData: 0x44434241,
			wantLen:  InlineFlagWasm | (7 << 24) | 0x474645,
		},
	}
	for _, c := range cases {
		data, length := PackInlineWasm([]byte(c.in))
		if data != c.wantData || length != c.wantLen {
			t.Errorf(`PackInlineWasm(%q) = (data=0x%08x, len=0x%08x); want (data=0x%08x, len=0x%08x)`,
				c.in, data, length, c.wantData, c.wantLen)
		}
	}
}

// PackInlineNative's concrete bit layout for the two-word
// (data, len) encoding on natives. Bytes 0..7 in `data`,
// bytes 8..14 in `len`'s low 56 bits, length nibble in bits
// 56..59, flag in bit 63.
func TestPackInlineNativeConcreteLayout(t *testing.T) {
	cases := []struct {
		in       string
		wantData uint64
		wantLen  uint64
	}{
		{
			in:       "",
			wantData: 0,
			wantLen:  InlineFlagNative | (0 << 56),
		},
		{
			// "A" = byte 0 in data only.
			in:       "A",
			wantData: 0x41,
			wantLen:  InlineFlagNative | (1 << 56),
		},
		{
			// "ABCDEFGH" = bytes 0..7 fill data exactly.
			in:       "ABCDEFGH",
			wantData: 0x4847464544434241,
			wantLen:  InlineFlagNative | (8 << 56),
		},
		{
			// "ABCDEFGHI" = data full + 1 byte in len's low 8.
			in:       "ABCDEFGHI",
			wantData: 0x4847464544434241,
			wantLen:  InlineFlagNative | (9 << 56) | 0x49,
		},
		{
			// "ABCDEFGHIJKLMNO" = 15 bytes, the native inline cap.
			// data = 'A'..'H'; len.bytes 0..6 = 'I'..'O' = 0x4F4E4D4C4B4A49.
			in:       "ABCDEFGHIJKLMNO",
			wantData: 0x4847464544434241,
			wantLen:  InlineFlagNative | (15 << 56) | 0x4F4E4D4C4B4A49,
		},
	}
	for _, c := range cases {
		data, length := PackInlineNative([]byte(c.in))
		if data != c.wantData || length != c.wantLen {
			t.Errorf(`PackInlineNative(%q) = (data=0x%016x, len=0x%016x); want (data=0x%016x, len=0x%016x)`,
				c.in, data, length, c.wantData, c.wantLen)
		}
	}
}

// Inline-form length zero is still INLINE (flag bit set) —
// distinguishes it from a heap-form (data, len) pair where
// both words are zero. The atomic-flip PR's empty-string
// sentinel will use the inline-zero encoding; readers must
// see `IsInline*` return true for it.
func TestInlineZeroLengthIsInline(t *testing.T) {
	data, length := PackInlineWasm(nil)
	if data != 0 {
		t.Errorf("PackInlineWasm(nil) data = 0x%08x, want 0", data)
	}
	if !IsInlineWasm(length) {
		t.Errorf("PackInlineWasm(nil) len = 0x%08x; flag bit must be set even for empty inline", length)
	}
	if got := LengthWasm(length); got != 0 {
		t.Errorf("LengthWasm of inline empty = %d, want 0", got)
	}

	dataN, lengthN := PackInlineNative(nil)
	if dataN != 0 {
		t.Errorf("PackInlineNative(nil) data = 0x%016x, want 0", dataN)
	}
	if !IsInlineNative(lengthN) {
		t.Errorf("PackInlineNative(nil) len = 0x%016x; flag bit must be set even for empty inline", lengthN)
	}
	if got := LengthNative(lengthN); got != 0 {
		t.Errorf("LengthNative of inline empty = %d, want 0", got)
	}
}
