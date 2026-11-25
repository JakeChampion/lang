package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Cell.replace stores its argument and gives the old payload to the caller
// as an owned value, also when the payload holds a handle (#15523).
func TestSelfHostCellReplace(t *testing.T) {
	const source = `
struct H { n: i32, name: string }
function (own h: H) close(): i32 {
  return h.n + h.name.len();
}
function take(c: Cell[Option[H]]): i32 {
  match (c.replace(None)) {
    Some(h) => {
      return h.close();
    },
    None => {
      return 0;
    }
  }
  return 0;
}
function swap_in(c: Cell[H], own next: H): i32 {
  let old = c.replace(next);
  return old.close();
}
function main(): i32 {
  let pool = cell_new(Some(H { n: 4, name: "a" + "b" }));
  assert(take(pool) == 6);
  assert(take(pool) == 0);
  let one = cell_new(H { n: 1, name: "x" });
  assert(swap_in(one, H { n: 2, name: "y" + "y" }) == 2);
  assert(one.get().n == 2);
  let xs = cell_new([1, 2, 3]);
  let none: i32[] = [];
  let old = xs.replace(none);
  xs.set(old.append(4));
  assert(xs.get().len() == 4 && xs.get()[3] == 4);
  let s = cell_new("x");
  let prev = s.replace("y" + "z");
  assert(prev == "x" && s.get() == "yz");
  return 0;
}
`
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
				t.Fatalf("Cell.replace: %v\n%s", err, out)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("Cell.replace: exit %d\n%s", code, stderr)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != frees || live != 0 {
				t.Fatalf("allocation census: %v\n%s", err, stderr)
			}
		})
	}
}

// Cell.replace takes one argument of the element type.
func TestSelfHostCellReplaceDiagnostics(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for name, tc := range map[string]struct{ src, want string }{
		"arity": {`function main(): i32 {
  let c = cell_new(1);
  let old = c.replace();
  return old;
}
`, "error[E004]"},
		"element-type": {`function main(): i32 {
  let c = cell_new(1);
  let old = c.replace("one");
  return old;
}
`, "error[E038]"},
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
