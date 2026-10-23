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
		"function use(): i32 { return 1; }",
		"function (): i32 { return 1; }",
		"struct match { x: i32 }",
		"struct 123 { x: i32 }",
		"enum match { A, B }",
		"trait use { function conv(self: Self): i32; }",
		"trait 123 { }",
		"type match = i32;",
		"const use: i32 = 1;",
		"function f(x): i32 { return 0; }",
		"function f(x",
		"function f(x, y: i32): i32 { return 0; }",
		"function main(): i32 { let (1, b) = (1, 2); return b; }",
		"function main(): i32 { let ((1, b), c) = ((1, 2), 3); return c; }",
		"function f((1, b): (i32, i32)): i32 { return b; }",
		"enum O { A(i32), B }\nfunction f(A(v): O): i32 { return v; }",
		"enum O { A(i32), B }\nfunction f(O.A(v): O): i32 { return v; }",
		"function f(: i32 {\n  return 1;\n}\n",
		"function (x: i32) use(): i32 { return 0; }",
		"trait Conv { function conv(self): i32; }",
		"trait Conv { function conv(self): i32 { return 0; } }",
		"@import(\"demo:pkg\", \"funcs\") function f(x): i32;",
		"trait Conv { function use(): i32; }",
		"function outer(): i32 { function use(): i32 { return 0; } return 0; }",
		"function outer(): i32 { function f(x): i32 { return 0; } return 0; }",
		"function use(): i32 { return 1; }\nfunction main(): i32 { return @; }",
		"function f(x): i32 { return 0; }\nfunction main(): i32 { return @; }",
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

func TestSelfHostMalformedDeclarationsAllFindings(t *testing.T) {
	h := selfHostCLIForHost(t)
	programs := []string{
		"function main(): i32 { return @; }\nfunction use(): i32 { return 1; }\n",
		"function main(): i32 { return @; }\nfunction f(x): i32 { return 0; }\n",
		"function f(x): i32 { return 0; }\nfunction use(): i32 { return 1; }\n",
		"function main(): i32 { return @; }\ntrait Conv { function conv(self): i32 { return 0; } }\nstruct A { }\nimpl Conv for A { }\n",
	}
	findings := func(out []byte) []string {
		var result []string
		for _, m := range nativeDiagPos.FindAllStringSubmatch(string(out), -1) {
			result = append(result, m[1]+":"+m[2]+" "+m[3]+" "+diagLabel.ReplaceAllString(m[4], ""))
		}
		return result
	}
	for i, source := range programs {
		src := writeTemp(t, t.TempDir(), fmt.Sprintf("all%d.fern", i), []byte(source))
		native, err := exec.Command(h.native, "-check", src).CombinedOutput()
		want := findings(native)
		if err == nil || len(want) < 2 {
			t.Fatalf("oracle must report at least two parse errors:\n%s", native)
		}
		for _, args := range [][]string{{"-check", src, h.stdlib}, {"-fmt", src}} {
			out, err := exec.Command(h.cli, args...).CombinedOutput()
			got := findings(out)
			if err == nil || strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("%s: want %q, got %q:\n%s", args[0], want, got, out)
			}
		}
	}
}
