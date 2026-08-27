package arm64

import (
	"fmt"
	"sort"
)

// The conditional branches carry a much shorter reach than `b`/`bl`:
// b.cond / cbz / cbnz encode a signed 19-bit instruction offset (±1 MB)
// and tbz / tbnz a signed 14-bit one (±32 KB). A single function in the
// self-host compiler outgrew the 19-bit span in mid-2026, so a branch
// between two of its own blocks stopped encoding and the assembler
// refused the program.
//
// A veneer cannot help here: the conditional itself is what will not
// reach, so the fix has to shorten the conditional rather than shorten
// what it points at. Invert it and hop over an unconditional branch,
// which reaches 128 MB on its own and can take a veneer beyond that:
//
//	b.eq far        ->      b.ne .Lskip
//	                        b far
//	                    .Lskip:
//
// Cost is one instruction per relaxed branch, and only at the branches
// that actually need it.

// relaxLabelPrefix names the synthetic label a relaxed branch skips to.
// `$` cannot appear in a label the assembler's own sources produce, so
// these cannot collide with a program's labels.
const relaxLabelPrefix = ".Lrelax$"

// shortBranchReach is a conditional branch kind's reach in
// instructions. imm26 is absent: that is the veneer pass's job.
func shortBranchReach(kind branchKind) int {
	switch kind {
	case branchImm19:
		return 1 << 18
	case branchImm14:
		return 1 << 13
	}
	return 0
}

// invertBranch returns the branch that transfers control exactly when
// insn does not, and whether insn is a form this understands. The
// conditional branches all invert by flipping one bit: b.cond's
// condition field has its complement in bit 0, and cbz/cbnz and
// tbz/tbnz are each other with bit 24 flipped.
func invertBranch(insn uint32) (uint32, bool) {
	switch {
	case insn&0xff000010 == 0x54000000: // b.cond
		return insn ^ 1, true
	case insn&0x7e000000 == 0x34000000: // cbz / cbnz, both widths
		return insn ^ (1 << 24), true
	case insn&0x7e000000 == 0x36000000: // tbz / tbnz, both widths
		return insn ^ (1 << 24), true
	}
	return 0, false
}

// relaxRun is the unconditional branch spliced in after a relaxed
// conditional — the far jump the conditional now hops over.
type relaxRun struct {
	at    int    // insn index to insert before: the conditional's index + 1
	label string // the original, out-of-range target
}

func (r *relaxRun) anchor() int { return r.at }

func (r *relaxRun) size() int { return 1 }

func (r *relaxRun) appendTo(a *Assembler, out []uint32) []uint32 {
	a.fixups = append(a.fixups, fixup{at: len(out), label: r.label, kind: branchImm26})
	return append(out, 0x14000000)
}

// shortBranches returns the indices (into a.fixups) of the conditional
// branches whose target lies outside their own immediate's reach.
// Branches to undefined labels are left alone, so the resolver still
// reports them by name.
func (a *Assembler) shortBranches() []int {
	var out []int
	for i, f := range a.fixups {
		lim := shortBranchReach(f.kind)
		if lim == 0 {
			continue
		}
		if a.relaxReach > 0 && a.relaxReach < lim {
			lim = a.relaxReach // tests shrink the span rather than emit a megabyte
		}
		t, ok := a.labels[f.label]
		if !ok {
			continue
		}
		if off := t - f.at; off < -lim || off >= lim {
			out = append(out, i)
		}
	}
	return out
}

// relaxShortBranches inverts each listed conditional branch and splices
// an unconditional `b` to its original target immediately after it, so
// the conditional only has to reach two instructions ahead.
func (a *Assembler) relaxShortBranches(short []int) error {
	runs := make([]spliceRun, 0, len(short))
	for _, fi := range short {
		f := a.fixups[fi]
		inv, ok := invertBranch(a.insns[f.at])
		if !ok {
			return fmt.Errorf("arm64: branch to %q at instruction %d is out of range and has no invertible form (0x%08x)", f.label, f.at, a.insns[f.at])
		}
		a.veneerSeq++
		skip := fmt.Sprintf("%s%d", relaxLabelPrefix, a.veneerSeq)
		// The skip label sits at the conditional's index + 1, which the
		// splice moves to just past the inserted `b` — exactly where
		// control must land when the condition does not hold.
		a.labels[skip] = f.at + 1
		a.insns[f.at] = inv
		a.fixups[fi].label = skip
		runs = append(runs, &relaxRun{at: f.at + 1, label: f.label})
	}
	// splice needs its runs in ascending anchor order, and a.fixups is
	// not sorted by index once a veneer pass has appended its own.
	sort.Slice(runs, func(i, j int) bool { return runs[i].anchor() < runs[j].anchor() })
	a.splice(runs)
	return nil
}
