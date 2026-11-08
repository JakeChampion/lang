package interp

import (
	"fmt"
	"testing"
)

func TestLanePermutePreservesBits(t *testing.T) {
	i := &Interp{Builtins: map[string]*Builtin{}}
	i.registerLaneBuiltins()
	for _, width := range []int{32, 64} {
		count := 128 / width
		a := Lanes{Width: width, Bits: []uint64{0x7f800001, 0x80000000, 0x7fc45678, 0xffffffff}[:count]}
		b := Lanes{Width: width, Bits: []uint64{0xdeadbeef, 0x7f800000, 1, 2}[:count]}
		if width == 64 {
			a.Bits = []uint64{0x7ff0000000000001, 0x8000000000000000}
			b.Bits = []uint64{0xfff8001234567890, 0x123456789abcdef0}
		}
		for k, want := range a.Bits {
			name := fmt.Sprintf("__f%dx%d_lane_bits", width, count)
			got, err := i.Builtins[name].Fn(i, []Value{a, Number(k)})
			if err != nil || got != Number(int64(want)) {
				t.Fatalf("%s lane %d: %v, %v", name, k, got, err)
			}
		}
		for _, verb := range []string{"swap_halves", "reverse", "zip_lo", "zip_hi"} {
			name := fmt.Sprintf("__f%dx%d_%s", width, count, verb)
			args := []Value{a, b}
			if verb == "reverse" || verb == "swap_halves" {
				args = args[:1]
			}
			got, err := i.Builtins[name].Fn(i, args)
			if err != nil {
				t.Fatal(err)
			}
			out := got.(Lanes)
			for k, bits := range out.Bits {
				source, at := a, k/2
				if k&1 != 0 {
					source = b
				}
				if verb == "reverse" {
					source, at = a, count-1-k
				}
				if verb == "swap_halves" {
					source, at = a, (k+count/2)%count
				}
				if verb == "zip_hi" {
					at += count / 2
				}
				if bits != source.Bits[at] {
					t.Fatalf("%s lane %d: %x, want %x", name, k, bits, source.Bits[at])
				}
			}
		}
	}
}
