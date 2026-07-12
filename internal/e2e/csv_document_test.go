package e2e

import "testing"

// Differential coverage for std/csv's full-document parser (csv_parse)
// and its inverse (csv_serialize) — the RFC 4180 multi-record surface
// beyond the single-record csv_parse_line. Returns 42 iff every case
// holds across backends: multi-record split, a quoted field with an
// embedded comma AND newline, doubled-quote escaping, CRLF endings with
// a trailing terminator (no spurious empty record), a trailing empty
// field, empty input, and a serialize→parse round-trip. Each leg skips
// itself when its toolchain is absent.
const csvDocProg = `
import "std/csv" as csv;
function main(): i32 {
    var r1: string[][] = csv.csv_parse("a,b,c\n1,2,3");
    if (r1.len() != 2 || r1[0][2] != "c" || r1[1][1] != "2") { return 1; }
    var r2: string[][] = csv.csv_parse("\"a,b\",\"line1\nline2\",z");
    if (r2.len() != 1 || r2[0].len() != 3) { return 2; }
    if (r2[0][0] != "a,b" || r2[0][1] != "line1\nline2") { return 3; }
    var r3: string[][] = csv.csv_parse("\"say \"\"hi\"\"\",x");
    if (r3[0][0] != "say \"hi\"") { return 4; }
    var r4: string[][] = csv.csv_parse("a,b\r\nc,d\r\n");
    if (r4.len() != 2 || r4[1][0] != "c" || r4[1][1] != "d") { return 5; }
    if (csv.csv_parse("a,")[0][1] != "") { return 6; }
    if (csv.csv_parse("").len() != 0) { return 7; }
    var rows: string[][] = [["x", "y,z"], ["1", "two\nlines"]];
    var back: string[][] = csv.csv_parse(csv.csv_serialize(rows));
    if (back.len() != 2 || back[0][1] != "y,z" || back[1][1] != "two\nlines") { return 8; }
    return 42;
}
`

func TestCsvDocInterp(t *testing.T) {
	if got := runInterpExit(t, csvDocProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestCsvDocX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, csvDocProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestCsvDocWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, csvDocProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestCsvDocArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, csvDocProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
