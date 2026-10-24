package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostAsmLoadRunStdlibRootVsFlags(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the driver takes host filesystem paths as argv; native-only")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "asm_load_run")

	src := "import \"core/map\";\n" +
		"function main(): i32 { return 1; }\n"
	prog := filepath.Join(dir, "flagprog.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}

	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"target-flag-is-not-a-root", []string{prog, "-target", "x86-64-linux"}},
		{"target-flag-is-not-a-root-arm64", []string{prog, "-target", "arm64-linux"}},
		{"root-after-a-flag-still-binds", []string{prog, "-target", "x86-64-linux", stdlibRoot}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, _ := cmd.Output()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("driver did not exit normally")
			}
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Fatalf("driver exited %d, want 0\nstderr: %s", code, stderr.String())
			}
			if len(out) == 0 {
				t.Fatal("driver emitted 0 bytes")
			}
			if got := stderr.String(); strings.Contains(got, "cannot read module") {
				t.Errorf("driver resolved a module under a flag value: %s", got)
			}
		})
	}
}
