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
			case e.short:
				e.newSize = 2
			default:
				e.newSize = e.size
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
		// Undefined label: stays long, and the rel32 pass reports it.
		_, ok := a.textLabels[a.relFixups[e.fixup].sym]
		e.short = ok
	}
	// Without text pads every event only ever grows, so two points can only
	// move apart: a branch out of rel8 range stays out however many others
	// are pinned, and a whole pass's verdicts can be applied in one batch.
	// A pad breaks that monotonicity — it can absorb an earlier branch's
	// growth and pull a later branch back INTO range (which gas's
	// incremental growth honors) — so with pads present each pass pins only
	// the first out-of-range branch, then re-lays out. Compiler-emitted
	// .text has no pads, so the O(passes×n) precise path is confined to
	// hand-written assembly.
	hasPad := false
	for i := range ev {
		if ev[i].align > 0 {
			hasPad = true
			break
		}
	}
	converged := false
	for iter := 0; iter < len(ev)+2; iter++ {
		layout()
		changed := false
		for i := range ev {
			e := &ev[i]
			if e.align > 0 || !e.short {
				continue
			}
			sym := a.relFixups[e.fixup].sym
			if disp := labelPos(sym) - (e.newStart + 2); disp < -128 || disp > 127 {
				e.short = false
				changed = true
				if hasPad {
					break
				}
			}
		}
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
		disp := labelPos(sym) - (e.newStart + 2)
		if disp < -128 || disp > 127 {
			return fmt.Errorf("internal: relaxed branch to %q out of rel8 range (%d)", sym, disp)
		}
		out[e.newStart+1] = byte(disp)
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
