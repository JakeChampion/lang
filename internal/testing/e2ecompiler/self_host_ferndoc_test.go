package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/tools/literate"
)

func TestSelfHostFerndocSelfChecks(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ferndoc_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ferndoc_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ferndoc_run.fern", "ferndoc_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("ferndoc_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("ferndoc_run exit code = %d, want 0 (the code is the failing case's id)\n%s", code, out)
	}
	if want := "ferndoc: association agrees"; !strings.Contains(string(out), want) {
		t.Errorf("ferndoc_run stdout = %q, want it to contain %q", out, want)
	}
}

func TestSelfHostFernDocPage(t *testing.T) {
	fernBin := buildFernDocCLI(t)
	want, err := os.ReadFile(filepath.Join("testdata", "ferndoc", "shapes.md"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(fernBin, "-doc", filepath.Join("testdata", "ferndoc", "std", "shapes.fern")).Output()
	if err != nil {
		t.Fatalf("fern -doc: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("page differs from testdata/ferndoc/shapes.md\n%s", firstPageDiff(string(want), string(got)))
	}
}

func TestSelfHostFernDocMode(t *testing.T) {
	if testing.Short() {
		t.Skip("stdlib sweep is slow; skipped under -short")
	}
	fernBin := buildFernDocCLI(t)
	outDir := t.TempDir()
	stdlib := filepath.Join(langSrcAbs(t, "internal"), "stdlib")
	for _, root := range []string{"std", "core"} {
		cmd := exec.Command(fernBin, "-doc", "-o", outDir, filepath.Join(stdlib, root))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("fern -doc %s: %v\n%s", root, err, stderr.String())
		}
	}
	pages := pageSet(t, outDir)
	want := stdlibPageNames(t, stdlib)
	for name := range want {
		if _, ok := pages[name]; !ok {
			t.Errorf("fern -doc wrote no %s", name)
		}
	}
	for name := range pages {
		if !want[name] {
			t.Errorf("fern -doc wrote %s, which no stdlib module names", name)
		}
	}
	for name, page := range pages {
		if !bytes.HasPrefix(page, []byte("---\ntitle: ")) {
			t.Errorf("%s has no front matter", name)
		}
	}

	one := exec.Command(fernBin, "-doc", filepath.Join(stdlib, "std", "io.fern.md"))
	got, err := one.Output()
	if err != nil {
		t.Fatalf("fern -doc <file>: %v", err)
	}
	if want := pages["io.md"]; !bytes.Equal(got, want) {
		t.Errorf("fern -doc std/io.fern differs from the walk's io.md\n%s", firstPageDiff(string(want), string(got)))
	}

	noOut := exec.Command(fernBin, "-doc", filepath.Join(stdlib, "std"))
	out, _ := noOut.Output()
	if code := noOut.ProcessState.ExitCode(); code != 2 {
		t.Errorf("fern -doc <DIR> with no -o: exit = %d, want 2", code)
	}
	if len(out) != 0 {
		t.Errorf("fern -doc <DIR> with no -o wrote %d bytes to stdout, want none", len(out))
	}
}

func buildFernDocCLI(t *testing.T) string {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the -doc mode takes host filesystem paths as argv; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	return buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
}

func stdlibPageNames(t *testing.T, stdlib string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	from := map[string]string{}
	for _, ns := range []string{"std", "core"} {
		root := filepath.Join(stdlib, ns)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".fern") || strings.HasSuffix(path, ".fern.md")) || strings.HasPrefix(d.Name(), "_test_") {
				return err
			}
			src, rerr := literate.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			if !documentsAnything(t, path, string(src)) {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			name := strings.ReplaceAll(strings.TrimSuffix(strings.TrimSuffix(rel, ".md"), ".fern"), string(filepath.Separator), "_") + ".md"
			mod := ns + "/" + filepath.ToSlash(rel)
			if prev, ok := from[name]; ok {
				t.Errorf("%s and %s both want the page %s", prev, mod, name)
			}
			from[name] = mod
			out[name] = true
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	return out
}

func documentsAnything(t *testing.T, path, src string) bool {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if len(prog.Impls) > 0 {
		return true
	}
	for _, fd := range prog.Funcs {
		if fd.Public && !strings.HasPrefix(fd.Name, "__") {
			return true
		}
	}
	for _, sd := range prog.Structs {
		if sd.Public {
			return true
		}
	}
	for _, ed := range prog.Enums {
		if ed.Public {
			return true
		}
	}
	for _, cd := range prog.Consts {
		if cd.Public {
			return true
		}
	}
	for _, td := range prog.Traits {
		if td.Public {
			return true
		}
	}
	return false
}

func pageSet(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			t.Fatalf("reading %s: %v", e.Name(), rerr)
		}
		out[e.Name()] = b
	}
	return out
}

func firstPageDiff(want, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("  line %d\n  --- want --- %q\n  --- got  --- %q", i+1, wl, gl)
		}
	}
	return "  (identical line-wise; trailing bytes differ)"
}
