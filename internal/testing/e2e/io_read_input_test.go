package e2e

import (
	"strings"
	"testing"
)

const parseIntOrSrc = `
import "std/i32";
import "std/result";
import "std/string";
function main(): i32 {
    let a: i32 = i32.parse_i32("42").unwrap_or(0);
    let b: i32 = i32.parse_i32("nope").unwrap_or(7);
    let c: i32 = i32.parse_i32("").unwrap_or(5);
    let d: i32 = i32.parse_i32("99999999999").unwrap_or(9);
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
