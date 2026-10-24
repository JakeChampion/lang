package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostSemanticHandleTty(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("handle-tty test runs only natively (drives a host terminal)")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfHostHandleTtySource), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "prog")
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", out, src, stdlibRoot)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR_REPORT=1")
	var report strings.Builder
	cmd.Stderr = &report
	if err := cmd.Run(); err != nil {
		t.Fatalf("compile: %v\n%s", err, report.String())
	}
	if produced, total := semTally(t, report.String()); produced != total {
		t.Fatalf("the typed path produced %d of %d declarations:\n%s", produced, total, report.String())
	}
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatal(err)
	}
	handleTtyOnPath(t, exec.Command(out))
}
