package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostPlatformBuiltinLowers pins the `Platform` half of #5686.
//
// `Platform` is a builtin struct: native declares it in the checker's builtin
// struct table (the capability bag threaded as every handler's second
// parameter). The self-host's builtins.fern — which exists precisely so the
// self-host reads those declarations from real source instead of drifting —
// never declared it. So any signature mentioning it, which is EVERY std/tcp
// serve entry point (`(HttpRequest, Platform) => HttpResponse`), failed to
// resolve and `__serve_loop` reported `BAIL lower`. One bailing function drops
// the whole module to the AST emitter, which cannot emit `tcp_listen` / `poll`
// at all — so the failure surfaced far from its cause, as `undefined reference
// to __fn_tcp_listen` at link.
//
// The probe is the assertion: every function of the std/tcp closure must lower,
// with the serve loop named explicitly so a regression says which one broke.
func TestSelfHostPlatformBuiltinLowers(t *testing.T) {
	_, runner, driverBin := buildModloadDriverX86(t)

	progDir := t.TempDir()
	for _, dir := range []string{"../../internal/stdlib/std", "../../internal/stdlib/core"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".fern") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			// core/ and std/ share no basenames today; a collision would make
			// the flat vendoring silently drop a module, so fail loudly.
			dst := filepath.Join(progDir, e.Name())
			if _, err := os.Stat(dst); err == nil {
				continue
			}
			if err := os.WriteFile(dst, src, 0o644); err != nil {
				t.Fatalf("write %s: %v", e.Name(), err)
			}
		}
	}
	bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	main := "import \"std/tcp\";\nfunction main(): i32 { return 0; }\n"
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"), []byte(main), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}

	report := string(runDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern"), "-ir-probe"))
	if !strings.Contains(report, "tcp____serve_loop: ir") {
		t.Errorf("std/tcp's __serve_loop did not lower; probe line: %q", probeLineFor(report, "tcp____serve_loop"))
	}
	var bails []string
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(line, "BAIL") {
			bails = append(bails, line)
		}
	}
	if len(bails) > 0 {
		t.Errorf("%d function(s) of the std/tcp closure bail the IR path:\n%s", len(bails), strings.Join(bails, "\n"))
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
