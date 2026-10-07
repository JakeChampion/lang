package e2ecompiler

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostLiterateLoadDifferentialX86_64 is the parity gate for the
// compiler loading literate `.fern.md` documents (#11838): as the entry, both
// single-root and `file=` multi-module, and as an imported library, a
// dependency's lib included. Native is the oracle. When native's -check accepts
// a program, the self-host builds it and the binary must print and exit as
// native's -interp does. When native refuses it, the self-host's -check and
// build must refuse it with the same diagnostic lines, positions on the
// document's own lines included (native's indented source snippets aside).
func TestSelfHostLiterateLoadDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("literate-load differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	examples, err := filepath.Glob("../../../examples/literate/*.fern.md")
	if err != nil || len(examples) == 0 {
		t.Fatalf("no examples/literate documents found (%v)", err)
	}
	for _, ex := range examples {
		abs, err := filepath.Abs(ex)
		if err != nil {
			t.Fatal(err)
		}
		t.Run("example/"+filepath.Base(ex), func(t *testing.T) {
			compareLiterateLoad(t, nativeBin, driverBin, filepath.Dir(abs), abs)
		})
	}

	twoModules := func(sq string) string {
		return "# Two modules\n\n```fern file=main.fern entry\nimport \"./mathx\";\nfunction main(): i32 { return mathx.sq(3); }\n```\n\n" +
			"```fern file=mathx.fern\n<<sq>>\n```\n\n```fern\n<<sq>>=\npub function sq(n: i32): i32 {\n    " + sq + "\n}\n```\n"
	}
	for _, c := range []struct {
		name  string
		files map[string]string
		entry string
	}{
		// A single-root document imported from plain Fern.
		{"import-library", map[string]string{
			"mathlib.fern.md": "# Math\n\n```fern\n<<*>>=\npub function triple(x: i32): i32 { return x * 3; }\n```\n",
			"main.fern":       "import \"./mathlib\";\nfunction main(): i32 { return mathlib.triple(5); }\n",
		}, "main.fern"},
		// A plain `.fern` wins over a `.fern.md` of the same name.
		{"plain-fern-wins", map[string]string{
			"m.fern":    "pub function v(): i32 { return 1; }\n",
			"m.fern.md": "```fern\n<<*>>=\npub function v(): i32 { return 2; }\n```\n",
			"main.fern": "import \"./m\";\nfunction main(): i32 { return m.v(); }\n",
		}, "main.fern"},
		// A dependency whose lib module is a literate document.
		{"dependency-lib", map[string]string{
			"app/fern.toml":      "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n",
			"app/main.fern":      "import \"helper\";\nfunction main(): i32 { return helper.nine(); }\n",
			"helper/fern.toml":   "[package]\nname = \"helper\"\n",
			"helper/lib.fern.md": "```fern\n<<*>>=\npub function nine(): i32 {\n    <<body>>\n}\n```\n\n```fern\n<<body>>=\nreturn 9;\n```\n",
		}, "app/main.fern"},
		// A multi-module document whose entry imports a module it tangles to
		// AND a library on disk beside it.
		{"multi-file-and-disk-import", map[string]string{
			"doc.fern.md": "```fern file=main.fern entry\nimport \"./a\";\nimport \"./disk\";\nfunction main(): i32 { return a.v() + disk.w(); }\n```\n\n" +
				"```fern file=a.fern\npub function v(): i32 { return 30; }\n```\n",
			"disk.fern": "pub function w(): i32 { return 4; }\n",
		}, "doc.fern.md"},
		// The entry is the unique module declaring main when none is marked.
		{"multi-file-entry-by-main", map[string]string{
			"doc.fern.md": "```fern file=lib.fern\npub function h(): i32 { return 6; }\n```\n\n" +
				"```fern file=app.fern\nimport \"./lib\";\nfunction main(): i32 { return lib.h(); }\n```\n",
		}, "doc.fern.md"},
		// A type error in a chunk expanded under an indented reference: the
		// position is the chunk's own document line and column.
		{"entry-type-error", map[string]string{
			"bad.fern.md": "# Bad\n\nProse.\n\n```fern\n<<*>>=\nfunction main(): i32 {\n    <<body>>\n}\n```\n\nMore.\n\n```fern\n<<body>>=\nlet x: i32 = \"nope\";\nreturn x;\n```\n",
		}, "bad.fern.md"},
		{"import-library-type-error", map[string]string{
			"lib.fern.md": "# Lib\n\n```fern\n<<*>>=\npub function f(): i32 {\n    <<b>>\n}\n```\n\n```fern\n<<b>>=\nreturn true;\n```\n",
			"main.fern":   "import \"./lib\";\nfunction main(): i32 { return lib.f(); }\n",
		}, "main.fern"},
		// An error in a generated module that is not the entry.
		{"multi-file-type-error", map[string]string{
			"doc.fern.md": twoModules("return true;"),
		}, "doc.fern.md"},
		{"entry-undefined-chunk", map[string]string{
			"doc.fern.md": "# Undefined\n\n```fern\n<<*>>=\nfunction main(): i32 {\n    <<nope>>\n}\n```\n",
		}, "doc.fern.md"},
		{"entry-cyclic-chunk", map[string]string{
			"doc.fern.md": "```fern\n<<*>>=\n<<a>>\n```\n\n```fern\n<<a>>=\n<<b>>\n```\n\n```fern\n<<b>>=\n  <<a>>\n```\n",
		}, "doc.fern.md"},
		{"multi-file-two-marked", map[string]string{
			"doc.fern.md": "```fern file=a.fern entry\npub function h() {}\n```\n\n```fern file=b.fern entry\npub function g() {}\n```\n",
		}, "doc.fern.md"},
		{"multi-file-none-marked", map[string]string{
			"doc.fern.md": "```fern file=a.fern\npub function h() {}\n```\n\n```fern file=b.fern\npub function g() {}\n```\n",
		}, "doc.fern.md"},
		// An unused chunk is warned about before the document is refused.
		{"unused-chunk-warning", map[string]string{
			"doc.fern.md": "```fern\n<<zz>>=\nx\n```\n\n```fern\n<<aa>>=\ny\n```\n",
		}, "doc.fern.md"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeResolveProject(t, root, c.files)
			compareLiterateLoad(t, nativeBin, driverBin, root, filepath.Join(root, filepath.FromSlash(c.entry)))
		})
	}

	// An imported library that does not tangle, or tangles to several modules,
	// is refused naming the document — and, for a tangle error, the document
	// position native reports.
	t.Run("import-refusals", func(t *testing.T) {
		root := t.TempDir()
		writeResolveProject(t, root, map[string]string{
			"tlib.fern.md":  "# L\n\n```fern\n<<*>>=\npub function f(): i32 {\n    <<nope>>\n}\n```\n",
			"tmain.fern":    "import \"./tlib\";\nfunction main(): i32 { return tlib.f(); }\n",
			"multi.fern.md": "```fern file=a.fern\npub function a(): i32 { return 1; }\n```\n",
			"mmain.fern":    "import \"./multi\";\nfunction main(): i32 { return 0; }\n",
		})
		for _, c := range []struct{ entry, want string }{
			{"tmain.fern", filepath.Join(root, "tlib.fern.md") + ":6:5: literate: reference to undefined chunk \"<<nope>>\""},
			{"mmain.fern", filepath.Join(root, "multi.fern.md") + ": a multi-file literate document tangles to several modules"},
		} {
			entry := filepath.Join(root, c.entry)
			if out, err := exec.Command(nativeBin, "-check", entry).CombinedOutput(); err == nil {
				t.Fatalf("native accepted %s:\n%s", c.entry, out)
			}
			out, err := exec.Command(driverBin, "-check", entry).CombinedOutput()
			if err == nil || !strings.Contains(string(out), c.want) {
				t.Errorf("self-host -check %s: err=%v, want output containing %q, got:\n%s", c.entry, err, c.want, out)
			}
		}
	})
}

// compareLiterateLoad holds the self-host to native on one entry, run in `dir`.
func compareLiterateLoad(t *testing.T, nativeBin, driverBin, dir, entry string) {
	t.Helper()
	check := exec.Command(nativeBin, "-check", entry)
	check.Dir = dir
	nativeOut, nativeErr := check.CombinedOutput()
	if nativeErr == nil {
		interp := exec.Command(nativeBin, "-interp", entry)
		interp.Dir = dir
		var want bytes.Buffer
		interp.Stdout = &want
		_ = interp.Run()
		prog := filepath.Join(t.TempDir(), "prog")
		build := exec.Command(driverBin, "-target", "x86-64-linux", "-o", prog, entry)
		build.Dir = dir
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("self-host build failed where native accepted: %v\n%s", err, out)
		}
		run := exec.Command(prog)
		var got bytes.Buffer
		run.Stdout = &got
		_ = run.Run()
		if got.String() != want.String() || run.ProcessState.ExitCode() != interp.ProcessState.ExitCode() {
			t.Errorf("binary printed %q and exited %d; native -interp printed %q and exited %d",
				got.String(), run.ProcessState.ExitCode(), want.String(), interp.ProcessState.ExitCode())
		}
		return
	}
	want := diagnosticLines(nativeOut)
	if len(want) == 0 {
		t.Fatalf("native refused with no diagnostic:\n%s", nativeOut)
	}
	for _, args := range [][]string{
		{"-check", entry},
		{"-target", "x86-64-linux", "-o", filepath.Join(t.TempDir(), "prog"), entry},
	} {
		cmd := exec.Command(driverBin, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("self-host %s accepted what native refused:\n%s", args[0], nativeOut)
			continue
		}
		if got := diagnosticLines(out); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("self-host %s reported\n%s\nnative -check reported\n%s", args[0], strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

// diagnosticLines is a run's output without native's indented source-snippet
// lines.
func diagnosticLines(out []byte) []string {
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if l != "" && !strings.HasPrefix(l, " ") {
			lines = append(lines, l)
		}
	}
	return lines
}
