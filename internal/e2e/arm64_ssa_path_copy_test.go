package e2e

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// A path syscall wants a NUL-terminated C string, so every arm64ssa helper
// that takes a path copies it into the arena first. None gave the copy back:
// each call stranded path_len + 1 bytes (#8766). read_dir also bumped a 4 KiB
// getdents buffer per call, and remove_dir_all a 1 KiB one per level.
//
// Each operation below runs 16 times on short names and 16 times on names 150
// bytes longer, and the program exits with the operation's number when the
// arena grew by a different amount: a successful call's results do not depend
// on the path's length, so the difference is exactly what was stranded. Every
// call must succeed, since an IoError legitimately owns a copy of its path.
var arm64SSAPathCopyOps = []struct {
	name, body string
	// perCall, when set, bounds the growth of one call on the short names:
	// the scratch buffers read_dir and remove_dir_all used to strand do not
	// depend on the path, so the length comparison alone cannot see them.
	perCall int
}{
	{"access", `match (access(p, 4)) { Ok(_) => {}, Err(_) => { return 1; } }`, 0},
	{"open_reader", `match (open_reader(p)) { Ok(r) => { match (r.close()) { Some(_) => { return 1; }, None => {} } }, Err(_) => { return 1; } }`, 0},
	{"open_writer", `match (open_writer(p)) { Ok(r) => { match (r.close()) { Some(_) => { return 1; }, None => {} } }, Err(_) => { return 1; } }`, 0},
	{"open_appender", `match (open_appender(p)) { Ok(r) => { match (r.close()) { Some(_) => { return 1; }, None => {} } }, Err(_) => { return 1; } }`, 0},
	{"open_reader_with", `match (open_reader_with(p, 0)) { Ok(r) => { match (r.close()) { Some(_) => { return 1; }, None => {} } }, Err(_) => { return 1; } }`, 0},
	{"open_exclusive", `match (open_exclusive(q)) { Ok(r) => { match (r.close()) { Some(_) => { return 1; }, None => {} } }, Err(_) => { return 1; } }
    match (remove_file(q)) { Ok(_) => {}, Err(_) => { return 1; } }`, 0},
	{"write_file", `match (write_file(p, "abc")) { Ok(_) => {}, Err(_) => { return 1; } }`, 0},
	{"read_file", `match (read_file(p)) { Ok(s) => { if (s.len() != 3) { return 1; } }, Err(_) => { return 1; } }`, 0},
	{"read_file_bytes", `match (read_file_bytes(p)) { Ok(b) => { if (b.len() != 3) { return 1; } }, Err(_) => { return 1; } }`, 0},
	{"read_dir", `match (read_dir(d)) { Ok(es) => { if (es.len() != 0) { return 1; } }, Err(_) => { return 1; } }`, 1024},
	{"read_dir_all", `match (read_dir_all(d)) { Ok(es) => { if (es.len() != 2) { return 1; } }, Err(_) => { return 1; } }`, 1024},
	{"create_dir", `match (create_dir(q, 493)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (remove_dir(q)) { Ok(_) => {}, Err(_) => { return 1; } }`, 0},
	{"create_dir_all", `match (create_dir_all(q)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (remove_dir(q)) { Ok(_) => {}, Err(_) => { return 1; } }`, 0},
	{"remove_dir_all", `match (create_dir(q, 493)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (remove_dir_all(q)) { Ok(_) => {}, Err(_) => { return 1; } }`, 512},
	{"rename", `match (rename(p, q)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (rename(q, p)) { Ok(_) => {}, Err(_) => { return 1; } }`, 0},
	{"chmod", `match (chmod(p, 420)) { Ok(_) => {}, Err(_) => { return 1; } }`, 0},
	{"create_symlink", `match (create_symlink("t", q)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (read_link(q)) { Ok(s) => { if (s.len() != 1) { return 1; } }, Err(_) => { return 1; } }
    match (remove_file(q)) { Ok(_) => {}, Err(_) => { return 1; } }`, 0},
	{"create_link", `match (create_link(p, q)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (remove_file(q)) { Ok(_) => {}, Err(_) => { return 1; } }`, 0},
	{"set_file_times", `match (set_file_times(p, 1000000000, 0, 1000000000, 0, 0)) { Ok(_) => {}, Err(_) => { return 1; } }`, 0},
}

