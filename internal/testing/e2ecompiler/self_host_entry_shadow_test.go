package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// An entry-module function with the name of a builtin shadows it, as a
// function of an imported module does (#14244). The calls, the recursion and
// the lambda reach the program's own env.
const entryShadowSrc = `function env(x: i32): i32 {
  if (x > 0) {
    return env(x - 1) + 2;
  }
  return 1;
}

function main(): i32 {
  let f = (n: i32): i32 => env(n);
  let xs: i32[] = [f(3), env(2)];
  if (xs[0] != 7 || xs[1] != 5) {
    return 1;
  }
  print("ok " + xs.len().to_string());
  return 0;
}
`

func TestSelfHostEntryFunctionShadowsABuiltin(t *testing.T) {
	selfHostRunOnEveryTarget(t, entryShadowSrc)
}

func TestInterpEntryFunctionShadowsABuiltin(t *testing.T) {
	if out, code := e2eharness.RunInterp(t, entryShadowSrc); code != 0 || out != "ok 2\n" {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

// The shadow is applied where the program is bundled, so both checkers run
// through their command lines here, not through the codes harness.
func TestEntryShadowDiagnostics(t *testing.T) {
	h := selfHostCLIForHost(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	const env = "function env(x: i32): i32 { return x; }\n"
	cases := []struct {
		name, src, module, want string
	}{
		{"shadow", env + "function main(): i32 { return env(0); }\n", "", ""},
		{"call-names-the-function", env + "function main(): i32 { return env(\"a\"); }\n", "", `error[E038]: argument 1 to "env": expected i32, got string`},
		{"redeclared", env + env + "function main(): i32 { return env(0); }\n", "", `error[E006]: function "env" redeclared`},
		{"beside-a-module-env", "import \"./m\";\n" + env + "function main(): i32 { return env(m.env(1)); }\n", "pub function env(x: i32): i32 { return x + 1; }\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if c.module != "" {
				if err := os.WriteFile(filepath.Join(dir, "m.fern"), []byte(c.module), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, run := range [][]string{{h.cli, "-check", path, h.stdlib}, {oracle, "-check", path}} {
				out, err := exec.Command(run[0], run[1:]...).CombinedOutput()
				if c.want == "" && err != nil || c.want != "" && !strings.Contains(string(out), c.want) {
					t.Fatalf("%s -check: err=%v, want %q\n%s", filepath.Base(run[0]), err, c.want, out)
				}
			}
		})
	}
}
