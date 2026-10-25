package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func literateRun(t *testing.T, doc string, args ...string) (string, string, string, int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.fern.md")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	args = append(args, path)
	if args[0] == "-doctest" {
		args = append(args, e2eharness.SelfHostStdlibRoot(t))
	}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	return dir, stdout.String(), strings.ReplaceAll(stderr.String(), path, "DOC"), cmd.ProcessState.ExitCode()
}

func TestSelfHostTangle(t *testing.T) {
	for _, c := range []struct{ name, doc, want string }{
		{"root-and-indented-ref", "# Doc\n\n```fern\n<<*>>=\nfunction main(): i32 {\n    <<compute>>\n}\n```\n\n```fern\n<<compute>>=\nlet n: i32 = 20;\nreturn n + 2;\n```\n",
			"function main(): i32 {\n    let n: i32 = 20;\n    return n + 2;\n}\n"},
		{"nested-refs", "```fern\n<<*>>=\n<<outer>>\n```\n\n```fern\n<<outer>>=\nfunction main(): i32 {\n    <<inner>>\n}\n```\n\n```fern\n<<inner>>=\nreturn 4;\n```\n",
			"function main(): i32 {\n    return 4;\n}\n"},
		{"chunk-continued", "```fern\n<<*>>=\nfunction main(): i32 {\n    <<body>>\n}\n```\n\n```fern\n<<body>>=\nlet a: i32 = 1;\n```\n\n```fern\n<<body>>=\nreturn a + 1;\n```\n",
			"function main(): i32 {\n    let a: i32 = 1;\n    return a + 1;\n}\n"},
		{"prose-and-other-fences", "Some prose.\n\n```text\nnot fern, ignore me\n```\n\n```fern\n<<*>>=\nfunction main(): i32 { return 1; }\n```\n\nMore prose.\n",
			"function main(): i32 { return 1; }\n"},
		{"multi-file", "```fern file=lib.fern\npub function tag(): i32 { return 5; }\n```\n\n```fern file=main.fern entry\nimport \"./lib\";\nfunction main(): i32 { return lib.tag(); }\n```\n",
			"// ==> lib.fern <==\npub function tag(): i32 { return 5; }\n\n// ==> main.fern <==\nimport \"./lib\";\nfunction main(): i32 { return lib.tag(); }\n"},
		{"escaped-marker", "```fern\n<<*>>=\nfunction main(): i32 {\n    <<body>>\n}\n```\n\n```fern\n<<body>>=\n  \\<<not a ref>>\nreturn 0;\n```\n",
			"function main(): i32 {\n      <<not a ref>>\n    return 0;\n}\n"},
		{"fence-forms", "```FERN\n<<*>>=\nok();\n```\n```fern test\n<<*>>=\nbad();\n```\n",
			"ok();\n"},
		{"quoted-file", "```fern file=\"q.fern\"\nx\n```\n",
			"// ==> q.fern <==\nx\n"},
		{"unclosed-fence", "```fern\n<<*>>=\nfn main() {}\n",
			"fn main() {}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, out, errOut, code := literateRun(t, c.doc, "-tangle")
			if code != 0 || out != c.want {
				t.Errorf("exit %d\n--- got ---\n%q\n--- want ---\n%q\n%s", code, out, c.want, errOut)
			}
		})
	}

	t.Run("refusal-shows-the-line", func(t *testing.T) {
		_, _, errOut, code := literateRun(t, "```fern\n<<*>>=\n  <<nope>>\n```\n", "-tangle")
		want := "DOC:3:3: error[E092]: literate: reference to undefined chunk \"<<nope>>\"\n      <<nope>>\n      ^\n"
		if code != 1 || errOut != want {
			t.Errorf("exit %d, stderr\n%q\nwant\n%q", code, errOut, want)
		}
	})

	t.Run("unused-chunk-warning", func(t *testing.T) {
		_, out, errOut, code := literateRun(t, "```fern\n<<*>>=\nx\n```\n```fern\n<<spare>>=\ny\n```\n", "-tangle")
		if code != 0 || out != "x\n" || errOut != "DOC:6:1: warning[W003]: chunk <<spare>> is defined but never used\n" {
			t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("chunk", func(t *testing.T) {
		doc := "```fern\n<<*>>=\nfunction main(): i32 {\n    <<compute>>\n}\n```\n\n```fern\n<<compute>>=\nlet n: i32 = 20;\nreturn n + 2;\n```\n"
		if _, out, errOut, code := literateRun(t, doc, "-tangle", "-chunk", "compute"); code != 0 || out != "let n: i32 = 20;\nreturn n + 2;\n" {
			t.Errorf("exit %d, stdout %q\n%s", code, out, errOut)
		}
		if _, _, errOut, code := literateRun(t, doc, "-tangle", "-chunk", "nope"); code != 1 || !strings.Contains(errOut, `no chunk "<<nope>>" defined`) {
			t.Errorf("undefined chunk: exit %d, stderr %q", code, errOut)
		}
	})

	t.Run("output", func(t *testing.T) {
		doc := "```fern file=sub/lib.fern\npub function tag(): i32 { return 5; }\n```\n\n```fern file=main.fern entry\nimport \"./sub/lib\";\nfunction main(): i32 { return lib.tag(); }\n```\n"
		dir := t.TempDir()
		out := filepath.Join(dir, "out")
		if _, _, errOut, code := literateRun(t, doc, "-tangle", "-o", out); code != 0 {
			t.Fatalf("exit %d\n%s", code, errOut)
		}
		for name, want := range map[string]string{
			"main.fern":                      "import \"./sub/lib\";\nfunction main(): i32 { return lib.tag(); }",
			filepath.Join("sub", "lib.fern"): "pub function tag(): i32 { return 5; }",
		} {
			got, err := os.ReadFile(filepath.Join(out, name))
			if err != nil || string(got) != want {
				t.Errorf("%s = %q (%v), want %q", name, got, err, want)
			}
		}
		file := filepath.Join(dir, "one.fern")
		if _, _, errOut, code := literateRun(t, "```fern\n<<*>>=\nfunction main(): i32 { return 9; }\n```\n", "-tangle", "-o", file); code != 0 || errOut != "wrote "+file+"\n" {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		if got, _ := os.ReadFile(file); string(got) != "function main(): i32 { return 9; }\n" {
			t.Errorf("-o wrote %q", got)
		}
	})
}

func weaveGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "weave", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSelfHostWeave(t *testing.T) {
	doc := weaveGolden(t, "doc.fern.md")
	for _, c := range []struct {
		golden string
		args   []string
	}{
		{"doc.md", []string{"-weave"}},
		{"doc.html", []string{"-weave", "-html"}},
	} {
		t.Run(c.golden, func(t *testing.T) {
			_, out, errOut, code := literateRun(t, doc, c.args...)
			if code != 0 || errOut != "DOC:51:1: warning[W003]: chunk <<café>> is defined but never used\n" {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if want := weaveGolden(t, c.golden); out != want {
				t.Errorf("output differs from testdata/weave/%s:\n%s", c.golden, firstDifference(out, want))
			}
		})
	}

	t.Run("trivial", func(t *testing.T) {
		one := "# Only\n```fern\n<<*>>=\nfn main() {}\n```\n"
		if _, out, _, _ := literateRun(t, one, "-weave"); strings.Contains(out, "## Chunk index") {
			t.Errorf("a one-chunk document got an index:\n%s", out)
		}
		if _, out, _, _ := literateRun(t, one, "-weave", "-html"); strings.Contains(out, `<nav class="toc">`) {
			t.Errorf("a one-heading document got a table of contents")
		}
		if _, out, _, _ := literateRun(t, "just prose\n", "-weave", "-html"); strings.Contains(out, `<section class="chunk-index">`) || !strings.Contains(out, "<title>Fern literate document</title>") {
			t.Errorf("a document without chunks:\n%s", out)
		}
	})

	t.Run("output-file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "doc.md")
		if _, out, errOut, code := literateRun(t, doc, "-weave", "-o", file); code != 0 || out != "" || !strings.HasSuffix(errOut, "wrote "+file+"\n") {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
		if got, _ := os.ReadFile(file); string(got) != weaveGolden(t, "doc.md") {
			t.Errorf("-o wrote something else than stdout carries:\n%s", got)
		}
	})
}

func firstDifference(got, want string) string {
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) && i < len(wl); i++ {
		if gl[i] != wl[i] {
			return "line " + strconv.Itoa(i+1) + ":\n  got:  " + gl[i] + "\n  want: " + wl[i]
		}
	}
	return "one is a prefix of the other: got " + strconv.Itoa(len(gl)) + " lines, want " + strconv.Itoa(len(wl))
}

func TestSelfHostDoctest(t *testing.T) {
	t.Run("pass-and-fail", func(t *testing.T) {
		doc := "# d\n```fern\n<<greet>>=\npub function greet(): string { return \"hi\"; }\n```\n" +
			"```fern test name=ok-case\n<<greet>>\nfunction main(): i32 { if (greet() != \"hi\") { return 1; } return 0; }\n```\n" +
			"```fern test name=fail-case\n<<greet>>\nfunction main(): i32 { return 1; }\n```\n" +
			"```fern test\nfunction main(): i32 { return 0; }\n```\n"
		_, out, _, code := literateRun(t, doc, "-doctest")
		want := "1..3\nok 1 - ok-case\nnot ok 2 - fail-case\n# example failed: main returned 1 (expected 0)\nok 3 - doctest 3 (line 14)\n"
		if code != 1 || out != want {
			t.Errorf("exit %d\n--- got ---\n%s--- want ---\n%s", code, out, want)
		}
	})

	t.Run("diagnostic-on-the-document", func(t *testing.T) {
		_, out, _, code := literateRun(t, "# t\n```fern test\nfunction main(): i32 { return \"nope\"; }\n```\n", "-doctest")
		if code != 1 || !strings.Contains(out, "doc.fern.md:3:24: error[E002]: return value: expected i32, got string\n#     function main(): i32 { return \"nope\"; }\n#                            ^\n") {
			t.Errorf("exit %d\n%s", code, out)
		}
	})

	t.Run("relative-import", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sub", "lib.fern"), []byte("pub function seven(): i32 { return 7; }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		doc := filepath.Join(dir, "doc.fern.md")
		if err := os.WriteFile(doc, []byte("```fern test name=rel\nimport \"./sub/lib\";\nfunction main(): i32 { if (lib.seven() != 7) { return 1; } return 0; }\n```\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(e2eharness.SelfHostCLI(t), "-doctest", doc, e2eharness.SelfHostStdlibRoot(t)).CombinedOutput()
		if err != nil || string(out) != "1..1\nok 1 - rel\n" {
			t.Errorf("%v\n%s", err, out)
		}
	})

	t.Run("no-examples", func(t *testing.T) {
		if _, out, errOut, code := literateRun(t, "# none\n", "-doctest"); code != 0 || out != "1..0\n" || errOut != "DOC: no `test` blocks found\n" {
			t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("undefined-chunk", func(t *testing.T) {
		if _, out, errOut, code := literateRun(t, "```fern test\n<<nope>>\n```\n", "-doctest"); code != 1 || out != "" || !strings.HasPrefix(errOut, "DOC:2:1: error[E092]: literate: reference to undefined chunk \"<<nope>>\"\n") {
			t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
}

func TestShippedFernLiterateTakesOneDocument(t *testing.T) {
	fern := e2eharness.ShippedFern(t)
	for _, mode := range []string{"-tangle", "-weave", "-doctest"} {
		cmd := exec.Command(fern, mode, "a.fern.md", "b.fern.md")
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 2 || string(out) != "fern: one document expected, got \"a.fern.md\" and \"b.fern.md\"\n" {
			t.Errorf("%s: exit %d\n%s", mode, code, out)
		}
	}
}
