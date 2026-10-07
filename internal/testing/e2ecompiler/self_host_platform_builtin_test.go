package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostPlatformBuiltinLowers pins the std/serve half of #5686.
//
// Every std/serve entry point names the host platform
// (`(HttpRequest, platform.Host) => HttpResponse`), so the self-host must
// resolve that type for `__serve_loop` to lower.
//
// The probe is the assertion: every function of the std/serve closure must lower,
// with the serve loop named explicitly so a regression says which one broke.
func TestSelfHostPlatformBuiltinLowers(t *testing.T) {
	_, runner, driverBin := buildModloadDriverX86(t)

	progDir := t.TempDir()
	// Keep nested modules such as std/tls/client in their real import paths.
	// Flattening only top-level files drops serve's transitive dependencies.
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

// probeLineFor returns the eligibility-report line for `fn`, or a not-found
// marker — the report lists one `name: verdict` line per function.
func probeLineFor(report, fn string) string {
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, fn+":") {
			return line
		}
	}
	return "(no line for " + fn + ")"
}
