package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end for `fern -append-report` (#6992). internal/ir gates the
// decisions; this gates that the CLI mode reaches them at all — a report
// flag that silently stopped reporting would leave every one of those
// tests passing.
func TestAppendReportCLI(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	// `quadratic` is the shape the report exists for: an append loop that
	// reads as linear and is not, because the intermediate binding keeps
	// the old buffer readable across the grow.
	if err := os.WriteFile(src, []byte(`function grow(n: i32): i32 {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return xs.len();
}
function quadratic(n: i32): i32 {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < n) {
        var keep: i32[] = xs.append(i);
        xs = keep;
        i = i + 1;
    }
    return xs.len();
}
function main(): i32 { return grow(3) + quadratic(3); }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runAppendReport(src, &out); err != nil {
		t.Fatalf("runAppendReport: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		"grow:4:35",
		"quadratic:11:36",
		"2 append site(s), 1 copying",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		switch {
		case strings.HasPrefix(line, "grow:"):
			if !strings.Contains(line, "in place") {
				t.Errorf("grow's self-reassign append not reported in place: %q", line)
			}
		case strings.HasPrefix(line, "quadratic:"):
			if !strings.Contains(line, "COPY") {
				t.Errorf("quadratic's aliased append not reported as copying: %q", line)
			}
		}
	}
}

// A program with no appends must say so rather than printing an empty
// report that reads as "checked, nothing wrong".
func TestAppendReportNoSites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runAppendReport(src, &out); err != nil {
		t.Fatalf("runAppendReport: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "no .append sites") {
		t.Errorf("want an explicit empty-report line, got %q", got)
	}
}
