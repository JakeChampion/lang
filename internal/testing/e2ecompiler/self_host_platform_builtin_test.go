package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostPlatformBuiltinLowers(t *testing.T) {
	_, runner, driverBin := buildModloadDriverX86(t)

	progDir := t.TempDir()
	copyStdlibTree(t, progDir)
	bsrc, err := os.ReadFile("../../../compiler/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	main := "import \"std/serve\";\nfunction main(): i32 { return 0; }\n"
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"), []byte(main), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}

	report := string(runDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern"), "-ir-probe"))
	if !strings.Contains(report, "serve____serve_loop: ir") {
		t.Errorf("std/serve's __serve_loop did not lower; probe line: %q", probeLineFor(report, "serve____serve_loop"))
	}
	if probeLineFor(report, "module") != "module: IR" {
		t.Errorf("std/serve's import closure did not lower:\n%s", report)
	}
}

func probeLineFor(report, fn string) string {
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, fn+":") {
			return line
		}
	}
	return "(no line for " + fn + ")"
}
