package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostGrantAttenuationDiagnosticPositions(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\na = { path = \"../a\", capabilities = [] }\n",
		"app/main.fern": "import \"a\";\nfunction main(): i32 { return a.one(); }\n",
		"a/fern.toml":   "[package]\nname = \"a\"\nlib = \"lib.fern\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"random\", \"fs\"] }\nc = { path = \"../c\", capabilities = [\"time\"] }\n",
		"a/lib.fern":    "import \"b\";\nimport \"c\";\npub function one(): i32 { return b.one() + c.one(); }\n",
		"b/fern.toml":   "[package]\nname = \"b\"\n",
		"b/lib.fern":    "pub function one(): i32 { return 1; }\n",
		"c/fern.toml":   "[package]\nname = \"c\"\n",
		"c/lib.fern":    "pub function one(): i32 { return 2; }\n",
	})
	for _, frontend := range []struct{ name, bin string }{{"Go", oracle}, {"self", cli.bin}} {
		t.Run(frontend.name, func(t *testing.T) {
			cmd := exec.Command(frontend.bin, "-q", "-check", filepath.Join(dir, "app/main.fern"), cli.stdlib)
			out, err := cmd.CombinedOutput()
			text := string(out)
			path := filepath.Join(dir, "a/fern.toml")
			if err == nil || strings.Count(text, "error[E088]") != 3 || strings.Count(text, path+":5:1: error[E088]:") != 2 || strings.Count(text, path+":6:1: error[E088]:") != 1 {
				t.Fatalf("want three manifest diagnostics at the dependency entries: %v\n%s", err, text)
			}
		})
	}
}
