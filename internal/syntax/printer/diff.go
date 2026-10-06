// Unified-diff helper for the formatter's `-d` mode.
//
// Computes a line-level diff between the file as-written and its
// formatted form, emitting a familiar `--- a/file +++ b/file @@`
// unified-diff string. The alignment is Myers' O(ND) diff in linear
// space, so the cost tracks the size of the change rather than the size
// of the file; no external diff binary is required, so the CLI stays
// self-contained on Windows / WSL / wherever it runs.
//
// The diff intentionally lives in the printer package rather than
// alongside Format itself so a future tooling consumer (an LSP, a
// pre-commit hook) can pull it in without dragging the CLI in too.

package printer

import (
	"fmt"
	"strings"
)

// UnifiedDiff returns a unified-diff text of `before` against
// `after`, both expected to be \n-separated source listings.
// `pathBefore` and `pathAfter` populate the `--- ` / `+++ ` header
// rows. The result is empty when the two inputs are byte-identical.
//
// The hunk format is the conventional `@@ -a,b +c,d @@` with the
// surrounding context lines included. Tools that read unified diffs
// (`patch`, GitHub PR review, IDE diff viewers) accept the output
// without complaint.
func UnifiedDiff(before, after, pathBefore, pathAfter string) string {
	if before == after {
		return ""
	}
	a := splitLines(before)
	b := splitLines(after)
	hunks := buildHunks(a, b, 3)
	if len(hunks) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", pathBefore)
	fmt.Fprintf(&sb, "+++ %s\n", pathAfter)
	for _, h := range hunks {
		sb.WriteString(formatHunk(h))
	}
	return sb.String()
}

// splitLines returns s broken on '\n' boundaries with the trailing
// newline retained on each line (so " " lines reproduce verbatim
// in the diff output). A trailing-empty entry from a final '\n'
// is dropped — the diff doesn't need a phantom empty line at
// the end.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, strings.Count(s, "\n")+1)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		// Trailing line with no \n — keep it so the diff faithfully
		// shows "no newline at end of file".
		out = append(out, s[start:])
	}
	return out
}

// op describes one entry in the edit script.
type op struct {
	kind byte // ' ' equal, '-' delete from a, '+' insert from b
	line string
}

// anchor is one matched line pair in the alignment, in filtered
// coordinates.
type anchor struct{ a, b int }

// maxSearchCost caps how far the middle-snake search looks for its two
// halves to meet before giving up on minimality and splitting at the
// furthest-reaching diagonal instead. The halves meet after about D/2
// rounds each, so edit distances up to twice the cap come out minimal;
// past that the script is still correct, just not guaranteed shortest.
// No source in this repository reaches it, reformatted from scratch or
// not — it bounds the machine-generated input that would.
const maxSearchCost = 1 << 14

