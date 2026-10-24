package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/fmtsource"
	goprinter "github.com/jakechampion/lang/internal/syntax/printer"
)

const fmtLiterateDoc = "# Formatting a document\n" +
	"\n" +
	"function   prose( ):i32{return 1;}\n" +
	"<<greet>>\n" +
	"<<greet>>=\n" +
	"\n" +
	"```fern\n" +
	"<<greet>>=\n" +
	"// Greets.\n" +
	"function   greet( ):i32{return 7;}\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"<<setup>>=\n" +
	"let    x=1;\n" +
	"let y   =x+1;\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"<<*>>=\n" +
	"<<greet>>\n" +
	"<<helpers>>\n" +
	"function   main( ):i32{\n" +
	"  <<setup>>\n" +
	"  return y+greet()+twice(1);\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"More prose between chunks.\n" +
	"\n" +
	"```fern\n" +
	"<<helpers>>=\n" +
	"function twice(n:i32):i32{return n*2;}\n" +
	"```\n" +
	"\n" +
	"```fern file=lib.fern\n" +
	"pub function   lib( ):i32{return 2;}\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"function   shown( ){}\n" +
	"```\n" +
	"\n" +
	"```fern test\n" +
	"function   main( ):i32{return 0;}\n" +
	"```\n" +
	"\n" +
	"```sh\n" +
	"let    x=1;\n" +
	"```\n"

func TestSelfHostFmtLiterate(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	root := repoRootFromTest(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	bin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	docs := map[string]string{
		"export-attribute": "# Export\n\n```fern\n<<*>>=\n@export(\"fern:test/math\", \"add\")\npub function add(x:i32,y:i32):i32{return x+y;}\n```\n",
		"several-chunks":   fmtLiterateDoc,
		"unclosed-fence":   "intro\n\n```fern\n<<*>>=\nfunction   main( ):i32{return 0;}\n",
		"only-references":  "# Root\n\n```fern\n<<*>>=\n<<a>>\n```\n\n```fern\n<<a>>=\n```\n",
	}
	for _, rel := range []string{
		"examples/literate/doctest_demo.fern.md",
		"examples/literate/fizzbuzz.fern.md",
		"examples/literate/multi_module.fern.md",
	} {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		docs[rel] = string(src)
	}

	for name, src := range docs {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			want := fmtsource.FormatLiterate(src)
			path := filepath.Join(t.TempDir(), "doc.fern.md")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}

			out, err := runX86_64Bin(runner, bin, "-fmt", path).Output()
			if err != nil {
				t.Fatalf("-fmt: %v", err)
			}
			if string(out) != want {
				t.Fatalf("-fmt differs from the Go formatter\n%s", firstDiffLines("go", want, "self-host", string(out)))
			}

			wantDiff := goprinter.UnifiedDiff(src, want, path, path)
			cmd := runX86_64Bin(runner, bin, "-fmt", "-d", path)
			out, _ = cmd.Output()
			wantCode := 0
			if wantDiff != "" {
				wantCode = 1
			}
			if code := cmd.ProcessState.ExitCode(); code != wantCode {
				t.Errorf("-fmt -d exited %d, want %d", code, wantCode)
			}
			if string(out) != wantDiff {
				t.Errorf("-fmt -d differs from the Go diff\n%s", firstDiffLines("go", wantDiff, "self-host", string(out)))
			}

			if out, err := runX86_64Bin(runner, bin, "-fmt", "-w", path).CombinedOutput(); err != nil {
				t.Fatalf("-fmt -w: %v\n%s", err, out)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Fatalf("-fmt -w wrote a different document\n%s", firstDiffLines("go", want, "self-host", string(got)))
			}

			cmd = runX86_64Bin(runner, bin, "-fmt", "-d", path)
			out, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != 0 || len(out) != 0 {
				t.Errorf("formatted document is not a fixed point (exit %d):\n%s", code, out)
			}
		})
	}
	t.Run("keeps-prose", func(t *testing.T) { checkLiterateKeepsProse(t, runner, bin) })
}

func checkLiterateKeepsProse(t *testing.T, runner []string, bin string) {
	path := filepath.Join(t.TempDir(), "doc.fern.md")
	if err := os.WriteFile(path, []byte(fmtLiterateDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(runner, bin, "-fmt", path).Output()
	if err != nil {
		t.Fatalf("-fmt: %v", err)
	}
	got := string(out)
	for _, kept := range []string{
		"# Formatting a document\n\nfunction   prose( ):i32{return 1;}\n<<greet>>\n<<greet>>=\n\n```fern\n",
		"<<*>>=\n<<greet>>\n<<helpers>>\nfunction   main( ):i32{\n  <<setup>>\n",
		"\nMore prose between chunks.\n",
		"```fern\nfunction   shown( ){}\n```\n",
		"```fern test\nfunction   main( ):i32{return 0;}\n```\n",
		"```sh\nlet    x=1;\n```\n",
	} {
		if !strings.Contains(got, kept) {
			t.Errorf("-fmt changed text it must keep: %q\n%s", kept, got)
		}
	}
	for _, formatted := range []string{
		"<<greet>>=\n// Greets.\nfunction greet(): i32 {\n  return 7;\n}\n```",
		"<<setup>>=\nlet x = 1;\nlet y = x + 1;\n```",
		"<<helpers>>=\nfunction twice(n: i32): i32 {\n  return n * 2;\n}\n```",
		"```fern file=lib.fern\npub function lib(): i32 {\n  return 2;\n}\n```",
	} {
		if !strings.Contains(got, formatted) {
			t.Errorf("-fmt did not format a chunk: want %q\n%s", formatted, got)
		}
	}
}
