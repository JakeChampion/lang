package e2ecompiler

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/stdlib"
	"github.com/jakechampion/lang/internal/tools/literate"
)

func TestSelfHostStdlibErrorTraits(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	decl := regexp.MustCompile(`(?m)^pub (?:enum|struct) (\w*(?:Error|Err))\b`)
	var imports, probes strings.Builder
	count := 0
	err := filepath.WalkDir(cli.stdlib, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || stdlib.ModuleName(path) == "" {
			return nil
		}
		src, err := literate.ReadFile(path)
		if err != nil {
			return err
		}
		types := decl.FindAllStringSubmatch(string(src), -1)
		if len(types) == 0 {
			return nil
		}
		rel, err := filepath.Rel(cli.stdlib, path)
		if err != nil {
			return err
		}
		alias := fmt.Sprintf("m%d", count)
		fmt.Fprintf(&imports, "import %q as %s;\n", stdlib.ModuleName(filepath.ToSlash(rel)), alias)
		for _, typ := range types {
			fmt.Fprintf(&probes, "function probe%d(e: %s.%s): dyn error.Error { let text: string = display(e); return e; }\n", count, alias, typ[1])
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("no public error types found")
	}
	t.Logf("check %d public error types", count)
	src := "import \"std/error\";\nimport \"core/cmp\";\n" + imports.String() +
		"function display[T: cmp.Display](e: T): string { return e.to_string(); }\n" + probes.String() +
		"function main(): i32 { return 0; }\n"
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, cmd := range map[string]*exec.Cmd{
		"oracle":    exec.Command(oracle, "-check", path),
		"self-host": runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib),
	} {
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s error contracts: %v\n%s", name, err, out)
		}
	}
}
