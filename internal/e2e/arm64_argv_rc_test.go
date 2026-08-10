package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestArm64ArgvStringsRcSafe pins the args() materialisation against the
// rc-header corruption fixed in the two-word arm64 variant: argv strings
// were allocated with plain __fern_alloc (no L2 rc header), but every rc
// consumer (__fern_str_inc / __fern_str_dec / the __fern_drop_arr_str
// element sweep) read-modify-writes the rc word at data-8 — which, for a
// headerless block, is the tail of the PREVIOUS argv string. Binding an
// args() element to a local (inc) or letting the array drop (dec sweep)
// then silently bumped a byte inside a neighbouring argv string by ±1.
// The damage is length-dependent (the touched word must land inside a
// live string), which is why it surfaced as path-length-sensitive
// flakiness — openat on argv[1] failing with one byte off by one — that
// forced suites off the native-mmc gate instead of failing anywhere
// deterministically.
//
// The program is the minimal shape that provokes the live rc ops: bind
// av[1] to a local so the ARRAY dies at the bind (Perceus then runs the
// drop_arr_str element sweep — whose dec of the FOLLOWING argv string
// lands in av[1]'s tail under the bug) and read through the local
// afterwards. Keeping any later av[...] use would hold the array live
// past the read and mask the corruption, as would a borrow-only op mix.
// The harness materialises a REAL file at every swept length and passes
// trailing arguments mirroring the driver command lines that failed
// (stdlib-root-shaped arg + -target arm64), covering the allocation-
// class-boundary bands (59..65, 79..81) the original failures landed in.
func TestArm64ArgvStringsRcSafe(t *testing.T) {
	bin, qemu := compileArm64Bin(t, `function main(): i32 {
    var av: string[] = args();
    if (av.len() < 2) { return 2; }
    var entry: string = av[1];
    match (read_file(entry)) {
        Ok(src) => { return 0; },
        Err(e) => { return 1; }
    }
    return 4;
}
`)
	stdlibish := "/tmp/stdlib-root-shaped-arg-x" // ~29 chars, like the real invocations
	for _, n := range []int{42, 59, 60, 61, 62, 63, 64, 65, 79, 80, 81, 127, 128} {
		prefix := fmt.Sprintf("/tmp/argvrc%d-", os.Getpid())
		if n-len(prefix) < 1 {
			continue
		}
		path := prefix + strings.Repeat("p", n-len(prefix))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		defer os.Remove(path)
		cmd := runArm64Bin(qemu, bin, path, stdlibish, "-target", "arm64-linux")
		out, _ := cmd.CombinedOutput()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("len=%d: did not exit normally (out=%q)", n, out)
		}
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("len=%d: read_file(argv[1]) failed on an existing file (exit=%d) — argv byte corrupted before openat", n, code)
		}
	}
}
