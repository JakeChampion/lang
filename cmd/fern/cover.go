package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

type coverFile struct {
	name        string
	hits        map[int]uint64
	lines       []int
	branches    map[branchKey]*branchCounts
	branchOrder []branchKey
}

type branchKey struct{ line, col int }

type branchCounts struct{ eval, taken uint64 }

func (b *branchCounts) falseCount() uint64 {
	if b.taken > b.eval {
		return 0
	}
	return b.eval - b.taken
}

func (b *branchCounts) edgesCovered() int {
	n := 0
	if b.taken > 0 {
		n++
	}
	if b.falseCount() > 0 {
		n++
	}
	return n
}

func fileFor(byName map[string]*coverFile, name string) *coverFile {
	f := byName[name]
	if f == nil {
		f = &coverFile{name: name, hits: map[int]uint64{}, branches: map[branchKey]*branchCounts{}}
		byName[name] = f
	}
	return f
}

func parseCoverBranchRow(byName map[string]*coverFile, lineno int, text string) error {
	rest := strings.TrimPrefix(text, ast.CoverBranchPrefix)
	sp := strings.LastIndex(rest, " ")
	if sp < 0 {
		return fmt.Errorf("line %d: %q: no count", lineno, text)
	}
	countStr := rest[sp+1:]
	rest = rest[:sp]
	sp = strings.LastIndex(rest, " ")
	if sp < 0 {
		return fmt.Errorf("line %d: %q: no edge", lineno, text)
	}
	edge := rest[sp+1:]
	site := rest[:sp]
	colSep := strings.LastIndex(site, ":")
	if colSep < 0 {
		return fmt.Errorf("line %d: %q: site is not <file>:<line>:<col>", lineno, text)
	}
	col, err := strconv.Atoi(site[colSep+1:])
	if err != nil {
		return fmt.Errorf("line %d: %q: bad column: %v", lineno, text, err)
	}
	site = site[:colSep]
	lineSep := strings.LastIndex(site, ":")
	if lineSep < 0 {
		return fmt.Errorf("line %d: %q: site is not <file>:<line>:<col>", lineno, text)
	}
	srcLine, err := strconv.Atoi(site[lineSep+1:])
	if err != nil {
		return fmt.Errorf("line %d: %q: bad source line: %v", lineno, text, err)
	}
	count, err := strconv.ParseUint(countStr, 10, 64)
	if err != nil {
		return fmt.Errorf("line %d: %q: bad count: %v", lineno, text, err)
	}
	f := fileFor(byName, site[:lineSep])
	k := branchKey{line: srcLine, col: col}
	b := f.branches[k]
	if b == nil {
		b = &branchCounts{}
		f.branches[k] = b
		f.branchOrder = append(f.branchOrder, k)
	}
	switch edge {
	case "E":
		b.eval += count
	case "T":
		b.taken += count
	default:
		return fmt.Errorf("line %d: %q: edge is %q, want E or T", lineno, text, edge)
	}
	return nil
}

