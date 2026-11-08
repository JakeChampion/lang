package interp

import (
	"fmt"
	"testing"
)

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
		{[]byte(""), 0, -1164367578},
		{[]byte("a"), 0, 690280484},
		{[]byte("abcdefgh"), 0, -810889936},
		{[]byte("abcdefghi"), 0, -989501627},
		{[]byte("hello, world"), 7, -517647510},
		{[]byte("hello, world"), -1, 1691401939},
		{[]byte("__fn_ssa_lift__lift_impl"), 208357, 1848727984},
		{high, -123456, -730865203},
	}
	for _, c := range cases {
		if got := int32(StrHash(c.s, c.seed)); got != c.want {
			t.Errorf("StrHash(%q, %d) = %d, want %d", c.s, c.seed, got, c.want)
		}
	}
}

// Names that differ only in their last bytes spread over the low bits that a
// power-of-two table masks. Before the finalizer, 3000 such names put 100 in
// one of 8192 buckets.
func TestStrHashSpreadsTheLowBits(t *testing.T) {
	for _, form := range []string{"S%d", "twice__S%d", "__fn_module__function_name_%d"} {
		counts := map[uint32]int{}
		worst := 0
		for i := 0; i < 3000; i++ {
			b := StrHash([]byte(fmt.Sprintf(form, i)), 0) & 8191
			counts[b]++
			worst = max(worst, counts[b])
		}
		if worst > 8 {
			t.Errorf("%s: %d of 3000 names share one of 8192 buckets", form, worst)
		}
	}
}
