package arm64

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// AArch64 `b` / `bl` carry a signed 26-bit instruction offset — ±2^25
// instructions, or ±128 MB. The self-host compiler's arm64 image passed
// that span in mid-2026, so a call from one end of .text to the other
// stopped encoding and the assembler refused the program outright.
//
// The fix is what a real linker does: plant a *veneer* — a trampoline
// within branch range of the call site that materialises the target's
// full address and jumps to it indirectly — and retarget the branch at
// the veneer.
//
//	adrp x17, target
//	add  x17, x17, #:lo12:target
//	br   x17
//
// adrp+add spans ±4 GB, so one hop always suffices. x17 (IP1) is an
// AArch64 intra-procedure-call scratch register, which AAPCS reserves
// for exactly this use: it is call-clobbered, so nothing live crosses
// the `bl` (or the tail-`b`) that enters a veneer. A `bl` sets x30
// itself before transferring control, so the veneer's plain `br` leaves
// call/return semantics untouched.
//
// Veneers are grouped into *islands* — a run of veneers headed by a `b`
// that hops over them, so an island is safe to splice in anywhere,
// including mid-function where control would otherwise fall through.
// Islands are placed at the ends of .text whenever the ends can cover
// every branch, since prepending shifts all code uniformly (changing no
// relative distance) and appending shifts nothing at all; interior
// islands appear only once .text is too long for the two ends to reach.
const (
	// imm26Reach is the b/bl span in instructions: ±2^25 = ±128 MB.
	imm26Reach = 1 << 25

	// veneerMargin holds branch-to-island distances clear of the exact
	// limit, absorbing the shift that splicing islands introduces.
	veneerMargin = 1 << 16

	// maxVeneerPasses bounds the plant → re-lay-out → re-check loop. A
	// pass both shortens the branches it retargets and lengthens the
	// code, so it can strand others — and its own hop-over branches join
	// the set to be checked — hence the loop; at the real span one pass
	// is enough, and the cap turns a hypothetical oscillation into an
	// error instead of a hang.
	maxVeneerPasses = 8

	// veneerReg is x17 / IP1, the scratch register a veneer clobbers.
	veneerReg = 17

	// veneerLabelPrefix namespaces the synthetic labels naming each
	// veneer, veneerEndPrefix the ones naming an island's first
	// instruction past the end. `$` cannot appear in a label the
	// emitters produce, so these never collide with program labels —
	// and the two prefixes are disjoint, so counting one never counts
	// the other.
	veneerLabelPrefix = ".Lveneer$"
	veneerEndPrefix   = ".Lveneerend$"

	// nopInsn is `nop` (hint #0), used to pad an island to an even
	// instruction count so splicing it cannot shift a wide literal
	// pool entry off its 8-byte alignment.
	//
	//	$ printf '.text\nnop\n' | aarch64-linux-gnu-as -o u.o -
	//	$ aarch64-linux-gnu-objdump -d u.o   → d503201f  nop
	nopInsn = 0xd503201f
)

// veneerIsland is one run of veneers to splice in before instruction
// index `at`, one veneer per distinct branch target.
type veneerIsland struct {
	at       int               // original insn index to insert before
	targets  []string          // real branch targets, in island order
	labels   []string          // synthetic label naming each veneer
	byTarget map[string]string // target → its veneer label (dedupe)
	endLabel string            // names the instruction past the island
}

// size is the island's length in instructions: the hop-over `b`, three
// instructions per veneer, and a pad word when that lands odd.
func (is *veneerIsland) size() int {
	n := 1 + 3*len(is.targets)
	if n%2 != 0 {
		n++
	}
	return n
}

// reach is the b/bl span in instructions. Tests shrink it (veneerReach)
// to exercise veneering without building a 128 MB program.
func (a *Assembler) reach() int {
	if a.veneerReach > 0 {
		return a.veneerReach
	}
	return imm26Reach
}

