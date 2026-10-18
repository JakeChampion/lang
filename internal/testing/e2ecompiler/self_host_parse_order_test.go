package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// diagLabel is the self-host's "at top level:" / "in fn '…':" label (#12762),
// which is not part of the words compared.
var diagLabel = regexp.MustCompile(`^(?:at top level: |in fn '[^']*': )`)

// A program with several parse errors reports the earliest first, from both
// front ends, in the same words at the same position (#12773). `-fmt` and the
// language server report through the same gates as `-check`, so they lead with
// the same finding.
func TestSelfHostFirstParseErrorInSourceOrder(t *testing.T) {
	h := selfHostCLIForHost(t)
	progs := []string{
		// A sentinel before a nameless function.
		"function main(): i32 { return @; }\nfunction use(): i32 { return 1; }",
		// A sentinel before an untyped parameter.
		"function main(): i32 { return @; }\nfunction f(x): i32 { return 0; }",
		// A sentinel in a later function, before a nameless one.
		"function main(): i32 { return 0; }\nfunction f(): i32 { return @; }\nfunction type(): i32 { return 1; }",
		// A malformed attribute, then the nameless struct its `struct` starts.
		"@struct\nstruct S { a: i32 }",
		// An unterminated string before a nameless function.
		"function main(): i32 { let s = \"abc;\n  return 0;\n}\nfunction use(): i32 { return 1; }",
	}
	dir := t.TempDir()
	for i, prog := range progs {
		src := filepath.Join(dir, fmt.Sprintf("multi%d.fern", i))
		if err := os.WriteFile(src, []byte(prog+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		nout, nerr := exec.Command(h.native, "-check", src).CombinedOutput()
		want := firstP001.FindStringSubmatch(string(nout))
		if nerr == nil || want == nil {
			t.Errorf("native on %q: want a P001, got (%v)\n%s", prog, nerr, nout)
			continue
		}
		for _, args := range [][]string{{"-check", src, h.stdlib}, {"-fmt", src}} {
			sout, serr := exec.Command(h.cli, args...).CombinedOutput()
			got := firstP001.FindStringSubmatch(string(sout))
			if serr == nil || got == nil || got[1] != want[1] || got[2] != want[2] {
				t.Errorf("self-host %s on %q: want %s %q first (native), got (%v)\n%s", args[0], prog, want[1], want[2], serr, sout)
			}
		}
		frames, _ := runLSP(t, exec.Command(h.cli, "-lsp", h.stdlib), session(lspOpen(fileURI(src), prog+"\n")))
		var first *lspDiag
		for _, p := range publishes(t, frames) {
			if p.URI == fileURI(src) && len(p.Diagnostics) > 0 {
				first = &p.Diagnostics[0]
			}
		}
		if first == nil {
			t.Errorf("language server on %q: no diagnostics published", prog)
			continue
		}
		pos := fmt.Sprintf("%d:%d", first.Range.Start.Line+1, first.Range.Start.Character+1)
		if first.Code != "P001" || pos != want[1] || diagLabel.ReplaceAllString(first.Message, "") != want[2] {
			t.Errorf("language server on %q: want %s %q first (native), got %s %s %q", prog, want[1], want[2], pos, first.Code, first.Message)
		}
	}

	// A sentinel at the top level of a module declaring `main` is one parse
	// error, not also a statement that never executes.
	src := filepath.Join(dir, "sentinel.fern")
	if err := os.WriteFile(src, []byte("@foo\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(h.cli, "-check", src, h.stdlib).CombinedOutput()
	if n := strings.Count(string(out), "error["); n != 1 {
		t.Errorf("-check on a top-level sentinel raised %d diagnostics, want 1:\n%s", n, out)
	}
}
