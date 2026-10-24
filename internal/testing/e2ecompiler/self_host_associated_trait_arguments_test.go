package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Associated calls preserve trait arguments in parameter and result types.
func TestSelfHostAssociatedTraitArguments(t *testing.T) {
	const factory = `trait Factory[A, B] { function make(value: A): Result[Self, B]; }
struct Product { value: i32 }
struct Problem { value: i32 }
impl Factory[i32, Problem] for Product {
  function make(value: i32): Result[Product, Problem] { return Err(Problem { value: value }); }
}
`
	const echo = `trait Echo[A] { function echo(value: A): A; }
struct Marker {}
impl Echo[string] for Marker { function echo(value: string): string { return value; } }
`
	cases := []struct {
		name, source, code string
	}{
		{"concrete-result", factory + `function main(): i32 {
  let Err(problem) = Product.make(-7) else { return 1; };
  if (problem.value != -7) { return 2; } return 0;
}`, ""},
		{"bounded-result", factory + `function run[T: Factory[i32, Problem]](witness: T): i32 {
  let Err(problem) = T.make(-7) else { return 1; };
  if (problem.value != -7) { return 2; } return 0;
}
function main(): i32 { return run(Product { value: 0 }); }`, ""},
		{"bound-result", echo + `function run[T: Echo[string]](witness: T): i32 {
  if (T.echo("hello").len() != 5) { return 1; } return 0;
}
function main(): i32 { if (Marker.echo("hi").len() != 2) { return 2; } return run(Marker {}); }`, ""},
		{"imported-trait", `import "std/convert" as convert;
import "std/error";
import "core/cmp";
struct Product { value: i32 }
struct Problem { value: i32 }
impl error.Error for Problem {}
impl cmp.Display for Problem {}
function (e: Problem) message(): string { return "range error"; }
function (e: Problem) to_string(): string { return e.message(); }
impl convert.TryFrom[i32, Problem] for Product {
  function try_from(value: i32): Result[Product, Problem] { return Err(Problem { value: value }); }
}
function run[T: convert.TryFrom[i32, Problem]](witness: T): i32 {
  let Err(problem) = T.try_from(-7) else { return 1; };
  if (problem.value != -7) { return 2; } return 0;
}
function main(): i32 {
  let Err(problem) = Product.try_from(-7) else { return 1; };
  if (problem.value != -7) { return 2; }
  return run(Product { value: 0 });
}`, ""},
		{"concrete-argument", factory + `function main(): i32 { let result = Product.make("wrong"); return 0; }`, "E038"},
		{"bound-argument", echo + `function bad[T: Echo[string]](witness: T): string { return T.echo(7); }
function main(): i32 { return 0; }`, "E038"},
	}
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			for name, cmd := range map[string]*exec.Cmd{
				"Go":   exec.Command(oracle, "-check", path),
				"self": runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib),
			} {
				out, err := cmd.CombinedOutput()
				if tc.code == "" {
					if err != nil {
						t.Fatalf("%s check: %v\n%s", name, err, out)
					}
				} else if err == nil || !strings.Contains(string(out), "["+tc.code+"]") {
					t.Errorf("%s check: %v, want %s\n%s", name, err, tc.code, out)
				}
			}
			if tc.code != "" {
				return
			}
			for name, cmd := range map[string]*exec.Cmd{
				"Go":   exec.Command(oracle, "-interp", path),
				"self": runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib),
			} {
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("%s interpreter: %v\n%s", name, err, out)
				}
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, tc.source, target); code != 0 {
					t.Errorf("%s exit %d\n%s", target, code, stderr)
				}
			}
		})
	}
}
