package ssa

import "fmt"

// Stats holds a snapshot of structural counters for a Func.
// Useful for benchmarking pass effectiveness ("Optimize
// reduced 47 ops to 12") and for PR reviews where you want to
// quantify the diff a pass made to a synthetic input.
//
// Counters are collected by walking every Block + Op once;
// O(N) cost. Build a fresh Stats whenever you need a
// snapshot — Stats doesn't auto-update when the Func mutates.
type Stats struct {
	Blocks        int              // total Block count in f.Blocks
	Reachable     int              // count of Blocks reachable from f.Entry (≤ Blocks)
	Ops           int              // total Op count across all Blocks
	Impure        int              // count of !IsPure Ops (Call/Load/Store/Alloc/MakeClosure/Env)
	Phis          int              // OpPhi count (subset of Ops)
	BlocksWithPhi int              // count of Blocks containing ≥1 OpPhi (join points)
	Consts        int              // ConstInt/Bool/String count (subset of Ops)
	Params        int              // len(f.Params), excluding the zero sentinel
	MaxBlockOps   int              // length of the longest Block's Ops list
	Terminators   map[TermKind]int // distribution by kind
	OpKinds       map[OpKind]int   // distribution by Op.Kind
}

// Stats walks `f` and returns a Stats snapshot.
func (f *Func) Stats() Stats {
	s := Stats{
		Terminators: map[TermKind]int{},
		OpKinds:     map[OpKind]int{},
	}
	if f == nil {
		return s
	}
	s.Blocks = len(f.Blocks)
	if reachable := Reachable(f); len(reachable) > 0 {
		s.Reachable = len(reachable)
	}
	for _, p := range f.Params {
		if p.IsValid() {
			s.Params++
		}
	}
	for _, b := range f.Blocks {
		if n := len(b.Ops); n > s.MaxBlockOps {
			s.MaxBlockOps = n
		}
		s.Ops += len(b.Ops)
		blockHasPhi := false
		for _, op := range b.Ops {
			s.OpKinds[op.Kind]++
			switch op.Kind {
			case OpPhi:
				s.Phis++
				blockHasPhi = true
			case OpConstInt, OpConstBool, OpConstString:
				s.Consts++
			}
			if !IsPure(op.Kind) {
				s.Impure++
			}
		}
		if blockHasPhi {
			s.BlocksWithPhi++
		}
		s.Terminators[b.Term.Kind]++
	}
	return s
}

// String renders a Stats in a single-line summary form
// (`blocks=3 reachable=3 ops=14 phis=2 consts=1 params=2`).
// Useful for log lines and benchmark output. `reachable` is
// distinct from `blocks` whenever PruneUnreachable hasn't run
// yet (or skipped a block); a healthy post-Optimize function
// has reachable==blocks.
func (s Stats) String() string {
	return fmt.Sprintf("blocks=%d reachable=%d ops=%d impure=%d phis=%d consts=%d params=%d max_block_ops=%d",
		s.Blocks, s.Reachable, s.Ops, s.Impure, s.Phis, s.Consts, s.Params, s.MaxBlockOps)
}

// Sub returns a Stats whose scalar fields are `s` minus
// `other`, component-wise. Useful for benchmarking pass
// effectiveness:
//
//	before := f.Stats()
//	Optimize(f)
//	delta := before.Sub(f.Stats())
//	// delta.Ops > 0 → Optimize removed that many ops
//
// MaxBlockOps subtracts directly even though "delta of max"
// isn't a max — it's the more useful signed delta of the
// two snapshots' values.
//
// The Terminators and OpKinds maps subtract per-key; absent
// keys default to zero (so `delta.OpKinds[OpAdd]` always
// reads `before - after`, even if `after` had no Add ops).
func (s Stats) Sub(other Stats) Stats {
	out := Stats{
		Blocks:        s.Blocks - other.Blocks,
		Reachable:     s.Reachable - other.Reachable,
		Ops:           s.Ops - other.Ops,
		Impure:        s.Impure - other.Impure,
		Phis:          s.Phis - other.Phis,
		BlocksWithPhi: s.BlocksWithPhi - other.BlocksWithPhi,
		Consts:        s.Consts - other.Consts,
		Params:        s.Params - other.Params,
		MaxBlockOps:   s.MaxBlockOps - other.MaxBlockOps,
		Terminators:   map[TermKind]int{},
		OpKinds:       map[OpKind]int{},
	}
	for k, v := range s.Terminators {
		out.Terminators[k] = v - other.Terminators[k]
	}
	for k, v := range other.Terminators {
		if _, seen := s.Terminators[k]; !seen {
			out.Terminators[k] = -v
		}
	}
	for k, v := range s.OpKinds {
		out.OpKinds[k] = v - other.OpKinds[k]
	}
	for k, v := range other.OpKinds {
		if _, seen := s.OpKinds[k]; !seen {
			out.OpKinds[k] = -v
		}
	}
	return out
}
