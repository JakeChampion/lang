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
		f := byName[name]
		if f == nil {
			f = &coverFile{name: name, hits: map[int]uint64{}}
			byName[name] = f
		}
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

// writeCoverSummary prints per-file line coverage and the uncovered
// lines, then a total. Uncovered lines are collapsed into ranges: a
// hundred consecutive dead lines are one fact, not a hundred.
func writeCoverSummary(w io.Writer, files []*coverFile) error {
	var totalLines, totalCovered int
	for _, f := range files {
		cov, all := f.covered(), len(f.lines)
		totalCovered += cov
		totalLines += all
		if _, err := fmt.Fprintf(w, "%s: %d/%d lines (%s)\n", f.name, cov, all, coverPct(cov, all)); err != nil {
			return err
		}
		if missing := f.uncovered(); len(missing) > 0 {
			if _, err := fmt.Fprintf(w, "  uncovered: %s\n", formatLineRanges(missing)); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(w, "total: %d/%d lines (%s)\n", totalCovered, totalLines, coverPct(totalCovered, totalLines))
	return err
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
// viewer reads: one SF/DA/LF/LH record set per file. Fern has no
// function or branch records to give yet, so FN/BR are absent rather
// than emitted empty.
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
		if _, err := fmt.Fprintf(w, "LF:%d\nLH:%d\nend_of_record\n", len(f.lines), f.covered()); err != nil {
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
