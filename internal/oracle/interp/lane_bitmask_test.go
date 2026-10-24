package interp

import (
	"fmt"
	"testing"
)

func TestLaneBitmaskPreservesRawSigns(t *testing.T) {
	i := &Interp{Builtins: map[string]*Builtin{}}
	i.registerLaneBuiltins()
	for _, width := range []int{32, 64} {
		name := fmt.Sprintf("__f%dx%d_bitmask", width, 128/width)
		for mask := 0; mask < 1<<(128/width); mask++ {
			lanes := Lanes{Width: width, Bits: make([]uint64, 128/width)}
			for lane := range lanes.Bits {
				// Include zero, NaNs, and arbitrary payloads without float conversion.
				lanes.Bits[lane] = []uint64{0, signBit(width) - 1, 1, signBit(width) >> 1}[lane]
				if mask&(1<<lane) != 0 {
					lanes.Bits[lane] |= signBit(width)
				}
			}
			got, err := i.Builtins[name].Fn(i, []Value{lanes})
			if err != nil || got != Number(mask) {
				t.Fatalf("%s mask %d = %v, %v", name, mask, got, err)
			}
		}
	}
}
