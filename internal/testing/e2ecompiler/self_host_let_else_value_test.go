package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A let-else in a value branch keeps the tail of the branch as the value. A
// value if-let whose branch is an Ok has a whole type for its value local.
func TestSelfHostLetElseValueBranch(t *testing.T) {
	const source = `
enum Shape { Circle(i32), Square(i32), Dot }
function block(t: Option[i32]): i32 {
  let r = {
    let Some(s) = t else {
      return 0;
    };
    s + 1
  };
  return r + 100;
}
function arm(x: Shape): Result[i32, string] {
  let v = match (x) {
    Shape.Dot => Err("dot"),
    _ => {
      let Shape.Circle(c) | Shape.Square(c) = x else {
        return Err("none");
      };
      Ok(c * 2)
    },
  };
  return v;
}
function tuple(p: (i32, i32)): string {
  let s = {
    let (1, b) = p else {
      return "no";
    };
    let q = b + 1;
    q.to_string()
  };
  return "t" + s;
}
function literal(n: i32): i32 {
  let s = {
    let 3 | 4 = n else {
      return -1;
    };
    n * 10
  };
  return s;
}
function nested(t: Option[Option[i32]]): i32 {
  let s = {
    let Some(Some(z)) = t else {
      return -2;
    };
    z
  };
  return s;
}
function two(a: Option[i32], b: Option[i32]): i32 {
  let r = {
    let Some(x) = a else {
      return -1;
    };
    let Some(y) = b else {
      return -2;
    };
    x * 10 + y
  };
  return r;
}
function branch(a: Option[i32], k: boolean): i32 {
  let r = if (k) {
    let Some(x) = a else {
      return 5;
    };
    x
  } else {
    7
  };
  return r;
}
function in_loop(t: Option[i32]): i32 {
  let total = 0;
  let s = {
    for i in 0..3 {
      let Some(z) = t else {
        break;
      };
      total += z + i;
    }
    total
  };
  return s;
}
function owned(t: Option[string]): string {
  let s = {
    let Some(text) = t else {
      return "none";
    };
    text + "!"
  };
  return s;
}
function if_let_ok(x: Option[i32]): Result[i32, string] {
  let k = if let Some(c) = x { Ok(c * 2) } else { return Err("none"); };
  return k;
}
function arm_if_let(x: Shape): Result[i32, string] {
  let v = match (x) {
    Shape.Dot => Err("dot"),
    _ => {
      if let Shape.Circle(c) = x { Ok(c * 2) } else { return Err("none"); }
    },
  };
  return v;
}
function main(): i32 {
  if (!match (if_let_ok(Some(4))) { Ok(v) => v == 8, Err(_) => false }) { return 11; }
  if (!match (if_let_ok(None)) { Err(e) => e == "none", Ok(_) => false }) { return 12; }
  if (!match (arm_if_let(Shape.Circle(3))) { Ok(v) => v == 6, Err(_) => false }) { return 13; }
  if (!match (arm_if_let(Shape.Square(3))) { Err(e) => e == "none", Ok(_) => false }) { return 14; }
  if (block(Some(5)) != 106 || block(None) != 0) { return 1; }
  if (!match (arm(Shape.Square(4))) { Ok(v) => v == 8, Err(_) => false }) { return 2; }
  if (!match (arm(Shape.Dot)) { Err(e) => e == "dot", Ok(_) => false }) { return 3; }
  if (tuple((1, 7)) != "t8" || tuple((2, 7)) != "no") { return 4; }
  if (literal(4) != 40 || literal(5) != -1) { return 5; }
  if (nested(Some(Some(9))) != 9 || nested(Some(None)) != -2) { return 6; }
  if (two(Some(1), Some(2)) != 12 || two(None, Some(2)) != -1 || two(Some(1), None) != -2) { return 7; }
  if (branch(Some(3), true) != 3 || branch(None, true) != 5 || branch(None, false) != 7) { return 8; }
  if (in_loop(Some(2)) != 9 || in_loop(None) != 0) { return 9; }
  if (owned(Some("a")) != "a!" || owned(None) != "none") { return 10; }
  return 0;
}`
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, cmd := range map[string]*exec.Cmd{
		"Go-check":    exec.Command(oracle, "-check", path),
		"Go-interp":   exec.Command(oracle, "-interp", path),
		"self-check":  runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib),
		"self-interp": runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib),
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
				t.Fatalf("value let-else: %v\n%s", err, out)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("value let-else: exit %d\n%s", code, stderr)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != frees || live != 0 {
				t.Fatalf("allocation census: %v\n%s", err, stderr)
			}
		})
	}
}

// The else branch of a value let-else must diverge, and the rest of its
// block must end in a value.
func TestSelfHostLetElseValueBranchDiagnostics(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for name, tc := range map[string]struct{ src, want string }{
		"else-value": {`function g(t: Option[i32]): i32 {
  let r = {
    let Some(s) = t else {
      5
    };
    s + 1
  };
  return r;
}
function main(): i32 { return g(None); }
`, "3:5: error[E022]"},
		"no-tail": {`function g(t: Option[i32]): i32 {
  let r = {
    let Some(s) = t else {
      return 0;
    };
    s + 1;
  };
  return r;
}
function main(): i32 { return g(None); }
`, "3:5: error[E061]: block-expression has no trailing value; a value is required here"},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for front, cmd := range map[string]*exec.Cmd{
				"Go":   exec.Command(oracle, "-check", path),
				"self": runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib),
			} {
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), tc.want) {
					t.Errorf("%s: want %q, got %v\n%s", front, tc.want, err, out)
				}
			}
		})
	}
}
