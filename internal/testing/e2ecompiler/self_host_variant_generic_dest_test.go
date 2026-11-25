package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// A bare variant in the argument of a generic call takes the enum that the
// destination of the call gives the type parameter, as the Go checker does.
// The destination reaches the argument through the result type of the
// callee, and through a method whose receiver type is its result type. The
// module also sees a second enum with the same variant through an import.
func TestSelfHostBareVariantTakesTheGenericDestination(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	files := map[string]string{
		"lib.fern": `pub enum Other { Done(i32), Idle }

pub struct El[M] { m: M, n: i32 }

pub function toggle[M](on: boolean, m: M): El[M] {
  return El { m: m, n: 1 };
}

pub function row[M](xs: El[M][]): El[M] {
  return xs[0];
}

pub function (e: El[M]) spaced(dp: i32): El[M] {
  return El { ...e, n: e.n + dp };
}

pub function idle(): Other {
  return Idle;
}
`,
		"main.fern": `import "./lib";

enum Msg { Draft(string), Done(i32) }

function show(m: Msg): i32 {
  match (m) {
    Done(i) => { return i; },
    Draft(_) => { return 0; }
  }
}

function direct(i: i32): lib.El[Msg] {
  return lib.toggle(true, Done(i));
}

function nested(i: i32): lib.El[Msg] {
  return lib.row([lib.toggle(true, Done(i))]);
}

function chained(i: i32): lib.El[Msg] {
  return lib.row([lib.toggle(true, Done(i))]).spaced(2);
}

function main(): i32 {
  let c = chained(4);
  return show(direct(1).m) + show(nested(2).m) + show(c.m) + c.n;
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if stderr, code := cli.exitOfFile(t, filepath.Join(dir, "main.fern"), "x86-64-linux", nil); code != 10 {
		t.Fatalf("exit %d, want 10\n%s", code, stderr)
	}
}
