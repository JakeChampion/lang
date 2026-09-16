package x86_64

import (
	"fmt"
	"sort"
)

// relaxEvent is one variable-size span of .text, recorded at emission in
// offset order: a relaxable branch (jmp/jcc to a label, emitted rel32 and
// shrinkable to the 2-byte rel8 form), or an alignment pad, whose width
// depends on the offset it ends up at. Calls and indirect branches are not
// events — call has no short form.
type relaxEvent struct {
	start int // .text offset of the span, pre-relaxation
	size  int // emitted byte length, pre-relaxation
	// Final-layout placement, filled in by relax:
	newStart int
	newSize  int
	// Branch (align == 0): the relFixups entry holding the target symbol.
	fixup int
	short bool
	// fixed marks a branch-class instruction with one encoding — a call, a
	// ret, an indirect jump — recorded only so that branch alignment can
	// pad it; it never shrinks and has no fixup of its own.
	fixed bool
	// bpad is the NOP padding laid before the branch under branch
	// alignment, so that its bytes neither cross nor end on a 32-byte
	// boundary (see Assembler.SetBranchAlignment). Filled in by relax.
	bpad int
	// Alignment pad (align > 1):
	align   int
	maxSkip int // -1 when absent
	// Labels / .loc rows defined at this pad's post edge. An empty pad
	// shares its offset with what precedes it, and can grow during
	// relaxation; only the recording order says which side of the new NOPs
	// each offset belongs on, so the post-edge ones are pinned here and
	// everything else ties to the pre-pad side.
	syms []string
	locs []int // indices into locRows
}

// defineTextLabel records a .text label at the current offset, binding it
// to the alignment pad it directly follows (see relaxEvent.syms).
func (a *Assembler) defineTextLabel(label string) {
	if _, dup := a.textLabels[label]; dup && a.dupLabelErr == nil {
		a.dupLabelErr = fmt.Errorf("duplicate .text label %q: the later "+
			"definition would rebind every branch that names it, so two "+
			"bodies sharing a label prefix run into each other. Give the "+
			"second one a prefix of its own", label)
	}
	a.textLabels[label] = len(a.text)
	if e := a.trailingPad(); e != nil {
		e.syms = append(e.syms, label)
	}
}

// trailingPad returns the alignment-pad event whose post edge is the
// current end of .text — the pad an immediately following label or .loc
// row binds to — or nil.
func (a *Assembler) trailingPad() *relaxEvent {
	if n := len(a.relaxEvents); n > 0 {
		if e := &a.relaxEvents[n-1]; e.align > 0 && e.start+e.size == len(a.text) {
			return e
		}
	}
	return nil
}

// relax shrinks in-range jmp/jcc instructions to their rel8 forms (EB ib /
// 70+cc ib, matching GNU as) and rebuilds .text once, remapping labels,
// fixups, and .loc rows onto the new layout. Pad widths are recomputed as
// code shrinks so aligned labels stay aligned.
//
// Sizes are settled by a fixpoint before the single rebuild: every branch
// with a known target starts short, and any branch out of rel8 range under
// the current layout is pinned long. Growing is monotone, so the loop
// terminates, and it lands on the minimal fixpoint — the one GNU as's
// grow-only relaxation picks. (A layout can have TWO fixpoints when an
// alignment pad sits between a branch and its target: the pad re-expands
// around the long form, keeping the long layout self-consistent too, so a
// shrink-only pass would keep branches gas makes short.)
func (a *Assembler) relax() error {
	if a.relaxDone {
		return a.relaxErr
	}
	a.relaxDone = true
	if a.dupLabelErr != nil {
		a.relaxErr = a.dupLabelErr
		return a.relaxErr
	}
	a.relaxErr = a.relaxOnce()
	return a.relaxErr
}

