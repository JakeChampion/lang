package e2e

import (
	"strings"
	"testing"
)

// wasmFdReleaseProgram runs every file builtin many more times than the
// descriptor limit the test imposes. A body that keeps the descriptor it
// opened — the preview-2 bodies once dropped neither the own<descriptor>
// from open-at nor the stream on it — exhausts the limit within the first
// few dozen rounds and the round reports which builtin failed.
const wasmFdReleaseProgram = `
import "std/i32";

function round(i: i32): i32 {
    var s: string = "line " + i.to_string() + "\n";
    var wrote: i32 = match (write_file("f.txt", s)) { Ok(_) => 1, Err(_) => 0 };
    if (wrote == 0) { return 1; }
    var got: i32 = match (read_file("f.txt")) { Ok(t) => t.len(), Err(_) => 0 - 1 };
    if (got != s.len()) { return 2; }
    var gotb: i32 = match (read_file_bytes("f.txt")) { Ok(b) => b.len(), Err(_) => 0 - 1 };
    if (gotb != s.len()) { return 3; }
    match (open_writer("w.txt")) {
        Ok(w) => {
            match (w.write("x")) { Some(_) => { return 4; }, None => {} }
            match (w.close()) { Some(_) => { return 4; }, None => {} }
        },
        Err(_) => { return 4; }
    }
    match (open_appender("w.txt")) {
        Ok(w) => {
            match (w.write("y")) { Some(_) => { return 5; }, None => {} }
            match (w.close()) { Some(_) => { return 5; }, None => {} }
        },
        Err(_) => { return 5; }
    }
    match (open_reader("w.txt")) {
        Ok(r) => {
            match (r.read_line()) { Some(l) => { if (l != "xy") { return 6; } }, None => { return 6; } }
            match (r.close()) { Some(_) => { return 6; }, None => {} }
        },
        Err(_) => { return 6; }
    }
    var n: i32 = match (read_dir(".")) { Ok(es) => es.len(), Err(_) => 0 - 1 };
    if (n < 2) { return 7; }
    var isf: boolean = match (stat("f.txt")) { Ok(st) => st.is_file, Err(_) => false };
    if (!isf) { return 8; }
    return 0;
}

function main(): i32 {
    var i: i32 = 0;
    while (i < 200) {
        var r: i32 = round(i);
        if (r != 0) { write("FAIL " + r.to_string() + " at round " + i.to_string()); return r; }
        i = i + 1;
    }
    write("fds-released");
    return 0;
}
`

func TestWASMFileBuiltinsReleaseDescriptors(t *testing.T) {
	stdout, stderr, _, _ := runWasmInDirOpts(t, wasmFdReleaseProgram, nil, runOpts{fdLimit: 64})
	if !strings.Contains(stdout, "fds-released") {
		t.Fatalf("stdout %q stderr %q; want fds-released", stdout, stderr)
	}
}
