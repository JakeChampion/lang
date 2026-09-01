package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Coverage reporting (#5548) — the read half of `fern -cover`.
//
// A -cover build writes one line per instrumented source line to stderr
// as it exits:
//
//	fern-cover: <file>:<line> <count>
//
// mixed in with whatever the program itself printed there. runCoverReport
// reads a saved copy of that stream, keeps the lines carrying the prefix,
// and folds them into per-file totals — or into lcov, for the tools that
// already know how to draw a coverage report.
//
// The stream is the whole truth: a -cover binary emits a line for every
// instrumented line whether it ran or not, so the rows present ARE the
// denominator and a missing row means the compile never instrumented that
// line, not that it went unexecuted.

// coverFile is one file's folded counts, lines ascending.
type coverFile struct {
	name  string
	hits  map[int]uint64
	lines []int
	// branches holds the two-counter pair per conditional (#5548 slice 3),
	// keyed by the conditional's (line, col) so two branches on one line —
	// an `&&` inside an `if` — stay apart. branchOrder keeps them
	// reportable in source order.
	branches    map[branchKey]*branchCounts
	branchOrder []branchKey
}

// branchKey identifies one conditional within a file.
type branchKey struct{ line, col int }

// branchCounts is one conditional's pair: how often it was evaluated and
// how often it was true. The false edge is the difference — the lowering
// has no `else` arm to hang a third counter on when the source wrote
// none, so it is derived rather than measured.
type branchCounts struct{ eval, taken uint64 }

// falseCount is how often the conditional took its false edge. Clamped at
// zero: eval and taken are read from a stream that may have been
// truncated or concatenated oddly, and a negative edge count would be a
// nonsense the report should not propagate.
func (b *branchCounts) falseCount() uint64 {
	if b.taken > b.eval {
		return 0
	}
	return b.eval - b.taken
}

// edgesCovered is how many of this conditional's two edges ran.
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

// fileFor returns the accumulator for one path, creating it on first
// sight. Line rows and branch rows both land in the same record.
func fileFor(byName map[string]*coverFile, name string) *coverFile {
	f := byName[name]
	if f == nil {
		f = &coverFile{name: name, hits: map[int]uint64{}, branches: map[branchKey]*branchCounts{}}
		byName[name] = f
	}
	return f
}

// parseCoverBranchRow folds one `fern-branch: <file>:<line>:<col> <E|T>
// <count>` row. Like the line rows, a malformed one is an error rather
// than a skip: a dropped row would silently move a percentage.
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

// parseCoverStream folds a saved -cover run's output into per-file
// counts. Lines without the prefix are the program's own stderr and are
// skipped; a prefixed line that does not parse is an error, because
// silently dropping it would understate the total it belongs to.
//
// Repeated rows for one line sum, so several runs' output can be
// concatenated into one report.
func parseCoverStream(r io.Reader) ([]*coverFile, error) {
	byName := map[string]*coverFile{}
	sc := bufio.NewScanner(r)
	// A report line is short, but the program's own stderr shares the
	// stream and need not be — a long line must not abort the scan.
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

// covered counts this file's instrumented lines that ran at least once.
func (f *coverFile) covered() int {
	n := 0
	for _, l := range f.lines {
		if f.hits[l] > 0 {
			n++
		}
	}
	return n
}

// uncovered lists this file's instrumented lines that never ran.
func (f *coverFile) uncovered() []int {
	var out []int
	for _, l := range f.lines {
		if f.hits[l] == 0 {
			out = append(out, l)
		}
	}
	return out
}

// branchEdges is this file's covered and total branch edges — two per
// conditional, since a conditional the run never reached still has two
// edges nobody took.
func (f *coverFile) branchEdges() (covered, total int) {
	for _, k := range f.branchOrder {
		covered += f.branches[k].edgesCovered()
		total += 2
	}
	return covered, total
}

// uncoveredBranches names each edge that never ran, in source order:
// `12:3 true` for a conditional at line 12, column 3 whose true arm was
// never taken. A conditional the run never reached contributes both.
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

// writeCoverSummary prints per-file line and branch coverage with the
// uncovered lines and edges, then a total. Uncovered lines are collapsed
// into ranges: a hundred consecutive dead lines are one fact, not a
// hundred.
//
// Branch figures are omitted from a file with no conditionals rather than
// printed as 0/0 — a file with nothing to branch on has not failed to
// cover anything.
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

// coverCounts renders the one-line "N/M lines (P), N/M branches (P)"
// figure, dropping the branch half when there are no conditionals.
func coverCounts(lineHit, lineAll, edgeHit, edgeAll int) string {
	out := fmt.Sprintf("%d/%d lines (%s)", lineHit, lineAll, coverPct(lineHit, lineAll))
	if edgeAll > 0 {
		out += fmt.Sprintf(", %d/%d branches (%s)", edgeHit, edgeAll, coverPct(edgeHit, edgeAll))
	}
	return out
}

// coverPct renders a percentage, or "n/a" for an empty denominator —
// reporting 0.0% for a program with nothing instrumented would read as a
// result rather than as the absence of one.
func coverPct(n, total int) string {
	if total == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(total))
}

// formatLineRanges collapses an ascending line list into comma-separated
// runs: [3 4 5 9] → "3-5, 9".
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

// writeCoverLcov writes the tracefile form every existing coverage
// viewer reads: one SF/DA/BRDA/LF/LH/BRF/BRH record set per file. Fern
// has no per-function records to give, so FN/FNDA are absent rather than
// emitted empty.
//
// Each conditional becomes two BRDA rows sharing a block id — its index
// within the file, so two conditionals on one line stay distinct — with
// branch 0 the false edge and 1 the true one. A conditional the run never
// evaluated writes `-` rather than 0 on both, which is lcov's spelling
// for "never reached", not "reached and not taken".
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

// runCoverReport implements `fern -cover-report FILE` (`-` for stdin):
// fold a saved -cover run's output into a per-file summary, or into lcov
// with -lcov.
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
