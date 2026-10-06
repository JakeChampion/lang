package interp

import "testing"

// The pins were computed by an independent implementation of
// compiler/ir.fern's str_hash definition, so every engine is held to the
// definition rather than to each other.
func TestStrHashPins(t *testing.T) {
	high := make([]byte, 0, 56)
	for c := 200; c < 256; c++ {
		high = append(high, byte(c))
	}
	cases := []struct {
		s    []byte
		seed int32
		want int32
	}{
		{[]byte(""), 0, 1339080641},
		{[]byte("a"), 0, 694301555},
		{[]byte("abcdefgh"), 0, 939000468},
		{[]byte("abcdefghi"), 0, -2056207145},
		{[]byte("hello, world"), 7, -196944044},
		{[]byte("hello, world"), -1, -194608413},
		{[]byte("__fn_ssa_lift__lift_impl"), 208357, -43142800},
		{high, -123456, -1417042185},
	}
	for _, c := range cases {
		if got := int32(StrHash(c.s, c.seed)); got != c.want {
			t.Errorf("StrHash(%q, %d) = %d, want %d", c.s, c.seed, got, c.want)
		}
	}
}
