package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var fmtParseErrorCases = []struct{ name, src, where string }{
	{"missing-semicolon", "function main(): i32 {\n  let x: i32 = 1\n  return x;\n}\n", ":3:3: error[P001]"},
	{"missing-last-brace", "function main(): i32 {\n  return 0;\n", ":3:1: error[P001]"},
	{"stray-token", "function main(): i32 { return @; }\n", ":1:31: error[P001]"},
	{"lex-error", "function main(): i32 { let s = \"abc\n  return 0; }\n", ":1:32: error[P001]"},
	{"untyped-param", "function f(x): i32 { return 0; }\nfunction main(): i32 { return f(1); }\n", ":1:13: error[P001]"},
}

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
				want := 1
				if args[1] == "-d" {
					want = 2
				}
				if code != want {
					t.Errorf("%v exited %d, want %d", args, code, want)
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

	t.Run("diff-exit-codes", func(t *testing.T) {
		tmp := t.TempDir()
		bad := filepath.Join(tmp, "bad.fern")
		messy := filepath.Join(tmp, "messy.fern")
		missing := filepath.Join(tmp, "missing.fern")
		if err := os.WriteFile(bad, []byte(fmtParseErrorCases[0].src), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(messy, []byte("function main(): i32 {return 0;}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			args []string
			want int
		}{
			{[]string{"-fmt", "-d", messy}, 1},
			{[]string{"-fmt", "-d", messy, bad}, 2},
			{[]string{"-fmt", "-d", bad, messy}, 2},
			{[]string{"-fmt", "-d", missing}, 2},
			{[]string{"-fmt", missing}, 1},
		} {
			stdout, stderr, code := runFmtCapture(runX86_64Bin(runner, bin, c.args...))
			if code != c.want {
				t.Errorf("%v exited %d, want %d:\n%s", c.args, code, c.want, stderr)
			}
			if len(c.args) == 4 && !strings.HasPrefix(stdout, "--- "+messy+"\n") {
				t.Errorf("%v did not print the diff of the file that parses:\n%s", c.args, stdout)
			}
		}
	})

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

func runFmtCapture(cmd *exec.Cmd) (string, string, int) {
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}
