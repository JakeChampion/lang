package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestArm64ArgvStringsRcSafe(t *testing.T) {
	bin, qemu := compileArm64Bin(t, `function main(): i32 {
    let av: string[] = args();
    if (av.len() < 2) { return 2; }
    let entry: string = av[1];
    match (read_file(entry)) {
        Ok(src) => { return 0; },
        Err(e) => { return 1; }
    }
    return 4;
}
`)
	stdlibish := "/tmp/stdlib-root-shaped-arg-x"
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