func (a *Assembler) relaxOnce() error {
	ev := a.relaxEvents
	hasBranch := false
	for i := range ev {
		if ev[i].align == 0 {
			hasBranch = true
			break
		}
	}
	if !hasBranch {
		return nil
	}
	// prefix[i]: total size delta of events 0..i under the current plan.
	prefix := make([]int, len(ev))
	layout := func() {
		cum := 0
		for i := range ev {
			e := &ev[i]
			e.newStart = e.start + cum
			switch {
			case e.align > 0:
				e.newSize = padWidth(e.newStart, e.align, e.maxSkip)
			default:
				n := e.size
				if e.short {
					n = 2
				}
				e.bpad = 0
				if a.alignBranches {
					e.bpad = branchPad(a.alignBase+e.newStart, n)
				}
				e.newSize = e.bpad + n
			}
			cum += e.newSize - e.size
			prefix[i] = cum
		}
	}
	// mapNew translates a pre-relaxation .text offset to the planned
	// layout. Only events strictly before the offset shift it: an offset
	// AT an event's start — a label at a shrinking branch, an instruction
	// end an empty pad shares — stays on the pre-event side. (An offset
	// inside a kept-long branch is also fine: that event's delta is 0.)
	mapNew := func(off int) int {
		i := sort.Search(len(ev), func(i int) bool { return ev[i].start >= off })
		if i == 0 {
			return off
		}
		return off + prefix[i-1]
	}
	// boundSym: labels pinned to a pad's post edge (relaxEvent.syms) map
	// to the pad's new end, not through mapNew — the two differ once an
	// empty pad grows.
	boundSym := map[string]int{}
	for i := range ev {
		for _, s := range ev[i].syms {
			boundSym[s] = i
		}
	}
	labelPos := func(sym string) int {
		if i, ok := boundSym[sym]; ok {
			return ev[i].newStart + ev[i].newSize
		}
		return mapNew(a.textLabels[sym])
	}
	for i := range ev {
		e := &ev[i]
		if e.align > 0 {
			continue
		}
		if e.fixed {
			continue
		}
		// Undefined label: stays long, and the rel32 pass reports it.
		_, ok := a.textLabels[a.relFixups[e.fixup].sym]
		e.short = ok
	}
	// The sizes settle the way GNU as settles them: passes over the events
	// in offset order, each branch judged against the layout as it stands
	// mid-pass, so a target behind it sits at this pass's position and a
	// target ahead at its last position plus the growth so far (gas's
	// "stretch"), and a pad is re-measured at the offset the pass has
	// reached. That is what lets a pad absorb an earlier branch's growth
	// and keep a later branch short, as gas does. Growth is monotone, so
	// the passes are bounded by the branch count and in practice a few.
	layout()
	before := make([]int, len(ev)) // prefix under the pass before this one
	// padsBefore[i]: pads among the events before event i. Growth does not
	// reach a target on the far side of a pad within the pass, as gas has
	// it (its "regions"): the pad is taken to absorb it, and where it does
	// not, the next pass sees the settled positions and grows the branch
	// then. Shrinkage, which only a pad produces, always carries.
	padsBefore := make([]int, len(ev)+1)
	for i := range ev {
		padsBefore[i+1] = padsBefore[i]
		if ev[i].align > 0 {
			padsBefore[i+1]++
		}
	}
	// seenFrom is where sym stands as event i sees it mid-pass, with cum
	// the growth so far: a position already laid out this pass, or the
	// last pass's position moved by the stretch that reaches it.
	seenFrom := func(sym string, i, cum int) int {
		stretch := cum
		if i > 0 {
			stretch -= before[i-1]
		}
		carried := func(j int) int { // stretch as seen from a label after events 0..j-1
			if stretch < 0 || padsBefore[j] == padsBefore[i] {
				return stretch
			}
			return 0
		}
		if k, ok := boundSym[sym]; ok {
			at := ev[k].newStart + ev[k].newSize
			if k >= i {
				at += carried(k + 1)
			}
			return at
		}
		off := a.textLabels[sym]
		j := sort.Search(len(ev), func(j int) bool { return ev[j].start >= off })
		switch {
		case j == 0:
			return off
		case j-1 < i:
			return off + prefix[j-1]
		default:
			return off + before[j-1] + carried(j)
		}
	}
	converged := false
	for iter := 0; iter < len(ev)+2; iter++ {
		copy(before, prefix)
		cum := 0
		changed := false
		for i := range ev {
			e := &ev[i]
			e.newStart = e.start + cum
			switch {
			case e.align > 0:
				e.newSize = padWidth(e.newStart, e.align, e.maxSkip)
			default:
				n := e.size
				if e.short {
					n = 2
				}
				e.bpad = 0
				if a.alignBranches {
					e.bpad = branchPad(a.alignBase+e.newStart, n)
				}
				if e.short {
					sym := a.relFixups[e.fixup].sym
					if disp := seenFrom(sym, i, cum) - (e.newStart + e.bpad + 2); disp < -128 || disp > 127 {
						e.short = false
						changed = true
						n = e.size
						if a.alignBranches {
							e.bpad = branchPad(a.alignBase+e.newStart, n)
						}
					}
				}
				e.newSize = e.bpad + n
			}
			cum += e.newSize - e.size
			prefix[i] = cum
		}
		a.relaxPasses = iter + 1
		if !changed {
			converged = true
			break
		}
	}
	if !converged {
		for i := range ev {
			if ev[i].align == 0 {
				ev[i].short = false
			}
		}
		layout()
	}
	// Rebuild .text: copy the fixed byte runs, re-encode the shrunk
	// branches, re-emit the pads at their recomputed widths.
	out := make([]byte, 0, len(a.text))
	prev := 0
	for i := range ev {
		e := &ev[i]
		out = append(out, a.text[prev:e.start]...)
		if len(out) != e.newStart {
			return fmt.Errorf("internal: branch-relaxation layout drift at %#x", e.start)
		}
		if e.align == 0 {
			out = appendNopPad(out, e.bpad)
		}
		switch {
		case e.align > 0:
			out = appendNopPad(out, e.newSize)
		case e.short && a.text[e.start] == 0xE9:
			out = append(out, 0xEB, 0)
		case e.short:
			out = append(out, a.text[e.start+1]-0x80+0x70, 0) // 0F 80+cc → 70+cc
		default:
			out = append(out, a.text[e.start:e.start+e.size]...)
		}
		prev = e.start + e.size
	}
	out = append(out, a.text[prev:]...)
	// Resolve the short branches' rel8 inline; their rel32 fixups are
	// dropped below. textLabels is still pre-relaxation here, so labelPos
	// applies exactly once.
	resolved := make([]bool, len(a.relFixups))
	for i := range ev {
		e := &ev[i]
		if e.align > 0 || !e.short {
			continue
		}
		sym := a.relFixups[e.fixup].sym
		disp := labelPos(sym) - (e.newStart + e.bpad + 2)
		if disp < -128 || disp > 127 {
			return fmt.Errorf("internal: relaxed branch to %q out of rel8 range (%d)", sym, disp)
		}
		out[e.newStart+e.bpad+1] = byte(disp)
		resolved[e.fixup] = true
	}
	a.text = out
	// Remap every recorded offset onto the new layout.
	for name, off := range a.textLabels {
		if i, ok := boundSym[name]; ok {
			a.textLabels[name] = ev[i].newStart + ev[i].newSize
		} else {
			a.textLabels[name] = mapNew(off)
		}
	}
	for i := range a.locRows {
		a.locRows[i].Offset = mapNew(a.locRows[i].Offset)
	}
	for i := range ev {
		for _, li := range ev[i].locs {
			a.locRows[li].Offset = ev[i].newStart + ev[i].newSize
		}
	}
	for i := range a.ripFixups {
		a.ripFixups[i].at = mapNew(a.ripFixups[i].at)
		a.ripFixups[i].end = mapNew(a.ripFixups[i].end)
	}
	// CFI offsets are remapped for the same reason .loc rows are, and it
	// matters more: an FDE stores the DISTANCE between consecutive rules, so
	// a stale offset does not merely mislabel a line, it unwinds at the wrong
	// instruction — and the bytes stay well-formed while doing it.
	a.cfi.Remap(mapNew)
	kept := a.relFixups[:0]
	for i, f := range a.relFixups {
		if resolved[i] {
			continue
		}
		f.at = mapNew(f.at)
		kept = append(kept, f)
	}
	a.relFixups = kept
	return nil
}

// branchPad is the NOP padding that moves an n-byte branch at address start
// so that it neither crosses a 32-byte boundary nor ends on one: the
// start of the next 32-byte line, when it would. A jump, call or return
// straddling a 32-byte line, or whose last byte sits on the line's last
// byte, is the shape the Skylake-family JCC erratum's microcode fix keeps
// out of the decoded-instruction cache, and a tight loop with one such
// branch runs at half speed from the legacy decoder — the difference between
// two builds of the same kernel whose only change was where a helper landed.
func branchPad(start, n int) int {
	last := start + n - 1
	if start/32 == last/32 && last%32 != 31 {
		return 0
	}
	return 32 - start%32
}
