package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestSelfHostREPL drives `-repl` through one session: expressions print their
// value, declarations and `let`s carry to later lines, a void call prints
// nothing of its own, a function declared again replaces the first, and an
// error is reported without ending the session.
func TestSelfHostREPL(t *testing.T) {
	in := strings.Join([]string{
		"1 + 2 * 3",
		`"hi"`,
		"let x = 5;",
		"x + 10",
		"function dbl(x: i32): i32 { return x * 2; }",
		"dbl(21)",
		`print("side")`,
		`function f(): void { print("in f"); }`,
		"f()",
		"let a: i32[] = [1, 2];",
		"a",
		"let y: i32 = ;",
		"function dbl(x: i32): i32 { return x * 3; }",
		"dbl(2)",
		"x = x + 1;",
		"x",
		"2.5",
		"0.1 + 0.2",
		"true",
		"struct P { x: i32, y: i32 }",
		"let p = P { x: 1, y: 2 };",
		"p",
		"p.x",
		"let o: Option[i32] = Some(3);",
		"o",
		`(1, "a")`,
		"9000000000",
		"'c'",
		"",
	}, "\n")
	want := strings.Join([]string{
		"fern REPL — Ctrl+D to exit",
		"> 7",
		"> hi",
		"> > 15",
		"> > 42",
		"> side",
		"> > in f",
		"> > [1, 2]",
		"> error: in fn '__repl': expected an expression, got \";\"",
		"> > 6",
		"> > 6",
		"> 2.5",
		"> 0.30000000000000004",
		"> true",
		"> > > P { x: 1, y: 2 }",
		"> 1",
		"> > Some(3)",
		"> (1, a)",
		"> 9000000000",
		"> c",
		"> ",
		"",
	}, "\n")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-repl", e2eharness.SelfHostStdlibRoot(t))
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != want {
		t.Errorf("%v\n--- got ---\n%s\n--- want ---\n%s", err, out, want)
	}
}

// effectsRun writes src as main.fern and returns `-effects`' stdout and exit
// status.
func effectsRun(t *testing.T, src string) (string, int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-effects", path, e2eharness.SelfHostStdlibRoot(t))
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	return stdout.String() + stderr.String(), cmd.ProcessState.ExitCode()
}

// TestSelfHostEffects pins `-effects` on a program using each edge the graph
// draws: a method call, a function passed as a value (an escape), a call
// through a parameter (an indirect call), a lambda that escapes, and a
// constructor call, which is neither.
func TestSelfHostEffects(t *testing.T) {
	src := `struct Logger { path: string }

function (l: Logger) log(msg: string): void {
  match (write_file(l.path, msg)) {
    Ok(_) => {},
    Err(_) => {}
  }
}

function save(p: string): void {
  let l: Logger = Logger { path: p };
  l.log("x");
}

function wrap(n: i32): Option[i32] {
  return Some(n);
}

function shout(): void {
  print("hi");
}

function apply(f: () => void): void {
  f();
}

pub function run(): void {
  apply(shout);
  let k: () => void = () => { save("y"); };
  k();
}

function main(): i32 {
  run();
  match (wrap(0)) {
    Some(v) => {
      return v;
    },
    None => {
      return 1;
    }
  }
}
`
	want := `== authority (caps) — what a dependency can reach ==
Logger.log  fs  (example: Logger.log → write_file)
save  fs  (example: save → Logger.log → write_file)
apply  fs  (via a function value: fs)  (example: apply → (function value) → save → Logger.log → write_file)
run  fs  (example: run → save → Logger.log → write_file)
main  fs  (example: main → run → save → Logger.log → write_file)

7 functions; 5 reach an effect, of which 1 call a tagged builtin themselves and 4 only inherit
1 are ` + "`pub`" + `, of which 1 reach an effect — the size of a "require a row on public signatures" rule
1 reach an effect only through a function value
  row size 0: 2 functions
  row size 1: 5 functions

== host (platforms) — what the target must provide ==
Logger.log  fs  (example: Logger.log → write_file)
save  fs  (example: save → Logger.log → write_file)
shout  log  (example: shout → print)
apply  fs,log  (via a function value: fs,log)  (example: apply → (function value) → save → Logger.log → write_file)
run  fs,log  (example: run → save → Logger.log → write_file)
main  fs,log  (example: main → run → save → Logger.log → write_file)

7 functions; 6 reach an effect, of which 2 call a tagged builtin themselves and 4 only inherit
1 are ` + "`pub`" + `, of which 1 reach an effect — the size of a "require a row on public signatures" rule
1 reach an effect only through a function value
  row size 0: 1 functions
  row size 1: 3 functions
  row size 2: 3 functions

`
	if out, code := effectsRun(t, src); code != 0 || out != want {
		t.Errorf("exit %d\n%s", code, firstDifference(out, want))
	}

	// A call through a function held in a struct field is a call through a
	// value too.
	field := `struct Route { handler: () => void }

function shout(): void {
  print("hi");
}

function dispatch(r: Route): void {
  r.handler();
}

function main(): i32 {
  dispatch(Route { handler: shout });
  return 0;
}
`
	if out, _ := effectsRun(t, field); !strings.Contains(out, "dispatch  log  (via a function value: log)  (example: dispatch → (function value) → shout → print)") {
		t.Errorf("a call through a function field is not charged:\n%s", out)
	}

	// A program that does not check is refused with its diagnostics.
	if out, code := effectsRun(t, "function main(): i32 { return \"s\"; }\n"); code != 1 || !strings.Contains(out, "error[E002]") {
		t.Errorf("exit %d\n%s", code, out)
	}
}
