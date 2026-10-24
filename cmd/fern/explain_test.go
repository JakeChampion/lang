package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunExplain(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runExplain("", &out, &errOut); code != 0 || !strings.Contains(out.String(), "E048") {
		t.Errorf("runExplain(\"\") = %d, stdout %q; want 0 and a list naming E048", code, out.String())
	}
	out.Reset()
	if code := runExplain("E048", &out, &errOut); code != 0 || !strings.Contains(out.String(), "E048") {
		t.Errorf("runExplain(E048) = %d, stdout %q", code, out.String())
	}
	out.Reset()
	if code := runExplain("E999999", &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "unknown error code") {
		t.Errorf("runExplain(E999999) = %d, stderr %q; want 1 and an unknown-code message", code, errOut.String())
	}
}

func TestExplainCommandLine(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "fern")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}
	if out, code := run("-explain"); code != 0 || !strings.HasPrefix(out, "E001\n") || !strings.Contains(out, "\nE003\n") {
		t.Errorf("-explain: exit %d, output %q; want 0 and the list of codes", code, out)
	}
	for _, args := range [][]string{{"-explain", "e001"}, {"-explain=E001"}, {"--explain", "E001"}} {
		out, code := run(args...)
		if code != 0 || !strings.HasPrefix(out, "error E001: Undefined identifier\n\n") || strings.Contains(out, "# E001") {
			t.Errorf("%q: exit %d, output %q; want 0 and the heading once, as the header", args, code, out)
		}
	}
	if out, code := run("-explain", "E001", "E002"); code != 2 || !strings.Contains(out, "takes one error code") {
		t.Errorf("-explain E001 E002: exit %d, output %q; want the usage refusal 2", code, out)
	}
}
