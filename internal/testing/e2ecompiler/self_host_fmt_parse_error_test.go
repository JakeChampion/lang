package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fmtParseErrorCases each fail to parse, at the position `-check` names.
var fmtParseErrorCases = []struct{ name, src, where string }{
	{"missing-semicolon", "function main(): i32 {\n  let x: i32 = 1\n  return x;\n}\n", ":3:3: error[P001]"},
	{"missing-last-brace", "function main(): i32 {\n  return 0;\n", ":3:1: error[P001]"},
	// The printer read this `return @;` as a void `return;`, losing the `@`.
	{"stray-token", "function main(): i32 { return @; }\n", ":1:31: error[P001]"},
	{"lex-error", "function main(): i32 { let s = \"abc\n  return 0; }\n", ":1:32: error[P001]"},
	{"untyped-param", "function f(x): i32 { return 0; }\nfunction main(): i32 { return f(1); }\n", ":1:13: error[P001]"},
}

// TestSelfHostFmtRefusesParseErrors pins #12646: `-fmt` on a file that does
// not parse printed the program with a `/*unknown-stmt:…*/` marker, which is
// not Fern, and exited 0, and `-fmt -w` called the refusal a formatter bug.
// Every mode now reports `-check`'s parse errors, exits 1 and writes nothing.
func TestSelfHostFmtRefusesParseErrors(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	bin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	oracle := buildLangBinForInterp(t)

	for _, c := range fmtParseErrorCases {
		t.Run(c.name, func(t *testing.T) {
			tmp := t.TempDir()
			path := filepath.Join(tmp, "bad.fern")
			if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := exec.Command(oracle, "-fmt", path).Run(); err == nil {
				t.Fatal("oracle -fmt accepted the source — the fixture no longer fails to parse")
			}
			_, check, code := runFmtCapture(runX86_64Bin(runner, bin, "-check", path))
			if code != 1 || !strings.Contains(check, path+c.where) {
				t.Fatalf("-check exited %d, want 1 with %q:\n%s", code, c.where, check)
			}
			out := filepath.Join(tmp, "out.fern")
			for _, args := range [][]string{
				{"-fmt", path},
				{"-fmt", "-d", path},
				{"-fmt", "-w", path},
				{"-fmt", "-o", out, path},
			} {
				stdout, stderr, code := runFmtCapture(runX86_64Bin(runner, bin, args...))
				if code != 1 {
					t.Errorf("%v exited %d, want 1", args, code)
				}
				if stdout != "" {
					t.Errorf("%v wrote to stdout:\n%s", args, stdout)
				}
				if stderr != check {
					t.Errorf("%v reported\n%s\nwant -check's report\n%s", args, stderr, check)
				}
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != c.src {
				t.Errorf("-fmt -w changed the source: %q, %v", got, err)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("-fmt -o wrote %s (stat: %v)", out, err)
			}
		})
	}

	// A file list keeps going past a file that does not parse, and the exit
	// code is the worst one seen.
	t.Run("file-list", func(t *testing.T) {
		tmp := t.TempDir()
		bad := filepath.Join(tmp, "bad.fern")
		good := filepath.Join(tmp, "good.fern")
		if err := os.WriteFile(bad, []byte(fmtParseErrorCases[0].src), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(good, []byte("function main(): i32 {return 0;}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code := runFmtCapture(runX86_64Bin(runner, bin, "-fmt", bad, good))
		if code != 1 {
			t.Errorf("exited %d, want 1", code)
		}
		if stdout != "function main(): i32 {\n  return 0;\n}\n" {
			t.Errorf("the file that parses was not formatted:\n%s", stdout)
		}
		if !strings.Contains(stderr, bad+":3:3: error[P001]") {
			t.Errorf("the file that does not parse was not reported:\n%s", stderr)
		}
	})

	// A literate chunk body that does not parse is a fragment, kept as written.
	t.Run("literate-fragment", func(t *testing.T) {
		doc := "# Doc\n\n```fern\n<<*>>=\nfunction main(): i32 {\n  let x: i32 = 1\n  return x;\n}\n```\n"
		path := filepath.Join(t.TempDir(), "doc.fern.md")
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code := runFmtCapture(runX86_64Bin(runner, bin, "-fmt", path))
		if code != 0 || stdout != doc {
			t.Errorf("exited %d, want 0 and the document unchanged:\n%s\n%s", code, stdout, stderr)
		}
	})
}

// runFmtCapture runs cmd and returns its stdout, stderr and exit code.
func runFmtCapture(cmd *exec.Cmd) (string, string, int) {
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}
