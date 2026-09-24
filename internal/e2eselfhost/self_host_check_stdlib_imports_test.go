package e2eselfhost

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSelfHostCheckAcceptsEveryStdlibImport runs `-check` on a program that
// only imports one stdlib module, for every module. The self-host marks a
// module ill-typed when any statement in it types unknown, and it checks
// every bundled stdlib body, so one inference gap in a module failed `-check`
// for every program importing it: `buf_new` in std/array's `join` did that to
// anything importing std/string (#10132).
func TestSelfHostCheckAcceptsEveryStdlibImport(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	var driver string
	var runner []string
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		driver = buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	} else {
		var gcc string
		gcc, runner = x86_64Tooling(t)
		driver = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	}
	native := buildLangBinForInterp(t)
	root, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	var mods []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".fern") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		mods = append(mods, strings.TrimSuffix(filepath.ToSlash(rel), ".fern"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) < 50 {
		t.Fatalf("found %d stdlib modules under %s — the walk is not reading the stdlib", len(mods), root)
	}
	for _, mod := range mods {
		t.Run(mod, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(mod, "/", "_")+".fern")
			src := "import \"" + mod + "\";\nfunction main(): i32 { return 0; }\n"
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(native, "-check", path).CombinedOutput(); err != nil {
				t.Fatalf("native checker rejected the import: %v\n%s", err, out)
			}
			cmd := runX86_64Bin(runner, driver)
			cmd.Args = append(cmd.Args, "-check", path, root)
			out, err := cmd.CombinedOutput()
			if err != nil || len(out) != 0 {
				t.Errorf("self-host -check: %v\n%s", err, out)
			}
		})
	}
}
