package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A local binding shadows an import's qualifier and a module-level function
// of its name, from the binding to the end of its scope, in both compilers.
// Before the binding, and outside its scope, the module is reached as usual.
func TestLocalShadowsImport(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, c := range []struct {
		name, main string
		want       int
	}{
		{"let", `function main(): i32 { let util: string = "abc"; return util.len(); }`, 3},
		{"init-before-binding", `function main(): i32 { let util: i32 = util.one() + 1; return util; }`, 2},
		{"param", `function f(util: string): i32 { return util.len(); }
function main(): i32 { return f("abcd") + util.one(); }`, 5},
		{"block-scope", `function main(): i32 {
  if (true) {
    let util: string = "ab";
    if (util.len() != 2) { return 9; }
  }
  return util.one();
}`, 1},
		{"lambda-param", `function main(): i32 {
  let f = (util: string): i32 => { return util.len(); };
  return f("xyz") + util.one();
}`, 4},
		{"loop-binder", `function main(): i32 {
  let n: i32 = 0;
  for util in ["a", "bb"] { n = n + util.len(); }
  return n + util.one();
}`, 4},
		// In an imported module a call to its own `two` resolves to the
		// function until a local of that name is bound.
		{"own-function-before-local", `function main(): i32 { return lib.run(); }`, 7},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"util.fern": "pub function one(): i32 { return 1; }\n",
				"lib.fern":  "function two(): i32 { return 2; }\npub function run(): i32 { let a: i32 = two(); let two: i32 = 5; return a + two; }\n",
				"main.fern": "import \"./util\";\nimport \"./lib\";\n" + c.main + "\n",
			}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			main := filepath.Join(dir, "main.fern")
			for leg, cmd := range map[string]*exec.Cmd{
				"self-host": runX86_64Bin(cli.runner, cli.bin, "-interp", main, cli.stdlib),
				"oracle":    exec.Command(oracle, "-interp", main),
			} {
				out, _ := cmd.CombinedOutput()
				if code := cmd.ProcessState.ExitCode(); code != c.want {
					t.Errorf("%s: exit %d, want %d\n%s", leg, code, c.want, out)
				}
			}
		})
	}
}
