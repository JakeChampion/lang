package semir

import "github.com/jakechampion/lang/internal/ssa"

// Lifetime end is absence of a place, not destruction of its old SSA value.
// The value may still be held by a returned snapshot or another binding. These
// masks are derived only after boundary/exit verification and have no runtime
// representation. Unentered branch-local places are safe to end without reads.
func bindingLifetimeEnds(f *Func) map[*ssa.Block][]uint64 {
	if len(f.bindings) == 0 || len(f.cleanupExits) == 0 {
		return nil
	}
	words := (len(f.bindings) + 63) / 64
	masks := make(map[*cleanupBoundary][]uint64)
	for index, binding := range f.bindings {
		if binding.boundary == nil {
			continue
		}
		mask := masks[binding.boundary]
		if mask == nil {
			mask = make([]uint64, words)
			masks[binding.boundary] = mask
		}
		mask[index/64] |= uint64(1) << (index % 64)
	}
	ends := make(map[*ssa.Block][]uint64)
	for _, exit := range f.cleanupExits {
		for _, end := range exit.ends {
			mask := masks[end.boundary]
			if mask == nil {
				continue
			}
			if ends[end.block] == nil {
				ends[end.block] = append([]uint64(nil), mask...)
			} else {
				for i, bits := range mask {
					ends[end.block][i] |= bits
				}
			}
		}
	}
	return ends
}
