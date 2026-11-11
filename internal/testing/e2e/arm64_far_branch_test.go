package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Each arm of the `if` is more than 1 MiB of arm64 code, so the branch over
// an arm and the branch back to the loop head are out of the reach of a
// conditional branch.
func TestArm64FarConditionalBranch(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	var prog strings.Builder
	prog.WriteString("function main(): i32 {\n  let x: i32 = 0;\n  let i: i32 = 0;\n  while (i < 3) {\n    if (i % 2 == 0) {\n")
	for k := 0; k < 70000; k++ {
		fmt.Fprintf(&prog, "      x = x * 3 + %d;\n", k%7)
	}
	prog.WriteString("    } else {\n")
	for k := 0; k < 70000; k++ {
		fmt.Fprintf(&prog, "      x = x * 5 - %d;\n", k%5)
	}
	prog.WriteString("    }\n    i = i + 1;\n  }\n  print(x);\n  return 0;\n}\n")
	src := filepath.Join(dir, "far.fern")
	if err := os.WriteFile(src, []byte(prog.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"arm64-linux", "arm64-darwin"} {
		out := filepath.Join(dir, target)
		if o, err := exec.Command(bin, "-target", target, "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("%s build failed: %v\n%s", target, err, o)
		}
	}
	cmd := exec.Command(filepath.Join(dir, "arm64-linux"))
	if qemu != "" {
		cmd = exec.Command(qemu, filepath.Join(dir, "arm64-linux"))
	}
	stdout, err := cmd.CombinedOutput()
	if err != nil || string(stdout) != "84882080\n" {
		t.Fatalf("arm64-linux run = (%q, %v), want %q", stdout, err, "84882080\n")
	}
}
