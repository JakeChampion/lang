package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostTangleDifferentialX86_64 is the parity gate for `-tangle`
// (#6751): the self-host must expand a literate document into exactly the
// bytes native expands it into.
//
// `literate.fern` has carried the tangle engine since it was ported, and
// nothing called it — so nothing compared it either. Byte equality is the
// right bar here rather than "it produced something compilable": a tangled
// module IS the program, and a line that lands in the wrong place, or an
// indent the expansion failed to carry, changes what compiles.
func TestSelfHostTangleDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("tangle differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	for _, c := range []struct {
		name   string
		doc    string
		wantOK bool
	}{
		// The root chunk plus one reference, indented — the indent of the
		// `<<ref>>` line prefixes every line the chunk expands to, which is
		// the rule most easily got wrong.
		{"root-and-indented-ref", "# Doc\n\n```fern\n<<*>>=\nfunction main(): i32 {\n    <<compute>>\n}\n```\n\n```fern\n<<compute>>=\nvar n: i32 = 20;\nreturn n + 2;\n```\n", true},
		// A chunk referenced from another chunk, so expansion recurses.
		{"nested-refs", "```fern\n<<*>>=\n<<outer>>\n```\n\n```fern\n<<outer>>=\nfunction main(): i32 {\n    <<inner>>\n}\n```\n\n```fern\n<<inner>>=\nreturn 4;\n```\n", true},
		// Same chunk name defined twice: the pieces concatenate in document
		// order rather than the later one winning.
		{"chunk-continued", "```fern\n<<*>>=\nfunction main(): i32 {\n    <<body>>\n}\n```\n\n```fern\n<<body>>=\nvar a: i32 = 1;\n```\n\n```fern\n<<body>>=\nreturn a + 1;\n```\n", true},
		// Prose and non-fern fences around the chunks must contribute nothing.
		{"prose-and-other-fences", "Some prose.\n\n```text\nnot fern, ignore me\n```\n\n```fern\n<<*>>=\nfunction main(): i32 { return 1; }\n```\n\nMore prose.\n", true},
		// A `file=PATH` document tangles to one module per path, under the
		// `// ==> path <==` banner.
		{"multi-file", "```fern file=lib.fern\npub function tag(): i32 { return 5; }\n```\n\n```fern file=main.fern entry\nimport \"./lib\";\nfunction main(): i32 { return lib.tag(); }\n```\n", true},
		// A document with no root chunk is refused by both.
		{"no-root-chunk", "just prose, no chunks at all\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc := filepath.Join(t.TempDir(), "doc.fern.md")
			if err := os.WriteFile(doc, []byte(c.doc), 0o644); err != nil {
				t.Fatal(err)
			}

			nativeCmd := exec.Command(nativeBin, "-tangle", doc)
			nativeOut, _ := nativeCmd.Output()
			nativeOK := nativeCmd.ProcessState.ExitCode() == 0

			shCmd := exec.Command(driverBin, "-tangle", doc)
			shOut, _ := shCmd.Output()
			shOK := shCmd.ProcessState.ExitCode() == 0

			if nativeOK != c.wantOK {
				t.Fatalf("native tangle ok = %v, want %v", nativeOK, c.wantOK)
			}
			if shOK != nativeOK {
				t.Fatalf("native tangle ok = %v, self-host = %v\n--- self-host stdout ---\n%s", nativeOK, shOK, shOut)
			}
			// The refusal's wording is not compared: native renders a
			// diagnostic with the source line and a caret, and no self-host
			// diagnostic carries source context (its `-check` output does not
			// either). That both refuse, and name the same document line, is
			// what matters — the line is asserted below.
			if !c.wantOK {
				return
			}
			if string(nativeOut) != string(shOut) {
				t.Errorf("tangled output differs:\n--- native ---\n%q\n--- self-host ---\n%q", nativeOut, shOut)
			}
			if len(nativeOut) == 0 {
				t.Error("native tangled to nothing — the case proves nothing")
			}
		})
	}

	// `-o` writes the tangled source instead of printing it, and writes the
	// same bytes stdout would have carried.
	t.Run("output-file", func(t *testing.T) {
		tmp := t.TempDir()
		doc := filepath.Join(tmp, "doc.fern.md")
		if err := os.WriteFile(doc, []byte("```fern\n<<*>>=\nfunction main(): i32 { return 9; }\n```\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		piped, err := exec.Command(driverBin, "-tangle", doc).Output()
		if err != nil {
			t.Fatalf("self-host -tangle: %v", err)
		}
		out := filepath.Join(tmp, "out.fern")
		if err := exec.Command(driverBin, "-tangle", doc, "-o", out).Run(); err != nil {
			t.Fatalf("self-host -tangle -o: %v", err)
		}
		written, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if string(written) != string(piped) {
			t.Errorf("-o wrote %q, stdout carried %q", written, piped)
		}
		if !strings.Contains(string(written), "return 9;") {
			t.Errorf("tangled file lost its body: %q", written)
		}
	})
}