// editScript diffs a against b and returns the merge of the two as an
// ordered edit script. Each op carries the line text (already with its
// trailing newline if any) so the hunk emitter just needs to glue
// prefixes and write.
//
// The alignment is Myers' O(ND) greedy diff in its linear-space
// divide-and-conquer form. Memory is O(m+n); the LCS table this replaced
// was O(m*n), which is 48 GB for the largest source in this repository.
func editScript(a, b []string, maxCost int) []op {
	m, n := len(a), len(b)
	ia, ib, distinct := internLines(a, b)

	// Shared ends belong to every optimal alignment, so peel them off
	// before searching. The usual case — a handful of reformatted lines in
	// an otherwise untouched file — then costs about what the change costs.
	pre := 0
	for pre < m && pre < n && ia[pre] == ib[pre] {
		pre++
	}
	suf := 0
	for suf < m-pre && suf < n-pre && ia[m-1-suf] == ib[n-1-suf] {
		suf++
	}

	// A line occurring on only one side cannot be part of any common
	// subsequence, so it is an edit in every alignment and the search never
	// needs to see it. On a wholly reformatted source that is most of the
	// file, and the search cost scales with the edit distance.
	inA := make([]bool, distinct)
	inB := make([]bool, distinct)
	for _, id := range ia[pre : m-suf] {
		inA[id] = true
	}
	for _, id := range ib[pre : n-suf] {
		inB[id] = true
	}
	d := &differ{maxCost: maxCost}
	for i := pre; i < m-suf; i++ {
		if inB[ia[i]] {
			d.ia = append(d.ia, ia[i])
			d.mapA = append(d.mapA, i)
		}
	}
	for j := pre; j < n-suf; j++ {
		if inA[ib[j]] {
			d.ib = append(d.ib, ib[j])
			d.mapB = append(d.mapB, j)
		}
	}
	d.compare(0, len(d.ia), 0, len(d.ib))

	// Everything between two matched lines is an edit; deletions come
	// before insertions within each such run.
	out := make([]op, 0, m+n)
	i, j := 0, 0
	fill := func(ai, bj int) {
		for ; i < ai; i++ {
			out = append(out, op{'-', a[i]})
		}
		for ; j < bj; j++ {
			out = append(out, op{'+', b[j]})
		}
	}
	for ; i < pre; i++ {
		out = append(out, op{' ', a[i]})
	}
	j = pre
	for _, an := range d.anchors {
		fill(d.mapA[an.a], d.mapB[an.b])
		out = append(out, op{' ', a[i]})
		i++
		j++
	}
	fill(m-suf, n-suf)
	for ; i < m; i++ {
		out = append(out, op{' ', a[i]})
	}
	return out
}

// internLines maps every distinct line to a small integer so the snake
// loops compare ints instead of strings, and reports how many there are.
func internLines(a, b []string) ([]int32, []int32, int) {
	ids := make(map[string]int32, len(a)+len(b))
	intern := func(lines []string) []int32 {
		out := make([]int32, len(lines))
		for i, s := range lines {
			id, ok := ids[s]
			if !ok {
				id = int32(len(ids))
				ids[s] = id
			}
			out[i] = id
		}
		return out
	}
	ra, rb := intern(a), intern(b)
	return ra, rb, len(ids)
}

// differ holds the two filtered line sequences, the map back to the
// original line numbers, and the alignment being built. vf and vr are the
// forward and reverse furthest-reaching arrays, allocated once and reused
// by every bisect.
type differ struct {
	ia, ib     []int32
	mapA, mapB []int
	anchors    []anchor
	vf, vr     []int32
	maxCost    int
}

// compare appends the alignment of ia[alo:ahi] against ib[blo:bhi].
func (d *differ) compare(alo, ahi, blo, bhi int) {
	// Peeling the shared ends off every subproblem is not just a shortcut:
	// it is what guarantees the bisect below splits strictly inside the
	// range, so the recursion terminates.
	for alo < ahi && blo < bhi && d.ia[alo] == d.ib[blo] {
		d.anchors = append(d.anchors, anchor{alo, blo})
		alo++
		blo++
	}
	suffix := 0
	for ahi-suffix > alo && bhi-suffix > blo && d.ia[ahi-1-suffix] == d.ib[bhi-1-suffix] {
		suffix++
	}
	ahi -= suffix
	bhi -= suffix

	if alo < ahi && blo < bhi {
		x, y := d.bisect(alo, ahi, blo, bhi)
		// A split that makes no progress would not terminate. The end
		// peeling above rules that out; the guard keeps a bug in the bisect
		// from becoming a hang.
		if (x != alo || y != blo) && (x != ahi || y != bhi) {
			d.compare(alo, x, blo, y)
			d.compare(x, ahi, y, bhi)
		}
	}

	for k := 0; k < suffix; k++ {
		d.anchors = append(d.anchors, anchor{ahi + k, bhi + k})
	}
}

