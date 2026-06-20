package e2e

import (
	"strings"
	"testing"
)

// Runtime tests for two std helpers added to simplify the
// examples/cli/ tools: `(s: string).parse_int_or(fallback)` and
// `io.read_input(path)` (stdin when the operand is "-" / "", a file
// otherwise). Type-check-on-import is covered by
// TestStdlibModulesImportStandalone; these pin the behaviour.

// parse_int_or returns the parsed value on success and the fallback
// on any parse failure. Encoded into the exit code: 42 (parsed) + 7
// (fallback for "nope") + 5 (fallback for "") + 9 (fallback for the
// overflow case) = 63.
const parseIntOrSrc = `
import "std/string";
function main(): i32 {
    var a: i32 = "42".parse_int_or(0);
    var b: i32 = "nope".parse_int_or(7);
    var c: i32 = "".parse_int_or(5);
    var d: i32 = "99999999999".parse_int_or(9);
    return a + b + c + d;
}`

func TestX86_64ParseIntOr(t *testing.T) {
	if _, code := compileAndRunX86_64(t, parseIntOrSrc); code != 63 {
		t.Errorf("got %d, want 63 (42 + 7 + 5 + 9)", code)
	}
}

func TestArm64ParseIntOr(t *testing.T) {
	if _, code := compileAndRunArm64(t, parseIntOrSrc); code != 63 {
		t.Errorf("got %d, want 63", code)
	}
}

func TestWASMParseIntOr(t *testing.T) {
	if got := runWasm(t, parseIntOrSrc); got != 63 {
		t.Errorf("got %d, want 63", got)
	}
}

// read_input("-") reads all of stdin. (The component runner echoes
// main's return value onto stdout, so we assert the written bytes
// are present rather than matching stdout exactly.)
func TestWASMReadInputStdin(t *testing.T) {
	src := `
import "std/string";
import "std/io";
function main(): i32 {
    match (io.read_input("-")) {
        Ok(t) => { write(t); return 0; },
        Err(_) => { write("ERR"); return 1; }
    }
}`
	out, _, _ := runWasmStdinEnv(t, src, "piped input\n", nil)
	if !strings.Contains(out, "piped input") || strings.Contains(out, "ERR") {
		t.Errorf("stdout = %q, want it to contain the piped stdin and no ERR", out)
	}
}

// read_input(path) reads a named file (Ok), and reports Err for a
// missing one — the two arms every cli tool relies on. The arms
// write distinct markers so we can assert which fired from stdout.
func TestWASMReadInputFile(t *testing.T) {
	src := `
import "std/string";
import "std/io";
function main(): i32 {
    match (io.read_input("data.txt")) {
        Ok(s) => { write("OK:" + s); return 0; },
        Err(_) => { write("ERR"); return 1; }
    }
}`
	out, _, _, _ := runWasmInDir(t, src, map[string]string{"data.txt": "from a file"})
	if !strings.Contains(out, "OK:from a file") {
		t.Errorf("Ok path stdout = %q, want it to contain %q", out, "OK:from a file")
	}

	missSrc := `
import "std/string";
import "std/io";
function main(): i32 {
    match (io.read_input("does-not-exist.txt")) {
        Ok(_) => { write("OK"); return 0; },
        Err(_) => { write("ERR"); return 1; }
    }
}`
	mout, _, _, _ := runWasmInDir(t, missSrc, nil)
	if !strings.Contains(mout, "ERR") {
		t.Errorf("Err path stdout = %q, want it to contain ERR (missing file should be Err)", mout)
	}
}
