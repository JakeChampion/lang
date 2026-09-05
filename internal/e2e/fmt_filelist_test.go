package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/printer"
)

// TestFmtFileList pins the native half of the gofmt-shaped `-fmt FILE...`
// contract the self-host CLI mirrors (TestSelfHostFmtWriteAndDiffViaInterp's
// file-list case): stdout mode concatenates in argument order, `-d` prints one
// diff per differing file and exits 1 when any does, `-w` writes every file, a
// file that cannot be read does not stop the rest but does set the exit code,
// and `-o` — one output name — is refused with more than one input.
func TestFmtFileList(t *testing.T) {
	bin := buildLangBinForInterp(t)
	const unformatted = "function add(a: i32,b: i32): i32 {\nreturn a+b;\n}\n"
	prog, err := parser.Parse(unformatted)
	if err != nil {
		t.Fatal(err)
	}
	want := printer.Format(prog)

	dir := t.TempDir()
	messy := filepath.Join(dir, "messy.fern")
	clean := filepath.Join(dir, "clean.fern")
	messy2 := filepath.Join(dir, "messy2.fern")
	for _, f := range []struct{ path, src string }{{messy, unformatted}, {clean, want}, {messy2, unformatted}} {
		if err := os.WriteFile(f.path, []byte(f.src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	if out, code := run("-fmt", messy, clean, messy2); code != 0 || out != want+want+want {
		t.Errorf("-fmt over three files: exit %d, out:\n%s", code, out)
	}
	wantDiff := printer.UnifiedDiff(unformatted, want, messy, messy) + printer.UnifiedDiff(unformatted, want, messy2, messy2)
	if out, code := run("-fmt", "-d", messy, clean, messy2); code != 1 || out != wantDiff {
		t.Errorf("-fmt -d over three files: exit %d (want 1), out:\n%s", code, out)
	}
	if out, code := run("-fmt", "-o", filepath.Join(dir, "out.fern"), messy, clean); code != 2 || out != "" {
		t.Errorf("-fmt -o with two inputs: exit %d (want 2), out %q", code, out)
	}
	// The missing file is reported and skipped; the rest is still formatted.
	if out, code := run("-fmt", filepath.Join(dir, "nope.fern"), clean); code != 1 || out != want {
		t.Errorf("-fmt with a missing file first: exit %d (want 1), out:\n%s", code, out)
	}
	if out, code := run("-fmt", "-w", messy, clean, messy2); code != 0 || out != "" {
		t.Errorf("-fmt -w over three files: exit %d, out %q", code, out)
	}
	for _, p := range []string{messy, clean, messy2} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("-fmt -w left %s as:\n%s", filepath.Base(p), got)
		}
	}
	if _, code := run("-fmt", "-d", messy, clean, messy2); code != 0 {
		t.Errorf("-fmt -d after -w exited %d, want 0", code)
	}
}
