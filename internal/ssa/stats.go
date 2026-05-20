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
	Blocks      int              // total Block count in f.Blocks
	Reachable   int              // count of Blocks reachable from f.Entry (≤ Blocks)
	Ops         int              // total Op count across all Blocks
	Phis        int              // OpPhi count (subset of Ops)
	Consts      int              // ConstInt/Bool/String count (subset of Ops)
	Params      int              // len(f.Params), excluding the zero sentinel
	MaxBlockOps int              // length of the longest Block's Ops list
	Terminators map[TermKind]int // distribution by kind
	OpKinds     map[OpKind]int   // distribution by Op.Kind
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
		for _, op := range b.Ops {
			s.OpKinds[op.Kind]++
			switch op.Kind {
			case OpPhi:
				s.Phis++
			case OpConstInt, OpConstBool, OpConstString:
				s.Consts++
			}
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
	return fmt.Sprintf("blocks=%d reachable=%d ops=%d phis=%d consts=%d params=%d max_block_ops=%d",
		s.Blocks, s.Reachable, s.Ops, s.Phis, s.Consts, s.Params, s.MaxBlockOps)
}
