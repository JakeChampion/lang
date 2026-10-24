package printer

import (
	"fmt"
	"strings"
)

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
		out = append(out, s[start:])
	}
	return out
}

type op struct {
	kind byte
	line string
}

type anchor struct{ a, b int }

const maxSearchCost = 1 << 14

func editScript(a, b []string, maxCost int) []op {
	m, n := len(a), len(b)
	ia, ib, distinct := internLines(a, b)

	pre := 0
	for pre < m && pre < n && ia[pre] == ib[pre] {
		pre++
	}
	suf := 0
	for suf < m-pre && suf < n-pre && ia[m-1-suf] == ib[n-1-suf] {
		suf++
	}

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

type differ struct {
	ia, ib     []int32
	mapA, mapB []int
	anchors    []anchor
	vf, vr     []int32
	maxCost    int
}

func (d *differ) compare(alo, ahi, blo, bhi int) {
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
		if (x != alo || y != blo) && (x != ahi || y != bhi) {
			d.compare(alo, x, blo, y)
			d.compare(x, ahi, y, bhi)
		}
	}

	for k := 0; k < suffix; k++ {
		d.anchors = append(d.anchors, anchor{ahi + k, bhi + k})
	}
}

func (d *differ) bisect(alo, ahi, blo, bhi int) (int, int) {
	nA, nB := ahi-alo, bhi-blo
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
	meetForward := delta%2 != 0
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
	return alo + nA/2, blo + nB/2
}

type hunk struct {
	aStart, aLen int
	bStart, bLen int
	ops          []op
}

func buildHunks(a, b []string, context int) []hunk {
	script := editScript(a, b, maxSearchCost)
	if len(script) == 0 {
		return nil
	}
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
			aStart: aPos + 1,
			aLen:   aLen,
			bStart: bPos + 1,
			bLen:   bLen,
			ops:    script[start:end],
		})
	}
	return out
}

func formatHunk(h hunk) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.aStart, h.aLen, h.bStart, h.bLen)
	for _, op := range h.ops {
		sb.WriteByte(op.kind)
		sb.WriteString(op.line)
		if len(op.line) == 0 || op.line[len(op.line)-1] != '\n' {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
