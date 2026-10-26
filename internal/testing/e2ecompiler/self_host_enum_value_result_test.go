package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostNestedEnumValueResult(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const library = `pub enum Wrap[E] { Problem(E), Empty }
pub enum Outcome { Pass(i32), Fail(string) }
pub enum Box[T] { Boxed(T), Vacant }
pub function outcome(flag: boolean, message: string): Outcome {
  return if (flag) { Pass(7) } else { Fail(message + "-failure") };
}
`
	const source = `import "./lib";
enum Inner { Data(string), Empty }
enum Outer { Wrapped(Inner), Other }
function generic(value: Result[i32, lib.Wrap[string]]): lib.Outcome {
  return match (value) { Err(lib.Problem(message)) => lib.outcome(message.len() > 0, message), _ => lib.outcome(false, "fallback") };
}
function ordinary(value: Outer): lib.Outcome {
  return match (value) { Outer.Wrapped(Inner.Data(message)) => lib.outcome(message.len() > 0, message), _ => lib.outcome(false, "other") };
}
function early(value: Result[i32, lib.Wrap[string]], flag: boolean): lib.Outcome {
  return match (value) { Err(lib.Problem(message)) => { if (flag) { return lib.outcome(false, message); } lib.outcome(true, message) }, _ => lib.outcome(true, "other") };
}
function packed[T](value: Result[i32, lib.Wrap[T]], seed: T): lib.Box[T] {
  return match (value) { Err(lib.Problem(item)) => lib.Boxed(item), _ => lib.Boxed(seed) };
}
function main(): i32 {
  let text = args()[0] + "-owned";
  for result in [generic(Err(lib.Problem(text))), ordinary(Outer.Wrapped(Inner.Data(text))), early(Err(lib.Problem(text)), false)] {
    match (result) { lib.Pass(n) => { if (n != 7) { return 1; } }, lib.Fail(_) => { return 2; } }
  }
  for result in [generic(Ok(1)), ordinary(Outer.Other), early(Err(lib.Problem(text)), true)] {
    match (result) { lib.Fail(message) => { if (message.len() == 0) { return 3; } }, lib.Pass(_) => { return 4; } }
  }
  match (packed(Err(lib.Problem(text)), text)) { lib.Boxed(v) => { if (v != text) { return 5; } }, lib.Vacant => { return 6; } }
  match (packed[i64](Ok(1), 5000000007i64)) { lib.Boxed(v) => { if (v != 5000000007i64) { return 7; } }, lib.Vacant => { return 8; } }
  return 0;
}

`
	for _, target := range []string{"interpreter", "x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			for name, text := range map[string]string{"lib.fern": library, "main.fern": source} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if target == "interpreter" {
				oracle := buildLangBinForInterp(t)
				if out, err := exec.Command(oracle, "-interp", filepath.Join(dir, "main.fern")).CombinedOutput(); err != nil {
					t.Fatalf("interpreter: %v\n%s", err, out)
				}
				return
			}
			stderr, code := cli.exitOfFile(t, filepath.Join(dir, "main.fern"), target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("nested enum result exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostNestedEnumValueResultRejects(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const prefix = `enum Inner { Data(string), Empty }
enum Outer { Wrapped(Inner), Other }
enum Outcome { Pass(i32), Fail(string) }
enum Different { Good(i32), Bad(string) }
enum Box[T] { Boxed(T), Vacant }
function good(): Outcome { return Outcome.Pass(7); }
function other(): Different { return Different.Good(7); }
`
	for _, tc := range []struct{ name, source, code string }{
		{"primitive", `function f(v: Outer): Outcome { return match (v) { Outer.Wrapped(Inner.Data(s)) => 7, _ => 8 }; }`, "E002"},
		{"nominal", `function f(v: Outer): Outcome { return match (v) { Outer.Wrapped(Inner.Data(s)) => other(), _ => other() }; }`, "E002"},
		{"annotated local", `function f(v: Outer): Outcome { let out: Outcome = match (v) { Outer.Wrapped(Inner.Data(s)) => other(), _ => other() }; return out; }`, "E003"},
		{"mixed arms", `function f(v: Outer): Outcome { return match (v) { Outer.Wrapped(Inner.Data(s)) => good(), _ => true }; }`, "E003"},
		{"generic payload", `function f(v: Outer): Box[string] { return match (v) { Outer.Wrapped(Inner.Data(s)) => Boxed(7), _ => Boxed(8) }; }`, "E002"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(prefix+tc.source+"\nfunction main(): i32 { return 0; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					out, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "out"), path).CombinedOutput()
					if err == nil || !strings.Contains(string(out), "error["+tc.code+"]") {
						t.Fatalf("expected %s, got %v\n%s", tc.code, err, out)
					}
				})
			}
		})
	}
}
