package ssa

import "math"

// fingerprint hashes everything the optimisation passes can change: the block
// list, each op's kind, results, operands, immediates, width and address flag,
// the predecessor lists and the terminators. Optimize compares fingerprints to
// tell whether an iteration changed the function, where rendering the function
// to text cost more than the passes it was checking. A hash collision ends the
// loop an iteration early, which costs an optimisation, never correctness.
func fingerprint(f *Func) uint64 {
	h := fp(fpOffset)
	h = h.int(int64(len(f.Blocks)))
	for _, blk := range f.Blocks {
		h = h.int(int64(blk.ID)).int(int64(len(blk.Ops)))
		for _, op := range blk.Ops {
			h = h.int(int64(op.Kind)).int(int64(op.Result.ID)).int(int64(op.Result2.ID))
			h = h.values(op.Args)
			h = h.int(op.Imm).int(int64(math.Float64bits(op.F64))).str(op.Str)
			h = h.int(int64(op.Width)).bool(op.Addr).int(int64(len(op.CaptureSlots)))
			for _, c := range op.CaptureSlots {
				h = h.int(int64(c))
			}
		}
		h = h.int(int64(len(blk.Preds)))
		for _, p := range blk.Preds {
			h = h.int(int64(blockID(p)))
		}
		t := &blk.Term
		h = h.int(int64(t.Kind)).int(int64(t.Cond.ID))
		h = h.int(int64(blockID(t.Target))).int(int64(blockID(t.True))).int(int64(blockID(t.False)))
		h = h.int(int64(t.Value.ID)).int(int64(t.Value2.ID))
	}
	return uint64(h)
}

// fp is a 64-bit FNV-1a state, mixed a word at a time.
type fp uint64

const (
	fpOffset fp = 14695981039346656037
	fpPrime  fp = 1099511628211
)

func (h fp) int(x int64) fp { return (h ^ fp(uint64(x))) * fpPrime }

func (h fp) bool(b bool) fp {
	if b {
		return h.int(1)
	}
	return h.int(0)
}

func (h fp) str(s string) fp {
	h = h.int(int64(len(s)))
	for i := 0; i < len(s); i++ {
		h = (h ^ fp(s[i])) * fpPrime
	}
	return h
}

func (h fp) values(vs []Value) fp {
	h = h.int(int64(len(vs)))
	for _, v := range vs {
		h = h.int(int64(v.ID))
	}
	return h
}
