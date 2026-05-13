package langstring

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
		n           int
		fitsNative  bool
		fitsWasm    bool
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

// Length* masks the flag bit but leaves heap-form lengths
// untouched.
func TestLengthMasksFlag(t *testing.T) {
	if got := LengthNative(InlineFlagNative | 12); got != 12 {
		t.Errorf("LengthNative(flag|12) = %d, want 12", got)
	}
	if got := LengthNative(0x100); got != 0x100 {
		t.Errorf("LengthNative(0x100) = %d, want 0x100 (heap-form length untouched)", got)
	}
	if got := LengthWasm(InlineFlagWasm | 5); got != 5 {
		t.Errorf("LengthWasm(flag|5) = %d, want 5", got)
	}
	if got := LengthWasm(0x100); got != 0x100 {
		t.Errorf("LengthWasm(0x100) = %d, want 0x100", got)
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
