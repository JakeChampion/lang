package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostGenericSupertraits(t *testing.T) {
	const echo = `trait Parent[A] { function echo(value: A): A; }
trait Child[U]: Parent[U] {}
struct Marker {}
impl Parent[string] for Marker { function echo(value: string): string { return value; } }
impl Child[string] for Marker {}
`
	const receiver = `trait Parent[A] { function echo(self: Self, value: A): A; }
trait Child[U]: Parent[U] {}
struct Marker {}
impl Parent[string] for Marker { function echo(self: Self, value: string): string { return value; } }
impl Child[string] for Marker {}
`
	cases := []struct{ name, source, code string }{
		{"associated", echo + `function run[T: Child[string]](witness: T): i32 { if (T.echo("hello").len() != 5) { return 1; } return 0; }
function main(): i32 { return run(Marker {}); }`, ""},
		{"receiver", receiver + `function run[T: Child[string]](witness: T): i32 { if (witness.echo("hello").len() != 5) { return 1; } return 0; }
function main(): i32 { return run(Marker {}); }`, ""},
		{"transitive-reordered", `trait Parent[A, B] { function convert(value: A): B; }
trait Middle[U, V]: Parent[V, U] {}
trait Child[X]: Middle[X, string] {}
struct Marker {}
impl Parent[string, i32] for Marker { function convert(value: string): i32 { return value.len(); } }
impl Middle[i32, string] for Marker {}
impl Child[i32] for Marker {}
function run[T: Child[i32]](witness: T): i32 { if (T.convert("hello") != 5) { return 1; } return 0; }
function main(): i32 { return run(Marker {}); }`, ""},
		{"nested-result", `trait Parent[A, B] { function make(value: A): Result[Self, B]; }
trait Child[U]: Parent[i32, Option[U]] {}
struct Product {}
struct Problem { value: i32 }
impl Parent[i32, Option[Problem]] for Product { function make(value: i32): Result[Product, Option[Problem]] { return Err(Some(Problem { value: value })); } }
impl Child[Problem] for Product {}
function run[T: Child[Problem]](witness: T): i32 {
 let Err(result) = T.make(-7) else { return 1; };
 let Some(problem) = result else { return 2; };
 if (problem.value != -7) { return 3; } return 0;
}
function main(): i32 { return run(Product {}); }`, ""},
		{"generic-implementation", `trait Parent[A] { function echo(self: Self, value: A): A; }
trait Child[U]: Parent[U] {}
struct Box[A] { value: A }
impl[A] Parent[A] for Box[A] { function echo(self: Self, value: A): A { return value; } }
impl[A] Child[A] for Box[A] {}
function run[T: Child[string]](witness: T): i32 { if (witness.echo("hello").len() != 5) { return 1; } return 0; }
function main(): i32 { return run(Box[string] { value: "world" }); }`, ""},
		{"default-method", `trait Parent[A] { function echo(value: A): A { return value; } }
trait Child[U]: Parent[U] {}
struct Marker {}
impl Parent[string] for Marker {}
impl Child[string] for Marker {}
function run[T: Child[string]](witness: T): i32 { if (T.echo("hello").len() != 5) { return 1; } return 0; }
function main(): i32 { return run(Marker {}); }`, ""},
		{"array-parent", `trait Parent {} trait Child: Parent[] {} function main(): i32 { return 0; }`, "P001"},
		{"generic-array-parent", `trait Parent[A] {} trait Child: Parent[i32][] {} function main(): i32 { return 0; }`, "P001"},
		{"tuple-parent", `trait Parent {} trait Child: (Parent, i32) {} function main(): i32 { return 0; }`, "P001"},
		{"function-parent", `trait Child: function(i32): i32 {} function main(): i32 { return 0; }`, "P001"},
		{"view-parent", `trait Parent {} trait Child: view Parent {} function main(): i32 { return 0; }`, "P001"},
		{"imported-value-namespace", `import "./values" as values;
function main(): i32 { if (values.pick("hello", true) != 16) { return 1; } return 0; }`, ""},
		{"imported-default-value-namespace", `import "./values" as values;
struct Marker {}
impl values.Default[string, boolean] for Marker {}
function run[T: values.Default[string, boolean]](witness: T): i32 { return T.value("hello", true); }
function main(): i32 { if (run(Marker {}) != 16) { return 1; } return 0; }`, ""},
		{"imported-shadow", `import "./api" as api;
struct Marker {}
impl api.Echo[string] for Marker { function echo(value: string): string { return value; } }
function run[T: api.Echo[string]](witness: T): i32 { if (T.echo("hello").len() != 5) { return 1; } return 0; }
function main(): i32 { return run(Marker {}); }`, ""},
		{"imported-receiver-shadow", `import "./api" as api;
struct Marker {}
impl api.Receiver[string] for Marker { function echo(self: Self, value: string): string { return value; } }
function run[T: api.Receiver[string]](witness: T): i32 { if (witness.echo("hello").len() != 5) { return 1; } return 0; }
function main(): i32 { return run(Marker {}); }`, ""},
		{"imported-default-shadow", `import "./api" as api;
struct Marker {}
impl api.Default[string] for Marker {}
function run[T: api.Default[string]](witness: T): i32 { if (T.echo("hello").len() != 5) { return 1; } return 0; }
function main(): i32 { return run(Marker {}); }`, ""},
		{"imported-nested-shadow", `import "./api" as api;
struct Marker {}
impl api.Nested[string] for Marker { function echo(value: Option[string]): Option[string] { return value; } }
function run[T: api.Nested[string]](witness: T): i32 { let Some(value) = T.echo(Some("hello")) else { return 1; }; if (value.len() != 5) { return 2; } return 0; }
function main(): i32 { return run(Marker {}); }`, ""},
		{"imported-concrete-scope", `import "./api" as api;
struct Marker {}
impl api.Concrete for Marker { function echo(value: api.A): api.A { return value; } }
function run[T: api.Concrete](witness: T): i32 { if (T.echo(api.A { value: 7 }).value != 7) { return 1; } return 0; }
function main(): i32 { return run(Marker {}); }`, ""},
		{"wrong-associated-argument", echo + `function bad[T: Child[string]](witness: T): string { return T.echo(7); } function main(): i32 { return 0; }`, "E038"},
		{"wrong-receiver-argument", receiver + `function bad[T: Child[string]](witness: T): string { return witness.echo(7); } function main(): i32 { return 0; }`, "E038"},
		{"wrong-parent-implementation", `trait Parent[A] {} trait Child[U]: Parent[U] {} struct Marker {} impl Parent[i32] for Marker {} impl Child[string] for Marker {} function main(): i32 { return 0; }`, "E021"},
		{"missing-parent-argument", `trait Parent[A] {} trait Child: Parent {} function main(): i32 { return 0; }`, "E021"},
		{"extra-parent-argument", `trait Parent[A] {} trait Child: Parent[i32, string] {} function main(): i32 { return 0; }`, "E021"},
		{"imported", `import "./api" as api;
struct Product {}
impl api.Parent[api.Problem] for Product { function make(value: i32): Result[Product, api.Problem] { return Err(api.Problem { value: value }); } }
impl api.Child[api.Problem] for Product {}
function run[T: api.Child[api.Problem]](witness: T): i32 { let Err(problem) = T.make(-7) else { return 1; }; if (problem.value != -7) { return 2; } return 0; }
function main(): i32 { return run(Product {}); }`, ""},
		{"qualified-parent", `import "./api" as contracts;
trait Child[U]: contracts.Parent[U] {}
struct Product {}
impl contracts.Parent[contracts.Problem] for Product { function make(value: i32): Result[Product, contracts.Problem] { return Err(contracts.Problem { value: value }); } }
impl Child[contracts.Problem] for Product {}
function run[T: Child[contracts.Problem]](witness: T): i32 { let Err(problem) = T.make(-7) else { return 1; }; if (problem.value != -7) { return 2; } return 0; }
function main(): i32 { return run(Product {}); }`, ""},
	}
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(tc.name, "imported") || tc.name == "qualified-parent" {
				const api = `pub struct A { value: i32 }
pub struct U { value: i32 }
pub struct Problem { value: i32 }
pub trait Echo[A] { function echo(value: A): A; }
pub trait Receiver[A] { function echo(self: Self, value: A): A; }
pub trait Default[A] { function echo(value: A): A { let copy: A = value; return copy; } }
pub trait Nested[A] { function echo(value: Option[A]): Option[A]; }
pub trait Concrete { function echo(value: A): A; }
pub trait Parent[A] { function make(value: i32): Result[Self, A]; }
pub trait Child[U]: Parent[U] {}`
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "api.fern"), []byte(api), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "imported-value-namespace" || tc.name == "imported-default-value-namespace" {
				const values = `pub const A: i32 = 7;
pub function B(): i32 { return 9; }
pub function pick[A, B](a: A, b: B): i32 { return A + B(); }
pub trait Default[A, B] { function value(a: A, b: B): i32 { return A + B(); } }`
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "values.fern"), []byte(values), 0o644); err != nil {
					t.Fatal(err)
				}
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
			if strings.HasPrefix(tc.name, "imported") || tc.name == "qualified-parent" {
				for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi", "arm64-darwin"} {
					t.Run(target, func(t *testing.T) {
						if target == "arm64-darwin" {
							dest := filepath.Join(filepath.Dir(path), target)
							cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-o", dest, path, cli.stdlib)
							cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
							if out, err := cmd.CombinedOutput(); err != nil {
								t.Fatalf("%s build: %v\n%s", target, err, out)
							}
							return
						}
						stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
						if code != 0 {
							t.Fatalf("%s exit %d\n%s", target, code, stderr)
						}
						allocs, frees, live := parseLeakcheck(t, target, stderr)
						if allocs != frees || live != 0 {
							t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
						}
					})
				}
				return
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, tc.source, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"); code != 0 {
					t.Errorf("%s exit %d\n%s", target, code, stderr)
				}
			}
		})
	}
}