// bisect finds a point (x, y) on a shortest edit path between
// ia[alo:ahi] and ib[blo:bhi] by growing the forward search from the top
// left and the reverse search from the bottom right until they meet on a
// common diagonal. Past the cost cap it stops waiting for them to meet
// and returns the furthest-reaching point either search has found.
func (d *differ) bisect(alo, ahi, blo, bhi int) (int, int) {
	nA, nB := ahi-alo, bhi-blo
	// The searches meet after at most ceil(D/2) rounds each, and D is at
	// most nA+nB; the extra round leaves room for the odd-delta case.
	maxD := (nA + nB + 3) / 2
	off := maxD + 1
	size := 2*maxD + 4
	if cap(d.vf) < size {
		d.vf = make([]int32, size)
		d.vr = make([]int32, size)
	}
	vf, vr := d.vf[:size], d.vr[:size]
	for i := range vf {
		vf[i] = -1
		vr[i] = -1
	}
	vf[off+1] = 0
	vr[off+1] = 0
	delta := nA - nB
	// The two searches can only meet on a forward step when delta is odd,
	// and only on a reverse step when it is even.
	meetForward := delta%2 != 0
	// Diagonals that have run off the grid stop being extended.
	fLo, fHi, rLo, rHi := 0, 0, 0, 0

	budget := maxD
	if budget > d.maxCost {
		budget = d.maxCost
	}
	for k := 0; k < budget; k++ {
		for j := -k + fLo; j <= k-fHi; j += 2 {
			o := off + j
			var x int32
			if j == -k || (j != k && vf[o-1] < vf[o+1]) {
				x = vf[o+1]
			} else {
				x = vf[o-1] + 1
			}
			y := x - int32(j)
			for x < int32(nA) && y < int32(nB) && d.ia[alo+int(x)] == d.ib[blo+int(y)] {
				x++
				y++
			}
			vf[o] = x
			switch {
			case int(x) > nA:
				fHi += 2
			case int(y) > nB:
				fLo += 2
			case meetForward:
				if ro := off + delta - j; ro >= 0 && ro < size && vr[ro] >= 0 {
					if int(x) >= nA-int(vr[ro]) {
						return alo + int(x), blo + int(y)
					}
				}
			}
		}
		for j := -k + rLo; j <= k-rHi; j += 2 {
			o := off + j
			var x int32
			if j == -k || (j != k && vr[o-1] < vr[o+1]) {
				x = vr[o+1]
			} else {
				x = vr[o-1] + 1
			}
			y := x - int32(j)
			for x < int32(nA) && y < int32(nB) && d.ia[ahi-1-int(x)] == d.ib[bhi-1-int(y)] {
				x++
				y++
			}
			vr[o] = x
			switch {
			case int(x) > nA:
				rHi += 2
			case int(y) > nB:
				rLo += 2
			case !meetForward:
				if fo := off + delta - j; fo >= 0 && fo < size && vf[fo] >= 0 {
					fx := int(vf[fo])
					if fx >= nA-int(x) {
						return alo + fx, blo + fx - (delta - j)
					}
				}
			}
		}
	}
	return d.furthest(vf, vr, off, size, alo, ahi, blo, bhi)
}

