package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostImportExtensionX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, c := range []struct {
		name  string
		files map[string]string
	}{
		{"relative", map[string]string{
			"main.fern": "import \"./two.fern\";\nfunction main(): i32 { print(two.f()); return 0; }\n",
			"two.fern":  "pub function f(): i32 { return 2; }\n",
		}},
		{"bare", map[string]string{
			"main.fern": "import \"two.fern\";\nfunction main(): i32 { print(two.f()); return 0; }\n",
			"two.fern":  "pub function f(): i32 { return 2; }\n",
		}},
		{"transitive", map[string]string{
			"main.fern":    "import \"./lib/mid.fern\";\nfunction main(): i32 { print(mid.f()); return 0; }\n",
			"lib/mid.fern": "import \"../two.fern\";\npub function f(): i32 { return two.f(); }\n",
			"two.fern":     "pub function f(): i32 { return 2; }\n",
		}},
		{"same-module", map[string]string{
			"main.fern": "import \"./two\" as short;\nimport \"./two.fern\" as full;\nfunction main(): i32 { print(short.f() + full.f()); return 0; }\n",
			"two.fern":  "pub function f(): i32 { return 1; }\n",
		}},
		{"literate", map[string]string{
			"main.fern":   "import \"./two.fern\";\nfunction main(): i32 { print(two.f()); return 0; }\n",
			"two.fern.md": "```fern\n<<*>>=\npub function f(): i32 { return 2; }\n```\n",
		}},
		{"dependency-submodule", map[string]string{
			"fern.toml":     "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"./pkg\" }\n",
			"main.fern":     "import \"helper/two.fern\";\nfunction main(): i32 { print(two.f()); return 0; }\n",
			"pkg/fern.toml": "[package]\nname = \"helper\"\n",
			"pkg/two.fern":  "pub function f(): i32 { return 2; }\n",
		}},
		{"stdlib", map[string]string{
			"main.fern": "import \"std/i64.fern\";\nfunction main(): i32 { let n: i64 = 6; print(n.gcd(4)); return 0; }\n",
		}},
		{"stdlib-array", map[string]string{
			"main.fern": "import \"std/array.fern\" as array;\nfunction twice(n: i32): i32 { return n * 2; }\nfunction main(): i32 { print(array.map([1], twice)[0]); return 0; }\n",
		}},
		{"stdlib-provenance", map[string]string{
			"main.fern": "import \"std/io.fern\";\nfunction main(): i32 { match (read_file(\"two.txt\")) { Ok(s) => { print(s); }, Err(e) => { return 1; } } return 0; }\n",
			"two.txt":   "2",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.files["main.fern"] = "import \"std/i32\";\n" + c.files["main.fern"]
			writeResolveProject(t, dir, c.files)
			main := filepath.Join(dir, "main.fern")
			for name, cmd := range map[string]*exec.Cmd{
				"oracle":    exec.Command(oracle, "-interp", main),
				"self-host": runX86_64Bin(cli.runner, cli.bin, "-interp", main, cli.stdlib),
			} {
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil || string(out) != "2\n" {
					t.Errorf("%s: %v, want stdout 2\n%s", name, err, out)
				}
			}
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					bin := filepath.Join(t.TempDir(), "prog")
					build := cli.compileCmd(target, bin, main)
					build.Dir = dir
					if out, err := build.CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, out)
					}
					cmd := runX86_64Bin(cli.runner, bin)
					if target == "wasm32-wasi" {
						cmd = exec.Command(e2eharness.Wasmtime(t), "run", "--dir=.", bin)
					}
					cmd.Dir = dir
					if out, err := cmd.CombinedOutput(); err != nil || string(out) != "2\n" {
						t.Errorf("run: %v, want stdout 2\n%s", err, out)
					}
				})
			}
		})
	}
}
