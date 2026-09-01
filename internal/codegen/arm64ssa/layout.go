package arm64ssa

import (
	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
)

// layoutOrder returns the order in which p's blocks are written to the
// assembly, chosen so that as many branches as possible target the block that
// physically follows and can therefore be dropped.
//
// The abstract emitter assigns block indices in the lifter's creation order,
// with critical-edge splits appended after every real block, so emitting in
// index order leaves an unconditional branch in front of nearly every label.
// The walk below instead follows each block's preferred fallthrough successor
// (a jump's target; a conditional's false arm) as far as it can, parking the
// other successor for a later chain.
//
// Block *identity* is unchanged: callers keep labelling each block by its
// index in p.Blocks, so no branch target needs remapping.
func layoutOrder(p *x86.Program) []int {
	n := len(p.Blocks)
	order := make([]int, 0, n)
	placed := make([]bool, n)
	var pending []int

	inRange := func(i int) bool { return i >= 0 && i < n }

	push := func(i int) {
		if inRange(i) && !placed[i] {
			pending = append(pending, i)
		}
	}

	start := p.Entry
	if !inRange(start) {
		start = 0
	}
	for cur := start; ; {
		for inRange(cur) && !placed[cur] {
			placed[cur] = true
			order = append(order, cur)
			next := -1
			switch t := p.Blocks[cur].Term; t.Kind {
			case x86.TJmp:
				next = t.Target
			case x86.TBrIf:
				push(t.True)
				next = t.False
			}
			cur = next
		}
		cur = -1
		for len(pending) > 0 {
			c := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if !placed[c] {
				cur = c
				break
			}
		}
		if cur < 0 {
			break
		}
	}

	// Anything the walk could not reach (an unreferenced block) still has to be
	// written, or its label would dangle.
	for i := 0; i < n; i++ {
		if !placed[i] {
			order = append(order, i)
		}
	}
	return order
}
