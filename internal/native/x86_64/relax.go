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
	// bpad is the padding laid at the event's start under branch
	// alignment, so that the branch's bytes neither cross nor end on a
	// 32-byte boundary (see Assembler.SetBranchAlignment). Filled in by
	// relax, and spent as prefixes before NOPs (see bpfx).
	bpad int
	// lead is the byte count at the event's start that belongs to the
	// instruction BEFORE the branch, which the event swallows so the
	// padding can land in front of it rather than between the two. Zero
	// when there is no such instruction to take.
	//
	// The erratum counts an instruction's prefixes as part of it, so a 2E
	// laid in front of the BRANCH lengthens the very instruction that must
	// not straddle the line and buys nothing. Lengthening the instruction
	// before it pushes the branch forward whole, which is why the padding
	// has to reach back over it.
	lead int
	// bpfx is how many of bpad's bytes are redundant 2E prefixes on the
	// lead instruction rather than NOPs. They decode as part of that
	// instruction, so unlike a NOP they cost no executed instruction —
	// which is the whole point (#10017). The rest of bpad stays NOPs.
	bpfx int
	// leadPrefixable says the lead instruction may carry redundant 2E
	// prefixes. A VEX-encoded instruction may not: a legacy segment prefix
	// ahead of C4/C5 is #UD, not a longer encoding of the same thing. Such
	// a lead is still taken for the fused-span measurement; only the
	// prefixes are withheld, and its padding stays NOPs.
	leadPrefixable bool
	// leadFuse marks a lead that macro-fuses with this branch (a flag-
	// setting ALU op before a jcc). The pair is what the erratum tests, so
	// the span kept off the line covers both.
	leadFuse bool
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
				bn := e.size - e.lead
				if e.short {
					bn = 2
				}
				e.bpad, e.bpfx = a.padFor(e, bn)
				e.newSize = e.bpad + e.lead + bn
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
	// An offset within a lead-extended event needs the padding laid before
	// the lead, and NOT that event's other delta: a branch that shrinks to
	// its rel8 form gets shorter without its start moving, so a rule or
	// label sitting AT that branch must not follow the shrink. Before the
	// lead was taken into the event, this fell out of "an offset at an
	// event's start stays on the pre-event side"; now it has to be said.
	// Every view of a position has to agree on this, or the fixpoint judges
	// a branch in rel8 range against an offset the rebuild then puts
	// somewhere else — which is how it settled a 127-byte displacement that
	// came out at 128.
	leadShift := func(pref []int, j, off int) (int, bool) {
		e := &ev[j-1]
		if e.align > 0 || e.lead == 0 || off > e.start+e.lead {
			return 0, false
		}
		base := 0
		if j >= 2 {
			base = pref[j-2]
		}
		return off + base + e.bpad, true
	}
	mapNew := func(off int) int {
		i := sort.Search(len(ev), func(i int) bool { return ev[i].start >= off })
		if i == 0 {
			return off
		}
		if at, ok := leadShift(prefix, i, off); ok {
			return at
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
			if at, ok := leadShift(prefix, j, off); ok {
				return at
			}
			return off + prefix[j-1]
		default:
			if at, ok := leadShift(before, j, off); ok {
				return at + carried(j)
			}
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
				bn := e.size - e.lead
				if e.short {
					bn = 2
				}
				e.bpad, e.bpfx = a.padFor(e, bn)
				if e.short {
					sym := a.relFixups[e.fixup].sym
					if disp := seenFrom(sym, i, cum) - (e.newStart + e.bpad + e.lead + 2); disp < -128 || disp > 127 {
						e.short = false
						changed = true
						bn = e.size - e.lead
						e.bpad, e.bpfx = a.padFor(e, bn)
					}
				}
				e.newSize = e.bpad + e.lead + bn
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
			out = appendNopPad(out, e.bpad-e.bpfx)
			for p := 0; p < e.bpfx; p++ {
				out = append(out, 0x2E)
			}
			out = append(out, a.text[e.start:e.start+e.lead]...)
		}
		bs := e.start + e.lead // the branch's own bytes
		switch {
		case e.align > 0:
			out = appendNopPad(out, e.newSize)
		case e.short && a.text[bs] == 0xE9:
			out = append(out, 0xEB, 0)
		case e.short:
			out = append(out, a.text[bs+1]-0x80+0x70, 0) // 0F 80+cc → 70+cc
		default:
			out = append(out, a.text[bs:e.start+e.size]...)
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
		disp := labelPos(sym) - (e.newStart + e.bpad + e.lead + 2)
		if disp < -128 || disp > 127 {
			return fmt.Errorf("internal: relaxed branch to %q out of rel8 range (%d)", sym, disp)
		}
		out[e.newStart+e.bpad+e.lead+1] = byte(disp)
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

// padFor sizes the padding an aligned branch event needs at its planned
// start, and how much of it the lead instruction absorbs as prefixes. The
// span kept off the 32-byte line is the BRANCH alone, or the lead and the
// branch together when they macro-fuse: the erratum tests the fused pair as
// one, and padding to put the jcc alone at the line start leaves that pair
// straddling it exactly.
func (a *Assembler) padFor(e *relaxEvent, bn int) (pad, pfx int) {
	if !a.alignBranches {
		return 0, 0
	}
	if e.leadFuse && e.lead+bn <= 32 {
		// Only when the pair can fit on one line at all: branchPad moves a
		// span to a line START, which keeps it whole only if it is no wider
		// than the line. A longer pair straddles wherever it is put, so
		// protect the branch alone and spend the padding there.
		pad = branchPad(a.alignBase+e.newStart, e.lead+bn)
	} else {
		pad = branchPad(a.alignBase+e.newStart+e.lead, bn)
	}
	return pad, prefixSplit(pad, e.lead, e.leadPrefixable)
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

// maxSegPrefixes is how many redundant 2E bytes one instruction may carry
// here. GNU as's -mbranches-within-32B-boundaries caps it at 5 per
// instruction, and the hard ceiling is the decoder's 15-byte instruction
// limit — past either, the rest of the padding stays NOPs.
const maxSegPrefixes = 5

// maxInstBytes is the decoder's limit. An instruction longer than this
// faults, so the prefixes may never carry the lead past it.
const maxInstBytes = 15

// prefixSplit divides `pad` bytes of branch padding into the redundant 2E
// prefixes the lead instruction can absorb and the NOPs that must make up
// the rest. A prefix rides along inside an instruction the CPU already
// decodes; a NOP is one more instruction retired on every pass through the
// loop the branch sits in, which on `cat -A` over 3 MB was 6.7% of all
// instructions retired (#10017).
func prefixSplit(pad, lead int, prefixable bool) int {
	if pad <= 0 || lead <= 0 || !prefixable {
		return 0
	}
	room := maxInstBytes - lead
	if room > maxSegPrefixes {
		room = maxSegPrefixes
	}
	if room < 0 {
		room = 0
	}
	if pad < room {
		return pad
	}
	return room
}