// furthest picks the split point for a subproblem whose edit distance
// exceeded the cost cap. The forward search's best point is the one that
// consumed most of both inputs from the front and the reverse search's is
// the one that consumed most from the back, so the two are scored on their
// own progress and the deeper one wins. Progress is at least the budget
// either way, so the recursion terminates.
func (d *differ) furthest(vf, vr []int32, off, size, alo, ahi, blo, bhi int) (int, int) {
	nA, nB := ahi-alo, bhi-blo
	bestF, fx, fy := 0, 0, 0
	bestR, rx, ry := 0, 0, 0
	for o := 0; o < size; o++ {
		j := o - off
		if x := int(vf[o]); x >= 0 {
			if y := x - j; x <= nA && y >= 0 && y <= nB && x+y > bestF {
				bestF, fx, fy = x+y, x, y
			}
		}
		if x := int(vr[o]); x >= 0 {
			if y := x - j; x <= nA && y >= 0 && y <= nB && x+y > bestR {
				bestR, rx, ry = x+y, nA-x, nB-y
			}
		}
	}
	if bestF >= bestR {
		if bestF > 0 && (fx < nA || fy < nB) {
			return alo + fx, blo + fy
		}
	} else if rx > 0 || ry > 0 {
		return alo + rx, blo + ry
	}
	// No usable split. Halve both sides so the recursion still shrinks;
	// compare's guard turns a truly degenerate range into a replace.
	return alo + nA/2, blo + nB/2
}

// hunk is one contiguous region of the diff plus enough surrounding
// context (`contextLines` on each side) to make the output
// human-readable.
type hunk struct {
	aStart, aLen int
	bStart, bLen int
	ops          []op
}

// buildHunks partitions the full edit script into hunks separated by
// runs of `2*context+1` equal lines or longer (anything shorter than
// that gets merged into the surrounding hunk). The result is a list
// of hunks each containing only its own context plus changes; ops
// that fall in pure-equal runs *between* hunks are discarded.
func buildHunks(a, b []string, context int) []hunk {
	script := editScript(a, b, maxSearchCost)
	if len(script) == 0 {
		return nil
	}
	// Collect indices of changed ops, then expand by context.
	type changeRun struct{ lo, hi int }
	var runs []changeRun
	i := 0
	for i < len(script) {
		if script[i].kind == ' ' {
			i++
			continue
		}
		lo := i
		for i < len(script) && script[i].kind != ' ' {
			i++
		}
		runs = append(runs, changeRun{lo, i})
	}
	if len(runs) == 0 {
		return nil
	}
	// Merge runs whose context windows overlap.
	merged := []changeRun{runs[0]}
	for _, r := range runs[1:] {
		last := &merged[len(merged)-1]
		gap := r.lo - last.hi
		if gap <= 2*context {
			last.hi = r.hi
		} else {
			merged = append(merged, r)
		}
	}
	// Emit a hunk per merged run, with `context` lines of surrounding
	// equal context tacked on each side (clipped to the script
	// boundaries).
	out := []hunk{}
	for _, r := range merged {
		start := r.lo - context
		if start < 0 {
			start = 0
		}
		end := r.hi + context
		if end > len(script) {
			end = len(script)
		}
		// Compute aStart / bStart by counting the prefix of the
		// script before `start`.
		aPos, bPos := 0, 0
		for k := 0; k < start; k++ {
			switch script[k].kind {
			case ' ':
				aPos++
				bPos++
			case '-':
				aPos++
			case '+':
				bPos++
			}
		}
		aLen, bLen := 0, 0
		for k := start; k < end; k++ {
			switch script[k].kind {
			case ' ':
				aLen++
				bLen++
			case '-':
				aLen++
			case '+':
				bLen++
			}
		}
		out = append(out, hunk{
			aStart: aPos + 1, // unified-diff line numbers are 1-based
			aLen:   aLen,
			bStart: bPos + 1,
			bLen:   bLen,
			ops:    script[start:end],
		})
	}
	return out
}

// formatHunk turns one hunk into its `@@ -a,b +c,d @@\n<lines>`
// string. Each context / change line includes its prefix and the
// original trailing newline.
func formatHunk(h hunk) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.aStart, h.aLen, h.bStart, h.bLen)
	for _, op := range h.ops {
		sb.WriteByte(op.kind)
		sb.WriteString(op.line)
		// Each line in the script already carries its trailing
		// newline. If a final line lacks one (no-newline-at-eof
		// case), append a `\n` so the diff itself stays
		// well-formed.
		if len(op.line) == 0 || op.line[len(op.line)-1] != '\n' {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