func parseCoverStream(r io.Reader) ([]*coverFile, error) {
	byName := map[string]*coverFile{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for lineno := 1; sc.Scan(); lineno++ {
		text := sc.Text()
		if strings.HasPrefix(text, ast.CoverBranchPrefix) {
			if err := parseCoverBranchRow(byName, lineno, text); err != nil {
				return nil, err
			}
			continue
		}
		if !strings.HasPrefix(text, ast.CoverLinePrefix) {
			continue
		}
		rest := strings.TrimPrefix(text, ast.CoverLinePrefix)
		sp := strings.LastIndex(rest, " ")
		if sp < 0 {
			return nil, fmt.Errorf("line %d: %q: no count", lineno, text)
		}
		site, countStr := rest[:sp], rest[sp+1:]
		colon := strings.LastIndex(site, ":")
		if colon < 0 {
			return nil, fmt.Errorf("line %d: %q: site is not <file>:<line>", lineno, text)
		}
		name := site[:colon]
		srcLine, err := strconv.Atoi(site[colon+1:])
		if err != nil {
			return nil, fmt.Errorf("line %d: %q: bad source line: %v", lineno, text, err)
		}
		count, err := strconv.ParseUint(countStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: %q: bad count: %v", lineno, text, err)
		}
		f := fileFor(byName, name)
		if _, seen := f.hits[srcLine]; !seen {
			f.lines = append(f.lines, srcLine)
		}
		f.hits[srcLine] += count
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := make([]*coverFile, 0, len(byName))
	for _, f := range byName {
		sort.Ints(f.lines)
		sort.Slice(f.branchOrder, func(i, j int) bool {
			if f.branchOrder[i].line != f.branchOrder[j].line {
				return f.branchOrder[i].line < f.branchOrder[j].line
			}
			return f.branchOrder[i].col < f.branchOrder[j].col
		})
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func (f *coverFile) covered() int {
	n := 0
	for _, l := range f.lines {
		if f.hits[l] > 0 {
			n++
		}
	}
	return n
}

func (f *coverFile) uncovered() []int {
	var out []int
	for _, l := range f.lines {
		if f.hits[l] == 0 {
			out = append(out, l)
		}
	}
	return out
}

func (f *coverFile) branchEdges() (covered, total int) {
	for _, k := range f.branchOrder {
		covered += f.branches[k].edgesCovered()
		total += 2
	}
	return covered, total
}

func (f *coverFile) uncoveredBranches() []string {
	var out []string
	for _, k := range f.branchOrder {
		b := f.branches[k]
		if b.taken == 0 {
			out = append(out, fmt.Sprintf("%d:%d true", k.line, k.col))
		}
		if b.falseCount() == 0 {
			out = append(out, fmt.Sprintf("%d:%d false", k.line, k.col))
		}
	}
	return out
}

func writeCoverSummary(w io.Writer, files []*coverFile) error {
	var totalLines, totalCovered, totalEdges, totalEdgesHit int
	for _, f := range files {
		cov, all := f.covered(), len(f.lines)
		totalCovered += cov
		totalLines += all
		bHit, bAll := f.branchEdges()
		totalEdgesHit += bHit
		totalEdges += bAll
		if _, err := fmt.Fprintf(w, "%s: %s\n", f.name, coverCounts(cov, all, bHit, bAll)); err != nil {
			return err
		}
		if missing := f.uncovered(); len(missing) > 0 {
			if _, err := fmt.Fprintf(w, "  uncovered lines: %s\n", formatLineRanges(missing)); err != nil {
				return err
			}
		}
		if missing := f.uncoveredBranches(); len(missing) > 0 {
			if _, err := fmt.Fprintf(w, "  uncovered branches: %s\n", strings.Join(missing, ", ")); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(w, "total: %s\n", coverCounts(totalCovered, totalLines, totalEdgesHit, totalEdges))
	return err
}

func coverCounts(lineHit, lineAll, edgeHit, edgeAll int) string {
	out := fmt.Sprintf("%d/%d lines (%s)", lineHit, lineAll, coverPct(lineHit, lineAll))
	if edgeAll > 0 {
		out += fmt.Sprintf(", %d/%d branches (%s)", edgeHit, edgeAll, coverPct(edgeHit, edgeAll))
	}
	return out
}

func coverPct(n, total int) string {
	if total == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(total))
}

func formatLineRanges(lines []int) string {
	var parts []string
	for i := 0; i < len(lines); {
		j := i
		for j+1 < len(lines) && lines[j+1] == lines[j]+1 {
			j++
		}
		if j == i {
			parts = append(parts, strconv.Itoa(lines[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", lines[i], lines[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

func writeCoverLcov(w io.Writer, files []*coverFile) error {
	for _, f := range files {
		if _, err := fmt.Fprintf(w, "SF:%s\n", f.name); err != nil {
			return err
		}
		for _, l := range f.lines {
			if _, err := fmt.Fprintf(w, "DA:%d,%d\n", l, f.hits[l]); err != nil {
				return err
			}
		}
		for block, k := range f.branchOrder {
			b := f.branches[k]
			for branch, taken := range []uint64{b.falseCount(), b.taken} {
				val := strconv.FormatUint(taken, 10)
				if b.eval == 0 {
					val = "-"
				}
				if _, err := fmt.Fprintf(w, "BRDA:%d,%d,%d,%s\n", k.line, block, branch, val); err != nil {
					return err
				}
			}
		}
		edgesHit, edges := f.branchEdges()
		if _, err := fmt.Fprintf(w, "LF:%d\nLH:%d\n", len(f.lines), f.covered()); err != nil {
			return err
		}
		if edges > 0 {
			if _, err := fmt.Fprintf(w, "BRF:%d\nBRH:%d\n", edges, edgesHit); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprint(w, "end_of_record\n"); err != nil {
			return err
		}
	}
	return nil
}

func runCoverReport(path string, lcov bool, w io.Writer) error {
	r := io.Reader(os.Stdin)
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	files, err := parseCoverStream(r)
	if err != nil {
		return err
	}
	if lcov {
		return writeCoverLcov(w, files)
	}
	return writeCoverSummary(w, files)
}
