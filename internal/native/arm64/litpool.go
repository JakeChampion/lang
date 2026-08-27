package arm64

import (
	"fmt"
	"sort"
)

// `ldr Xt, =value` reaches its literal with the same signed 19-bit offset the
// conditional branches use — ±1 MB — and literals are flushed once, at the end
// of .text. A self-host module reached 26 MB, so a load at the front was 26 MB
// from its pool and the program was refused.
//
// Flushing per function does not fix it either: one self-host function is
// 2.97 MB of code on its own, three times the reach.
//
// So the pool goes where the loads are. A far load gets its value re-homed into
// an island spliced in within reach, headed by a `b` that hops over it — the
// same placement veneers use against a different limit, so it shares their
// machinery. The original entry stays where it was; a literal is 8 bytes and
// duplicating one is cheaper than the bookkeeping to move it.

const (
	// litReach is the ldr-literal span in instructions: ±2^18, or ±1 MB.
	litReach = 1 << 18

	// litLabelPrefix names the label marking the instruction past a pool.
	litLabelPrefix = ".Llitpool$"
)

// litPoolReach is the span, shrunk in tests so a pool test need not emit a
// megabyte of instructions.
func (a *Assembler) litPoolReach() int {
	if a.relaxReach > 0 && a.relaxReach < litReach {
		return a.relaxReach
	}
	return litReach
}

// farLiterals returns the indices (into a.litFixups) of the loads whose pool
// entry lies outside the ldr-literal span.
func (a *Assembler) farLiterals() []int {
	lim := a.litPoolReach()
	var out []int
	for i, f := range a.litFixups {
		if off := f.poolIdx - f.at; off < -lim || off >= lim {
			out = append(out, i)
		}
	}
	return out
}

// litIsland is a pool of literal words to splice in near the loads that need
// them. The wide entries come first so one pad word after the hop-over `b` is
// enough to keep them 8-byte aligned: the island's own start index is even (see
// plantLitPools) and its size is rounded to even, so the run beginning two
// words in is 8-byte aligned wherever it lands.
type litIsland struct {
	at       int
	vals     []litRef
	uses     [][]int // for each val, the litFixups indices that will point at it
	byVal    map[litKey]int
	endLabel string
}

type litKey struct {
	val  uint64
	wide bool
}

func (is *litIsland) anchor() int { return is.at }

func (is *litIsland) size() int {
	n := 2 // the hop-over b, then the alignment pad
	for _, v := range is.vals {
		if v.wide {
			n += 2
		} else {
			n++
		}
	}
	if n%2 != 0 {
		n++
	}
	return n
}

func (is *litIsland) appendTo(a *Assembler, out []uint32) []uint32 {
	end := len(out) + is.size()
	a.labels[is.endLabel] = end
	a.fixups = append(a.fixups, fixup{at: len(out), label: is.endLabel, kind: branchImm26})
	out = append(out, 0x14000000)
	out = append(out, 0) // pad: the wide entries below start 8-byte aligned

	place := func(wide bool) {
		for i, v := range is.vals {
			if v.wide != wide {
				continue
			}
			for _, fi := range is.uses[i] {
				a.litFixups[fi].poolIdx = len(out)
			}
			out = append(out, uint32(v.val))
			if v.wide {
				out = append(out, uint32(v.val>>32))
			}
		}
	}
	place(true)
	place(false)

	for len(out) < end {
		out = append(out, nopInsn)
	}
	return out
}

// plantLitPools re-homes each listed load's literal into an island within
// reach of it.
func (a *Assembler) plantLitPools(far []int) error {
	lim := a.litPoolReach()
	// An island must start at an even index for its wide entries to land
	// 8-byte aligned, and every island's size is even, so an even anchor stays
	// even however many islands precede it.
	anchors := a.anchorsWithin(lim - a.litMargin())
	for i, p := range anchors {
		if p%2 != 0 {
			anchors[i] = p + 1
		}
	}

	byAnchor := map[int]*litIsland{}
	var islands []*litIsland
	for _, fi := range far {
		f := a.litFixups[fi]
		at, err := a.pickAnchorWithin(anchors, f.at, lim-a.litMargin(), "the literal load")
		if err != nil {
			return err
		}
		is := byAnchor[at]
		if is == nil {
			a.veneerSeq++
			is = &litIsland{at: at, byVal: map[litKey]int{}, endLabel: fmt.Sprintf("%s%d", litLabelPrefix, a.veneerSeq)}
			byAnchor[at] = is
			islands = append(islands, is)
		}
		k := litKey{val: a.litValue(fi), wide: f.wide}
		j, ok := is.byVal[k]
		if !ok {
			j = len(is.vals)
			is.byVal[k] = j
			is.vals = append(is.vals, litRef{val: k.val, wide: k.wide})
			is.uses = append(is.uses, nil)
		}
		is.uses[j] = append(is.uses[j], fi)
	}
	sort.Slice(islands, func(i, j int) bool { return islands[i].at < islands[j].at })

	runs := make([]spliceRun, len(islands))
	for i, is := range islands {
		runs[i] = is
	}
	a.splice(runs)
	return nil
}

// litMargin holds pool distances clear of the exact limit, absorbing the shift
// splicing introduces.
func (a *Assembler) litMargin() int {
	if m := a.litPoolReach() / 4; m < veneerMargin {
		return m
	}
	return veneerMargin
}

// litValue reads back the value a placed literal holds. FlushLiterals wrote it
// into the instruction stream and kept no copy, so the words are the record.
func (a *Assembler) litValue(fi int) uint64 {
	f := a.litFixups[fi]
	v := uint64(a.insns[f.poolIdx])
	if f.wide {
		v |= uint64(a.insns[f.poolIdx+1]) << 32
	}
	return v
}