// envVeneerReach reads FERN_ARM64_VENEER_REACH, which shrinks the branch
// span every Assembler assumes. Only programs of ~130 MB reach the real
// ±2^25-instruction limit, so without this the veneer path is exercised
// by one enormous test and nothing else; setting it to a few dozen
// instructions makes every ordinary program's calls veneered, turning
// the whole arm64 test corpus into veneer coverage. Unset in production.
func envVeneerReach() int {
	v := strings.TrimSpace(os.Getenv("FERN_ARM64_VENEER_REACH"))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// margin is the slack kept between a branch and the island it targets.
func (a *Assembler) margin() int {
	if m := a.reach() / 4; m < veneerMargin {
		return m
	}
	return veneerMargin
}

// insertVeneers rewrites the instruction stream so every b/bl reaches
// its target, planting veneer islands wherever the raw offset does not
// encode. Splicing an island shifts the code after it, which can in
// principle push another branch out of range, so it iterates to a fixed
// point. It is a no-op — not even a stream copy — when every branch
// already encodes, which is every program but the largest.
//
// Call it after FlushLiterals and before any layout that depends on
// len(insns): it changes the size of .text.
func (a *Assembler) insertVeneers() error {
	for pass := 0; ; pass++ {
		far := a.farBranches()
		if len(far) == 0 {
			a.veneerPasses = pass
			return nil
		}
		if pass >= maxVeneerPasses {
			return fmt.Errorf("arm64: branch veneering did not converge after %d passes (%d branches still out of range)", maxVeneerPasses, len(far))
		}
		if err := a.plantVeneers(far); err != nil {
			return err
		}
	}
}

// farBranches returns the indices (into a.fixups) of the b/bl fixups
// whose target lies outside the imm26 span. Branches to undefined
// labels are left alone, so the resolver still reports them by name.
func (a *Assembler) farBranches() []int {
	var out []int
	lim := a.reach()
	for i, f := range a.fixups {
		if f.kind != branchImm26 {
			continue
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

// veneerAnchors returns the instruction indices where an island may be
// spliced in, ascending. Both ends of .text are always candidates and
// are the safest: index 0 shifts every instruction by the same amount
// (so no branch, literal-pool, or adrp distance within the program
// changes at all) and the end shifts nothing. Interior anchors are
// added only when .text is longer than the two ends can cover between
// them, and are nudged clear of any wide literal they would split.
func (a *Assembler) veneerAnchors() []int {
	n := len(a.insns)
	stride := a.reach() - a.margin()
	anchors := []int{0}
	if n > 2*stride {
		for p := stride; p < n; p += stride {
			anchors = append(anchors, a.safeAnchor(p))
		}
	}
	return append(anchors, n)
}

// safeAnchor moves an interior anchor forward off the second word of a
// wide literal-pool entry: splicing there would separate the halves of
// an 8-byte literal from each other.
func (a *Assembler) safeAnchor(p int) int {
	for moved := true; moved; {
		moved = false
		for _, f := range a.litFixups {
			if f.wide && f.poolIdx+1 == p {
				p++
				moved = true
			}
		}
	}
	return p
}

// pickAnchor chooses the island nearest the branch at index `at` that
// the branch can still reach once the island is in place. Anchors are
// spaced a stride apart and a stride is the reach, so a site always
// exists; the error is an invariant check on veneerAnchors, not a case
// a program can reach.
func (a *Assembler) pickAnchor(anchors []int, at int, label string) (int, error) {
	lim := a.reach() - a.margin()
	best, bestD := -1, 0
	// Anchors are ascending, so only the two straddling `at` can be
	// nearest. Scanning them all instead is quadratic in the branch
	// count once a shortened span makes anchors dense.
	i := sort.SearchInts(anchors, at)
	for _, j := range [2]int{i - 1, i} {
		if j < 0 || j >= len(anchors) {
			continue
		}
		d := anchors[j] - at
		if d < 0 {
			d = -d
		}
		if d > lim {
			continue
		}
		if best < 0 || d < bestD {
			best, bestD = anchors[j], d
		}
	}
	if best < 0 {
		return 0, fmt.Errorf("arm64: no veneer site within branch range of the call to %q at instruction %d", label, at)
	}
	return best, nil
}

// plantVeneers builds the islands the given branches need, retargets
// those branches at their veneers, and splices the islands into the
// instruction stream. Nothing is mutated until every anchor has been
// chosen, so a failure leaves the assembler untouched.
func (a *Assembler) plantVeneers(far []int) error {
	anchors := a.veneerAnchors()
	byAnchor := map[int]*veneerIsland{}
	var islands []*veneerIsland

	// Which veneer each far branch ends up pointing at, parallel to far.
	retarget := make([]string, len(far))
	for i, fi := range far {
		f := a.fixups[fi]
		at, err := a.pickAnchor(anchors, f.at, f.label)
		if err != nil {
			return err
		}
		is := byAnchor[at]
		if is == nil {
			a.veneerSeq++
			is = &veneerIsland{at: at, byTarget: map[string]string{}, endLabel: fmt.Sprintf("%s%d", veneerEndPrefix, a.veneerSeq)}
			byAnchor[at] = is
			islands = append(islands, is)
		}
		lbl, ok := is.byTarget[f.label]
		if !ok {
			a.veneerSeq++
			lbl = fmt.Sprintf("%s%d$%s", veneerLabelPrefix, a.veneerSeq, f.label)
			is.byTarget[f.label] = lbl
			is.targets = append(is.targets, f.label)
			is.labels = append(is.labels, lbl)
		}
		retarget[i] = lbl
	}
	sort.Slice(islands, func(i, j int) bool { return islands[i].at < islands[j].at })

	for i, fi := range far {
		a.fixups[fi].label = retarget[i]
	}
	a.spliceIslands(islands)
	return nil
}

// spliceIslands rewrites insns with the islands inserted, shifting
// every recorded instruction index — labels, symbols, branch, adrp,
// :lo12:, literal-pool, and DWARF line rows — to match, then records
// the new veneers' own labels and fixups.
func (a *Assembler) spliceIslands(islands []*veneerIsland) {
	ats := make([]int, len(islands))
	cum := make([]int, len(islands))
	total := 0
	for i, is := range islands {
		total += is.size()
		ats[i], cum[i] = is.at, total
	}
	// delta is how far an original index moves: the combined size of the
	// islands spliced in at or before it. A label at an island's anchor
	// lands after the island, which is what both the hop-over `b` and
	// any branch to that label expect.
	delta := func(old int) int {
		if i := sort.SearchInts(ats, old+1); i > 0 {
			return cum[i-1]
		}
		return 0
	}

	for name, v := range a.labels {
		a.labels[name] = v + delta(v)
	}
	for name, s := range a.syms {
		if s.inText {
			s.val += delta(s.val)
			a.syms[name] = s
		}
	}
	for i := range a.fixups {
		a.fixups[i].at += delta(a.fixups[i].at)
	}
	for i := range a.adrpFixups {
		a.adrpFixups[i].at += delta(a.adrpFixups[i].at)
	}
	for i := range a.lo12Fixups {
		a.lo12Fixups[i].at += delta(a.lo12Fixups[i].at)
	}
	for i := range a.litFixups {
		a.litFixups[i].at += delta(a.litFixups[i].at)
		a.litFixups[i].poolIdx += delta(a.litFixups[i].poolIdx)
	}
	for i := range a.pendingLits {
		a.pendingLits[i].at += delta(a.pendingLits[i].at)
	}
	for i := range a.locRows {
		old := a.locRows[i].Offset / 4
		a.locRows[i].Offset = (old + delta(old)) * 4
	}

	out := make([]uint32, 0, len(a.insns)+total)
	next := 0
	for i, insn := range a.insns {
		for next < len(islands) && islands[next].at == i {
			out = a.appendIsland(out, islands[next])
			next++
		}
		out = append(out, insn)
	}
	for ; next < len(islands); next++ {
		out = a.appendIsland(out, islands[next])
	}
	a.insns = out

	// adrp/:lo12: resolve through syms, not labels, so a veneer to a
	// purely local label (one defined with Label rather than TextLabel)
	// needs a symbol of its own.
	for _, is := range islands {
		for _, t := range is.targets {
			if _, ok := a.syms[t]; !ok {
				a.syms[t] = symbol{inText: true, val: a.labels[t]}
			}
		}
	}
}

// appendIsland writes one island to the new instruction stream: a `b`
// hopping over it, then adrp/add/br per veneer. Indices are absolute in
// the stream being built, so the labels and fixups recorded here need
// no further shifting.
//
// The hop-over is a labelled fixup, not a hand-encoded offset. A later
// pass computes its anchors over the stream this one produced, so it can
// splice an island inside this one — and a raw offset is the one thing
// the index remap cannot correct, leaving the hop landing mid-veneer.
func (a *Assembler) appendIsland(out []uint32, is *veneerIsland) []uint32 {
	end := len(out) + is.size()
	a.labels[is.endLabel] = end
	a.fixups = append(a.fixups, fixup{at: len(out), label: is.endLabel, kind: branchImm26})
	out = append(out, 0x14000000)
	for i, t := range is.targets {
		a.labels[is.labels[i]] = len(out)
		a.adrpFixups = append(a.adrpFixups, symFixup{at: len(out), label: t, rd: veneerReg})
		out = append(out, ADRP(veneerReg, 0))
		a.lo12Fixups = append(a.lo12Fixups, symFixup{at: len(out), label: t, rd: veneerReg, rn: veneerReg})
		out = append(out, ADDimm(veneerReg, veneerReg, 0, false))
		out = append(out, BR(veneerReg))
	}
	for len(out) < end {
		out = append(out, nopInsn)
	}
	return out
}
