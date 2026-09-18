package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `fern -array-report` over a program with a three-stage chain (#9730).
//
// Asserts the SHAPE rather than the whole rendering: the column widths move
// with the longest function name, and a test that pinned the layout would
// fail on an unrelated rename while telling nobody anything.
func TestArrayReportCLI(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(`import "std/array";
function run(xs: i64[]): i64 {
  var out: Option[i64] = xs
    .map((x: i64): i64 => x + (1 as i64))
    .filter((x: i64): boolean => x > (0 as i64))
    .reduce((a: i64, b: i64): i64 => a + b);
  match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return run([1 as i64, 2 as i64]) as i32; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runArrayReport(src, &out); err != nil {
		t.Fatalf("runArrayReport: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"map(n->n) -> filter(n->k) -> reduce(n->1)",
		"elementwise",
		"selection",
		"reduction",
		"1 pipeline(s), 1 with more than one stage",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not mention %q:\n%s", want, got)
		}
	}
}

// A program with no std/array pipeline says so, rather than printing an empty
// report a reader would have to interpret.
func TestArrayReportCLIEmpty(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runArrayReport(src, &out); err != nil {
		t.Fatalf("runArrayReport: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "no std/array pipelines") {
		t.Errorf("want the empty-case line, got:\n%s", got)
	}
}
