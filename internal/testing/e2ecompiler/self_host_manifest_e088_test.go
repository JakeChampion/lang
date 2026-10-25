package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostManifestErrorIsE088(t *testing.T) {
	const badDep = "[package]\nname = \"app\"\n[dependencies]\nfoo = { path = \"../foo\", capabilities = [\"nett\"] }\n"
	const msg = `error[E088]: dependency "foo": capabilities: unknown capability "nett" (valid capabilities: env, fs, net, random, subprocess, time)`
	for _, c := range []struct {
		name  string
		files map[string]string
		bad   string
		line  string
		quote string
	}{
		{"entry", map[string]string{
			"fern.toml": badDep,
			"main.fern": "function main(): i32 { return 0; }\n",
		}, "fern.toml", ":4:1: " + msg, `foo = { path = "../foo", capabilities = ["nett"] }`},
		{"imported module", map[string]string{
			"main.fern":     "import \"./sub/lib\";\nfunction main(): i32 { return lib.one(); }\n",
			"sub/fern.toml": "[dependencies]\n",
			"sub/lib.fern":  "pub function one(): i32 { return 1; }\n",
		}, "sub/fern.toml", ":1:1: error[E088]: missing [package] name", "[dependencies]"},
		{"grant beyond what the package holds", map[string]string{
			"fern.toml":   "[package]\nname = \"app\"\n[dependencies]\na = { path = \"a\", capabilities = [\"fs\"] }\n",
			"main.fern":   "import \"a\";\nfunction main(): i32 { return a.one(); }\n",
			"a/fern.toml": "[package]\nname = \"a\"\n[dependencies]\nb = { path = \"../b\", capabilities = [\"env\"] }\n",
			"a/lib.fern":  "import \"b\";\npub function one(): i32 { return b.one(); }\n",
			"b/fern.toml": "[package]\nname = \"b\"\n",
			"b/lib.fern":  "pub function one(): i32 { return 1; }\n",
		}, "a/fern.toml", `:4:1: error[E088]: dependency "b" of "a" is granted 'env' but "a" itself holds only [fs] (attenuation: a dependency may grant at most what it holds)`, `b = { path = "../b", capabilities = ["env"] }`},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range c.files {
				p := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want := filepath.Join(dir, c.bad) + c.line
			for _, fe := range []struct {
				name string
				cmd  *exec.Cmd
			}{
				{"self-host", exec.Command(e2eharness.SelfHostCLI(t), "-check", "main.fern", e2eharness.SelfHostStdlibRoot(t))},
				{"oracle", exec.Command(buildFernCLIBin(t), "-check", "main.fern")},
			} {
				fe.cmd.Dir = dir
				out, _ := fe.cmd.CombinedOutput()
				if fe.cmd.ProcessState.ExitCode() == 0 || !strings.Contains(string(out), want) {
					t.Errorf("%s: -check exit = %d, want a refusal containing %q\n%s", fe.name, fe.cmd.ProcessState.ExitCode(), want, out)
				}
				if fe.name == "self-host" && !strings.Contains(string(out), c.quote) {
					t.Errorf("self-host: the refusal does not quote the manifest line %q\n%s", c.quote, out)
				}
			}
		})
	}
}
