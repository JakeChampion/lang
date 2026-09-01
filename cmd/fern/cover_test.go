package main

import (
	"strings"
	"testing"
)

// The -cover-report reader (#5548). Its contract is narrow and load-
// bearing: the rows in the stream ARE the denominator, the program's own
// stderr shares the stream and must be ignored, and a row that does not
// parse must be an error rather than a silently smaller total.

const coverStream = `some program output
fern-cover: a.fern:2 3
fern-cover: a.fern:3 0
fern-cover: a.fern:4 0
fern-cover: a.fern:5 0
fern-cover: a.fern:9 1
warning: unrelated stderr chatter
fern-cover: b.fern:1 7
`

func TestCoverReportSummary(t *testing.T) {
	var b strings.Builder
	files, err := parseCoverStream(strings.NewReader(coverStream))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := writeCoverSummary(&b, files); err != nil {
		t.Fatalf("summary: %v", err)
	}
	got := b.String()
	for _, want := range []string{
		"a.fern: 2/5 lines (40.0%)\n",
		// Consecutive dead lines collapse: a hundred of them is one
		// fact, not a hundred.
		"  uncovered: 3-5\n",
		"b.fern: 1/1 lines (100.0%)\n",
		"total: 3/6 lines (50.0%)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q; got:\n%s", want, got)
		}
	}
	// A fully-covered file has nothing to list.
	if strings.Count(got, "uncovered:") != 1 {
		t.Errorf("summary lists uncovered lines for a fully-covered file; got:\n%s", got)
	}
}

func TestCoverReportLcov(t *testing.T) {
	var b strings.Builder
	files, err := parseCoverStream(strings.NewReader(coverStream))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := writeCoverLcov(&b, files); err != nil {
		t.Fatalf("lcov: %v", err)
	}
	got := b.String()
	want := "SF:a.fern\nDA:2,3\nDA:3,0\nDA:4,0\nDA:5,0\nDA:9,1\nLF:5\nLH:2\nend_of_record\n" +
		"SF:b.fern\nDA:1,7\nLF:1\nLH:1\nend_of_record\n"
	if got != want {
		t.Errorf("lcov output:\n%s\nwant:\n%s", got, want)
	}
}

// Concatenated runs sum, so a suite split across several binaries reports
// as one measurement rather than as the last one to write.
func TestCoverReportSumsRepeatedRows(t *testing.T) {
	files, err := parseCoverStream(strings.NewReader(
		"fern-cover: a.fern:2 3\nfern-cover: a.fern:2 4\nfern-cover: a.fern:3 0\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if got := files[0].hits[2]; got != 7 {
		t.Errorf("a.fern:2 summed to %d, want 7", got)
	}
	if got := files[0].covered(); got != 1 {
		t.Errorf("covered = %d, want 1", got)
	}
}

// A path with a colon in it (a Windows drive letter, a URL-ish stdlib
// path) still splits at the LAST colon, so the line number is the line
// number.
func TestCoverReportSplitsAtTheLastColon(t *testing.T) {
	files, err := parseCoverStream(strings.NewReader("fern-cover: stdlib://std/io.fern:12 1\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(files) != 1 || files[0].name != "stdlib://std/io.fern" {
		t.Fatalf("got %+v, want one file named stdlib://std/io.fern", files)
	}
	if files[0].hits[12] != 1 {
		t.Errorf("line 12 hits = %d, want 1", files[0].hits[12])
	}
}

// A malformed prefixed row is a truncated or corrupted stream. Skipping it
// would shrink the denominator and inflate the percentage — the one
// failure a coverage number must never make quietly.
func TestCoverReportRejectsMalformedRows(t *testing.T) {
	for _, bad := range []string{
		"fern-cover: a.fern:2\n",
		"fern-cover: a.fern:x 1\n",
		"fern-cover: a.fern:2 nope\n",
		"fern-cover: nocolon 1\n",
	} {
		if _, err := parseCoverStream(strings.NewReader(bad)); err == nil {
			t.Errorf("parseCoverStream(%q) succeeded, want an error", bad)
		}
	}
}

// Nothing instrumented is the absence of a measurement, not a 0.0% one.
func TestCoverReportEmptyStream(t *testing.T) {
	var b strings.Builder
	files, err := parseCoverStream(strings.NewReader("just program output\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := writeCoverSummary(&b, files); err != nil {
		t.Fatalf("summary: %v", err)
	}
	if got, want := b.String(), "total: 0/0 lines (n/a)\n"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestFormatLineRanges(t *testing.T) {
	for _, c := range []struct {
		in   []int
		want string
	}{
		{[]int{3}, "3"},
		{[]int{3, 4, 5}, "3-5"},
		{[]int{3, 4, 5, 9}, "3-5, 9"},
		{[]int{1, 3, 5}, "1, 3, 5"},
		{[]int{1, 2, 7, 8, 9}, "1-2, 7-9"},
	} {
		if got := formatLineRanges(c.in); got != c.want {
			t.Errorf("formatLineRanges(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
