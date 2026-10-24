package main

import (
	"strings"
	"testing"
)

const coverStream = `some program output
fern-cover: a.fern:2 3
fern-cover: a.fern:3 0
fern-cover: a.fern:4 0
fern-cover: a.fern:5 0
fern-cover: a.fern:9 1
warning: unrelated stderr chatter
fern-cover: b.fern:1 7
`

const coverBranchStream = `fern-cover: a.fern:2 4
fern-cover: a.fern:9 1
fern-branch: a.fern:2:5 E 4
fern-branch: a.fern:2:5 T 3
fern-branch: a.fern:2:19 E 4
fern-branch: a.fern:2:19 T 0
fern-branch: a.fern:9:5 E 0
fern-branch: a.fern:9:5 T 0
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
		"  uncovered lines: 3-5\n",
		"b.fern: 1/1 lines (100.0%)\n",
		"total: 3/6 lines (50.0%)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "branches") {
		t.Errorf("summary reports branches for a stream with none; got:\n%s", got)
	}
	if strings.Count(got, "uncovered lines:") != 1 {
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

func TestCoverReportBranchSummary(t *testing.T) {
	var b strings.Builder
	files, err := parseCoverStream(strings.NewReader(coverBranchStream))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := writeCoverSummary(&b, files); err != nil {
		t.Fatalf("summary: %v", err)
	}
	got := b.String()
	for _, want := range []string{
		"a.fern: 2/2 lines (100.0%), 3/6 branches (50.0%)\n",
		"  uncovered branches: 2:19 true, 9:5 true, 9:5 false\n",
		"total: 2/2 lines (100.0%), 3/6 branches (50.0%)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q; got:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "100.0%), 3/6") {
		t.Errorf("expected fully-covered lines alongside half-covered branches; got:\n%s", got)
	}
}

func TestCoverReportBranchLcov(t *testing.T) {
	var b strings.Builder
	files, err := parseCoverStream(strings.NewReader(coverBranchStream))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := writeCoverLcov(&b, files); err != nil {
		t.Fatalf("lcov: %v", err)
	}
	got := b.String()
	want := "SF:a.fern\nDA:2,4\nDA:9,1\n" +
		"BRDA:2,0,0,1\nBRDA:2,0,1,3\n" +
		"BRDA:2,1,0,4\nBRDA:2,1,1,0\n" +
		"BRDA:9,2,0,-\nBRDA:9,2,1,-\n" +
		"LF:2\nLH:2\nBRF:6\nBRH:3\nend_of_record\n"
	if got != want {
		t.Errorf("lcov output:\n%s\nwant:\n%s", got, want)
	}
}

func TestCoverReportRejectsMalformedBranchRows(t *testing.T) {
	for _, bad := range []string{
		"fern-branch: a.fern:2:5 E\n",
		"fern-branch: a.fern:2:5 X 1\n",
		"fern-branch: a.fern:2:x E 1\n",
		"fern-branch: a.fern:2:5 E nope\n",
		"fern-branch: nocolon E 1\n",
	} {
		if _, err := parseCoverStream(strings.NewReader(bad)); err == nil {
			t.Errorf("parseCoverStream(%q) succeeded, want an error", bad)
		}
	}
}

func TestCoverReportClampsImpossibleBranchCounts(t *testing.T) {
	files, err := parseCoverStream(strings.NewReader(
		"fern-branch: a.fern:1:1 E 1\nfern-branch: a.fern:1:1 T 5\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := files[0].branches[branchKey{line: 1, col: 1}]
	if got := b.falseCount(); got != 0 {
		t.Errorf("falseCount() = %d, want 0", got)
	}
	if got := b.edgesCovered(); got != 1 {
		t.Errorf("edgesCovered() = %d, want 1 (the true edge only)", got)
	}
}
