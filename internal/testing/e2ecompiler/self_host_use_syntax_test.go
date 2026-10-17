package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// useSyntaxCases each hold one malformed `use` line. The self-host used to
// skip a missing `<-` silently and report the expression that followed as
// "use source must be a function call" at the end of the block (#12604).
var useSyntaxCases = []struct{ name, src string }{
	{"no-name", "function f(x: i32, k: (i32) => i32): i32 { return k(x); }\nfunction main(): i32 {\n  use <- f(1);\n  return 0;\n}\n"},
	{"two-names", "function f(x: i32, k: (i32) => i32): i32 { return k(x); }\nfunction main(): i32 {\n  use a, b <- f(1);\n  return a;\n}\n"},
	{"half-arrow", "function f(x: i32, k: (i32) => i32): i32 { return k(x); }\nfunction main(): i32 {\n  use n < f(1);\n  return n;\n}\n"},
	{"not-a-call", "function main(): i32 {\n  let g: i32 = 4;\n  use n <- g;\n  return n;\n}\n"},
}

// useDiagAt reads the position and text off either compiler's first error
// line; the self-host's text opens with the enclosing function.
var useDiagAt = regexp.MustCompile(`:(\d+):(\d+): error(?:\[P001\])?: (?:in fn '[^']*': )?(.*)`)

// TestSelfHostUseSyntaxDiagnosticsX86_64 is the differential for #12604: a
// malformed `use` is reported where native reports it, with native's text.
func TestSelfHostUseSyntaxDiagnosticsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	native := buildLangBinForInterp(t)
	for _, c := range useSyntaxCases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "prog.fern")
			if err := os.WriteFile(p, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			nout, _ := exec.Command(native, "-check", p).CombinedOutput()
			nm := useDiagAt.FindSubmatch(nout)
			if nm == nil {
				t.Fatalf("native reported no error for this program, so it cannot be the oracle:\n%s", nout)
			}
			var errb bytes.Buffer
			cmd := runX86_64Bin(runner, driver, "-check", p)
			cmd.Stderr = &errb
			if err := cmd.Run(); err == nil {
				t.Fatalf("self-host -check accepted a malformed use")
			}
			sm := useDiagAt.FindSubmatch(errb.Bytes())
			if sm == nil {
				t.Fatalf("self-host reported no positioned error:\n%s", errb.String())
			}
			if !bytes.Equal(sm[1], nm[1]) || !bytes.Equal(sm[2], nm[2]) || !bytes.Equal(sm[3], nm[3]) {
				t.Errorf("self-host %s:%s %q, want native's %s:%s %q", sm[1], sm[2], sm[3], nm[1], nm[2], nm[3])
			}
		})
	}
}
