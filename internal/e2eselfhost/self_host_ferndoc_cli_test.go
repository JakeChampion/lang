package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostFerndocCLIMatchesNative drives `ferndoc_run -doc` over a real
// stdlib tree and compares the DIRECTORY it produces with `cmd/ferndoc -out`'s.
//
// TestSelfHostFerndocPagesMatchNative already compares page bytes, but it
// hands the renderer one module's source on stdin and tells it what to call
// it. Everything between "here is a stdlib root" and "here are the pages" was
// therefore unexercised, and that is where cmd/ferndoc's actual contract
// lives —
//
//   - which files are modules (`*.fern`, minus the `_test_` stubs),
//   - that both `std/` and `core/` are documented, since both are import roots,
//   - that the walk DESCENDS: `std/wasm/` holds eleven modules the stdin
//     differential never saw, because its glob is flat,
//   - that a nested module's page flattens to `wasm_convert.md` rather than
//     colliding with top-level `convert.md`,
//   - that a module whose page renders empty is SKIPPED rather than written.
//
// A generator that renders every page correctly and writes the wrong set of
// them is still wrong, and nothing could see it before this.
func TestSelfHostFerndocCLIMatchesNative(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a self-host driver and runs cmd/ferndoc; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ferndoc_run.fern")
	docBin := buildSelfHostBin(t, gcc, dir, "ferndoc_run.fern", "ferndoc_run")

	// The stdlib the self-host reads has to be a real directory tree — the
	// point of the mode is that it enumerates one. cmd/ferndoc reads the same
	// sources through go:embed, so pointing both at internal/stdlib is what
	// makes the comparison meaningful.
	stdRoot := filepath.Join(langSrcAbs(t, "internal"), "stdlib")

	selfOut := t.TempDir()
	cmd := exec.Command(docBin, "-doc", "-o", selfOut, stdRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ferndoc_run -doc failed: %v\n%s", err, out)
	}

	nativeOut := t.TempDir()
	gen := exec.Command("go", "run", "./../../cmd/ferndoc", "-out", nativeOut)
	gen.Dir = "."
	var genErr bytes.Buffer
	gen.Stderr = &genErr
	if err := gen.Run(); err != nil {
		t.Fatalf("running cmd/ferndoc: %v\n%s", err, genErr.String())
	}

	// The page SET first. A missing page and a spurious one are different
	// bugs — one is an enumeration or skip-rule gap, the other a module
	// native filters out and the self-host does not — so they are reported
	// separately rather than as a diff.
	//
	// A module listed as diverging is allowed to be MISSING rather than
	// merely different: `convert` and `error` have no public surface beyond
	// their traits, so where native writes a page of traits the self-host
	// renders an empty one and skips it. That is the same "no TraitDecl" gap
	// the byte differential lists, expressed as an absent file.
	self := pageNames(t, selfOut)
	native := pageNames(t, nativeOut)
	for name := range native {
		if _, mayBeAbsent := ferndocPageDivergences[name]; !self[name] && !mayBeAbsent {
			t.Errorf("cmd/ferndoc wrote %s.md and `ferndoc_run -doc` did not", name)
		}
	}
	for name := range self {
		if !native[name] {
			t.Errorf("`ferndoc_run -doc` wrote %s.md and cmd/ferndoc did not", name)
		}
	}
	if len(native) < 45 {
		t.Fatalf("cmd/ferndoc wrote %d pages, expected the full stdlib — a shrunken comparison proves nothing", len(native))
	}

	var matched int
	for name := range native {
		want, err := os.ReadFile(filepath.Join(nativeOut, name+".md"))
		if err != nil {
			t.Fatalf("reading native page %s: %v", name, err)
		}
		if why, expected := ferndocPageDivergences[name]; expected {
			if !self[name] {
				continue // renders empty, so no file — see above
			}
			got, err := os.ReadFile(filepath.Join(selfOut, name+".md"))
			if err != nil {
				t.Fatalf("reading self-host page %s: %v", name, err)
			}
			if bytes.Equal(got, want) {
				t.Errorf("%s now matches native byte-for-byte, but is listed as diverging (%q) — delete its ferndocPageDivergences entry", name, why)
			}
			continue
		}
		if !self[name] {
			continue // already reported as a missing page above
		}
		got, err := os.ReadFile(filepath.Join(selfOut, name+".md"))
		if err != nil {
			t.Fatalf("reading self-host page %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: page written by `ferndoc_run -doc` differs from cmd/ferndoc\n%s", name, firstPageDiff(string(want), string(got)))
			continue
		}
		matched++
	}
	if matched < 60 {
		t.Errorf("only %d pages written byte-identically (of %d, %d listed as diverging) — the gate has gone hollow",
			matched, len(native), len(ferndocPageDivergences))
	}
	t.Logf("ferndoc_run -doc: %d pages byte-identical with cmd/ferndoc, %d listed as diverging", matched, len(ferndocPageDivergences))
}

// pageNames is the set of Markdown page basenames in a generated doc dir.
func pageNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			out[strings.TrimSuffix(e.Name(), ".md")] = true
		}
	}
	return out
}
