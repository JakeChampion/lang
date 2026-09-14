package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// `exit(n)` reaches a preview-2 host through wasi:cli/exit, whose
// argument is a `result<_, _>`: one bit. Every non-zero code is that
// bit, and the output written before the call is not lost.
func TestWASMExitCodeCollapsesToOne(t *testing.T) {
	for _, c := range []struct{ code, want int }{{0, 0}, {1, 1}, {3, 1}, {125, 1}} {
		src := fmt.Sprintf(`function main(): i32 {
    print("before exit");
    exit(%d);
    return 0;
}`, c.code)
		stdout, stderr, ec := runWasmStdinEnv(t, src, "", nil)
		if ec != c.want {
			t.Errorf("exit(%d): wasmtime exit %d, want %d\nstderr:\n%s", c.code, ec, c.want, stderr)
		}
		if !strings.Contains(stdout, "before exit") {
			t.Errorf("exit(%d): stdout lost the line written before it: %q", c.code, stdout)
		}
	}
}

// A host that preopens no directory leaves every path operation with
// nothing to resolve against. The answer is NotFound, the same as a
// path that is not there, rather than a trap on a handle the host
// never issued.
func TestWASMNoPreopenIsNotFound(t *testing.T) {
	src := `function say(op: string, e: IoError): void {
    match (e) {
        NotFound(_) => { print(op + " NotFound"); },
        _ => { print(op + " other"); }
    }
}
function main(): i32 {
    match (open_reader("f")) { Ok(_) => { print("open_reader ok"); }, Err(e) => { say("open_reader", e); } }
    match (open_writer("f")) { Ok(_) => { print("open_writer ok"); }, Err(e) => { say("open_writer", e); } }
    match (read_file("f")) { Ok(_) => { print("read_file ok"); }, Err(e) => { say("read_file", e); } }
    match (write_file("f", "x")) { Ok(_) => { print("write_file ok"); }, Err(e) => { say("write_file", e); } }
    match (stat("f")) { Ok(_) => { print("stat ok"); }, Err(e) => { say("stat", e); } }
    match (remove_file("f")) { Ok(_) => { print("remove_file ok"); }, Err(e) => { say("remove_file", e); } }
    match (create_dir("d", 493)) { Ok(_) => { print("create_dir ok"); }, Err(e) => { say("create_dir", e); } }
    match (read_dir(".")) { Ok(_) => { print("read_dir ok"); }, Err(e) => { say("read_dir", e); } }
    match (remove_dir("d")) { Ok(_) => { print("remove_dir ok"); }, Err(e) => { say("remove_dir", e); } }
    match (rename("f", "g")) { Ok(_) => { print("rename ok"); }, Err(e) => { say("rename", e); } }
    match (read_link("f")) { Ok(_) => { print("read_link ok"); }, Err(e) => { say("read_link", e); } }
    match (create_symlink("f", "g")) { Ok(_) => { print("create_symlink ok"); }, Err(e) => { say("create_symlink", e); } }
    match (truncate("f", 0 as i64)) { Ok(_) => { print("truncate ok"); }, Err(e) => { say("truncate", e); } }
    match (create_dir_all("d/e")) { Ok(_) => { print("create_dir_all ok"); }, Err(e) => { say("create_dir_all", e); } }
    match (temp_dir("t")) { Ok(_) => { print("temp_dir ok"); }, Err(e) => { say("temp_dir", e); } }
    match (remove_dir_all("d")) { Ok(_) => { print("remove_dir_all ok"); }, Err(e) => { say("remove_dir_all", e); } }
    return 0;
}`
	stdout, stderr, ec := runWasmStdinEnv(t, src, "", nil)
	if ec != 0 {
		t.Fatalf("wasmtime exit %d\nstdout:\n%s\nstderr:\n%s", ec, stdout, stderr)
	}
	for _, op := range []string{"open_reader", "open_writer", "read_file", "write_file", "stat", "remove_file", "create_dir", "read_dir",
		"remove_dir", "rename", "read_link", "create_symlink", "truncate", "create_dir_all", "temp_dir"} {
		if !strings.Contains(stdout, op+" NotFound\n") {
			t.Errorf("%s without a preopen: want NotFound, stdout:\n%s", op, stdout)
		}
	}
	// remove_dir_all ignores a path that is not there, and with no
	// directory to look in nothing is.
	if !strings.Contains(stdout, "remove_dir_all ok\n") {
		t.Errorf("remove_dir_all without a preopen: want ok, stdout:\n%s", stdout)
	}
}
