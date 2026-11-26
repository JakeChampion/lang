package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

const deriveReachLib = `import "core/cmp";

@derive(cmp.Eq, cmp.Hash, cmp.Display)
pub struct Fault { n: i32, s: string }

@derive(cmp.Eq, cmp.Display)
pub enum Shade { Light, Dark(i32) }

pub function size(n: i32): Result[i32, Fault] {
  if (n < 0) {
    return Err(Fault { n: n, s: "negative" });
  }
  return Ok(n * 3);
}

pub function shade(n: i32): Shade {
  if (n > 10) {
    return Dark(n);
  }
  return Light;
}
`

const deriveReachMain = `import "./shapes";

function main(): i32 {
  let n = match (shapes.size(14)) {
    Ok(v) => v,
    Err(f) => f.n,
  };
  match (shapes.shade(n)) {
    Dark(d) => {
      return d;
    },
    Light => {
      return 0;
    }
  }
  return 1;
}
`

// An imported module's types carry derived methods that the program builds
// values of but never calls. semreach drops them like any other uncalled
// function.
func TestSelfHostSemReachDropsUncalledDerive(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shapes.fern"), []byte(deriveReachLib), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(deriveReachMain), 0o644); err != nil {
		t.Fatal(err)
	}
	derived := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`(?m)^__fn_shapes__(Fault|Shade)__\w+:`),
		"arm64-linux":  regexp.MustCompile(`(?m)^__fn_shapes__(Fault|Shade)__\w+:`),
		"wasm32-wasi":  regexp.MustCompile(`\(func \$shapes__(Fault|Shade)\.\w+`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		asm := readFile(t, cli.emit(t, path, target))
		if m := derived[target].FindString(asm); m != "" {
			t.Errorf("%s: emitted the uncalled derived method %q", target, m)
		}
		if _, code := cli.exitOfFile(t, path, target, nil); code != 42 {
			t.Errorf("%s: exit %d, want 42", target, code)
		}
	}
}

const dynDerivedProg = `import "core/cmp";
import "core/map";

@derive(cmp.Eq, cmp.Hash, cmp.Display)
struct Key { a: i32, b: string }

@derive(cmp.Eq, cmp.Display)
enum Shade { Light, Dark(i32) }

struct Plain { n: i32 }

impl cmp.Display for Plain {
  function to_string(self: Self): string {
    return "plain";
  }
}

@noinline function width(d: dyn cmp.Display): i32 {
  return d.to_string().len();
}

@noinline function same[X: cmp.Eq](a: X, b: X): boolean {
  return a.eq(b);
}

function main(): i32 {
  let shown: dyn cmp.Display[] = [Key { a: 1, b: "q" }, Dark(7), Plain { n: 2 }];
  let total = 0;
  for d in shown {
    print(d.to_string());
    total += width(d);
  }
  let m: Map[Key, i32] = Map { Key { a: 1, b: "x" }: 10 };
  total += m.get_or(Key { a: 1, b: "x" }, 0);
  if (same(Dark(3), Dark(3)) && !same(Light, Dark(3))) {
    total += 100;
  }
  return total - 100;
}
`

// A dyn call dispatches to a derived method as it does to one that an impl
// writes, for a struct and for an enum. The derived eq and hash of a map key
// and of a generic bound stay reachable.
func TestSelfHostDynDerivedMethod(t *testing.T) {
	cli := buildSelfHostCLI(t)
	fern := buildLangBinForInterp(t)
	const want = 40
	if got := interpExitStdin(t, fern, dynDerivedProg, ""); got != want {
		t.Fatalf("the interpreter exited %d, want %d", got, want)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		stderr, code := cli.exitOf(t, dynDerivedProg, target)
		if code != want {
			t.Errorf("%s: exit %d, want %d (stderr %q)", target, code, want, stderr)
		}
	}
}
