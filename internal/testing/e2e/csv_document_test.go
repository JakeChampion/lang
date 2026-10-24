package e2e

import "testing"

// Differential coverage for std/csv's full-document parser (csv.parse)
// and its inverse (csv.serialize) — the RFC 4180 multi-record surface
// beyond the single-record csv.parse_line. Returns 42 iff every case
// holds across backends: multi-record split, a quoted field with an
// embedded comma AND newline, doubled-quote escaping, CRLF endings with
// a trailing terminator (no spurious empty record), a trailing empty
// field, empty input, and a serialize→parse round-trip. Each leg skips
// itself when its toolchain is absent.
const csvDocProg = `
import "std/csv" as csv;
function __csv_rows(s: string): string[][] {
  match (csv.parse(s)) {
    Ok(rows) => { return rows; },
    Err(e) => { eprint(e.message()); exit(98); }
  }
  return [];
}

function main(): i32 {
    let r1: string[][] = __csv_rows("a,b,c\n1,2,3");
    if (r1.len() != 2 || r1[0][2] != "c" || r1[1][1] != "2") { return 1; }
    let r2: string[][] = __csv_rows("\"a,b\",\"line1\nline2\",z");
    if (r2.len() != 1 || r2[0].len() != 3) { return 2; }
    if (r2[0][0] != "a,b" || r2[0][1] != "line1\nline2") { return 3; }
    let r3: string[][] = __csv_rows("\"say \"\"hi\"\"\",x");
    if (r3[0][0] != "say \"hi\"") { return 4; }
    let r4: string[][] = __csv_rows("a,b\r\nc,d\r\n");
    if (r4.len() != 2 || r4[1][0] != "c" || r4[1][1] != "d") { return 5; }
    if (__csv_rows("a,")[0][1] != "") { return 6; }
    if (__csv_rows("").len() != 0) { return 7; }
    let rows: string[][] = [["x", "y,z"], ["1", "two\nlines"]];
    let back: string[][] = __csv_rows(csv.serialize(rows));
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