func arm64SSAPathCopySrc() string {
	long := strings.Repeat("L", 150)
	var b strings.Builder
	for i, op := range arm64SSAPathCopyOps {
		fmt.Fprintf(&b, "function op%d(p: string, q: string, d: string): i32 {\n    %s\n    return 0;\n}\n", i, op.body)
		fmt.Fprintf(&b, "function grow%d(p: string, q: string, d: string): i64 {\n", i)
		b.WriteString("    var b0: i64 = __heap_bump_bytes();\n    var i: i32 = 0;\n")
		fmt.Fprintf(&b, "    while (i < 16) { if (op%d(p, q, d) != 0) { return 0 - 1; } i = i + 1; }\n", i)
		b.WriteString("    return __heap_bump_bytes() - b0;\n}\n")
	}
	b.WriteString("function setup(p: string, d: string): i32 {\n")
	b.WriteString("    match (write_file(p, \"abc\")) { Ok(_) => {}, Err(_) => { return 1; } }\n")
	b.WriteString("    match (create_dir(d, 493)) { Ok(_) => {}, Err(_) => { return 1; } }\n")
	b.WriteString("    return 0;\n}\n")
	b.WriteString("function main(): i32 {\n")
	fmt.Fprintf(&b, "    var long: string = %q;\n", long)
	b.WriteString("    if (setup(\"f\", \"d\") != 0 || setup(long + \"f\", long + \"d\") != 0) { return 2; }\n")
	b.WriteString("    var bad: i32 = 0;\n")
	for i := range arm64SSAPathCopyOps {
		// Once untimed per length, so first-use costs (a freelist class
		// seeded, a handle cached) land outside the measurement.
		fmt.Fprintf(&b, "    if (grow%d(\"f\", \"q\", \"d\") < 0 || grow%d(long + \"f\", long + \"q\", long + \"d\") < 0) { return %d; }\n", i, i, 40+i)
		fmt.Fprintf(&b, "    var s%d: i64 = grow%d(\"f\", \"q\", \"d\");\n", i, i)
		fmt.Fprintf(&b, "    var l%d: i64 = grow%d(long + \"f\", long + \"q\", long + \"d\");\n", i, i)
		fmt.Fprintf(&b, "    if (s%d < 0 || l%d < 0) { return %d; }\n", i, i, 40+i)
		fmt.Fprintf(&b, "    if (l%d != s%d) { eprint(\"stranded: %s short=\" + s%d.to_string() + \" long=\" + l%d.to_string() + \"\\n\"); if (bad == 0) { bad = %d; } }\n", i, i, arm64SSAPathCopyOps[i].name, i, i, 10+i)
		if op := arm64SSAPathCopyOps[i]; op.perCall > 0 {
			fmt.Fprintf(&b, "    if (s%d > %d) { eprint(\"scratch kept: %s short=\" + s%d.to_string() + \"\\n\"); if (bad == 0) { bad = %d; } }\n", i, 16*op.perCall, op.name, i, 10+i)
		}
	}
	b.WriteString("    return bad;\n}\n")
	return "import \"std/i64\";\n" + b.String()
}

func TestArm64SSAPathCopiesGiveBackTheArena(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("arm64 -backend ssa not exercised on windows")
	}
	qemu := arm64QemuOrEmpty(t)
	fern := buildFernForArm64SSA(t)
	bin := compileArm64SSA(t, fern, arm64SSAPathCopySrc(), nil)
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), nil)
	switch {
	case code == 0:
	case code >= 10 && code < 10+len(arm64SSAPathCopyOps):
		t.Fatalf("a path copy or scratch buffer is stranded (#8766):\n%s", stderr)
	case code >= 40 && code < 40+len(arm64SSAPathCopyOps):
		t.Fatalf("%s failed on a path it should accept\n%s", arm64SSAPathCopyOps[code-40].name, stderr)
	default:
		t.Fatalf("exit=%d\n%s", code, stderr)
	}
}
