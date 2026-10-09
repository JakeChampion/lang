package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Cells over composite elements (#2679): the state a long-running server keeps
// across requests. Each program asserts its own results and exits 0, and runs
// under the leak census, which is what proves the cell co-owns its element:
// a `get` that forgot to retain over-releases, a `set` that forgot to release
// leaks the value it replaced, and a final drop that freed only the box
// strands everything the element held.
var compositeCellSources = []struct{ name, source string }{
	{"server-session-table", `import "core/map";

struct Session { user: string, hits: i32, paths: string[] }
struct Req { user: string, path: string }

function visit(m: Map[string, Session], r: Req): Map[string, Session] {
  let s: Session = Session { user: r.user, hits: 0, paths: [] };
  if let Some(old) = m.get(r.user) {
    s = old;
  }
  return m.insert(r.user, Session { ...s, hits: s.hits + 1, paths: s.paths.append(r.path) });
}

function serve(reqs: Req[], handle: (Req) => i32): i32 {
  let ok: i32 = 0;
  for r in reqs {
    ok = ok + handle(r);
  }
  return ok;
}

function main(): i32 {
  let sessions: Cell[Map[string, Session]] = cell_new(map_new(8));
  let served: Cell[i32] = cell_new(0);
  let handle = (r: Req): i32 => {
    sessions.set(visit(sessions.get(), r));
    served.set(served.get() + 1);
    return 1;
  };
  let reqs: Req[] = [Req { user: "ann", path: "/" }, Req { user: "bob", path: "/a" }, Req { user: "ann", path: "/b" }];
  let first: Map[string, Session] = sessions.get();
  let round: i32 = 0;
  while (round < 40) {
    assert(serve(reqs, handle) == 3);
    round = round + 1;
  }
  assert(served.get() == 120);
  assert(first.len() == 0);
  let table: Map[string, Session] = sessions.get();
  assert(table.len() == 2);
  if let Some(ann) = table.get("ann") {
    assert(ann.hits == 80 && ann.paths.len() == 80 && ann.paths[79] == "/b");
  } else {
    assert(false);
  }
  sessions.set(map_new(8));
  assert(table.len() == 2);
  return 0;
}
`},
	{"arrays-structs-enums", `struct Inner { tags: string[], best: Option[string] }
struct Outer { name: string, inner: Inner, grid: i32[][] }
struct Holder { slot: Cell[Outer] }
enum Tree { Leaf(string), Node(Tree[]) }

function size(t: Tree): i32 {
  match (t) {
    Leaf(_) => {
      return 1;
    },
    Node(kids) => {
      let n: i32 = 0;
      for k in kids {
        n = n + size(k);
      }
      return n;
    }
  }
}

function exercise(): void {
  let words: Cell[string[]] = cell_new([]);
  let i: i32 = 0;
  while (i < 24) {
    words.set(words.get().append("w"));
    words.set(words.get());
    i = i + 1;
  }
  assert(words.get().len() == 24);

  let o: Outer = Outer { name: "o", inner: Inner { tags: ["a"], best: None }, grid: [[1, 2], [3]] };
  let c: Cell[Outer] = cell_new(o);
  let h: Holder = Holder { slot: c };
  let before: Outer = c.get();
  let replace: () => void = (): void => {
    let cur: Outer = h.slot.get();
    h.slot.set(Outer { ...cur, inner: Inner { tags: cur.inner.tags.append("b"), best: Some("b") }, grid: cur.grid.append([4]) });
  };
  replace();
  replace();
  assert(c.get().inner.tags.len() == 3 && c.get().grid.len() == 4);
  assert(before.inner.tags.len() == 1 && o.grid.len() == 2);
  if let Some(b) = c.get().inner.best {
    assert(b == "b");
  } else {
    assert(false);
  }

  let tree: Cell[Tree] = cell_new(Leaf("x"));
  let k: i32 = 0;
  while (k < 6) {
    tree.set(Node([tree.get(), Leaf("y")]));
    k = k + 1;
  }
  assert(size(tree.get()) == 7);

  let pair: Cell[(string, char)] = cell_new(("a", 'a'));
  pair.set((pair.get().0 + "b", 'b'));
  assert(pair.get().0 == "ab" && pair.get().1 == 'b');
}

function main(): i32 {
  for i in 0..4 { exercise(); }
  return 0;
}
`},
}

// The refused half: a struct holding a closure could capture the very cell
// it is stored in — `tie` builds exactly that loop — so the compiler must
// reject the program rather than compile a leak.
const compositeCellCycle = `struct Handler { name: string, run: () => i32 }
function tie(c: Cell[Handler]): void {
  c.set(Handler { name: "loop", run: () => c.get().run() });
}
function main(): i32 {
  let c = cell_new(Handler { name: "h", run: () => 1 });
  tie(c);
  return 0;
}
`

func writeCompositeCellSource(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cell.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostCompositeCells(t *testing.T) {
	cli := buildSelfHostCLI(t)
	reference := buildLangBinForInterp(t)
	for _, tc := range compositeCellSources {
		t.Run(tc.name, func(t *testing.T) {
			path := writeCompositeCellSource(t, tc.source)
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
			t.Run("interpreter", func(t *testing.T) {
				cmd := runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("interpreter: %v\n%s", err, out)
				}
			})
			t.Run("reference-interpreter", func(t *testing.T) {
				if out, err := exec.Command(reference, "-interp", path).CombinedOutput(); err != nil {
					t.Fatalf("reference interpreter: %v\n%s", err, out)
				}
			})
		})
	}
	t.Run("cycle-refused", func(t *testing.T) {
		path := writeCompositeCellSource(t, compositeCellCycle)
		out, err := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "error[E057]") || !strings.Contains(string(out), "Handler contains") {
			t.Fatalf("want E057 naming what Handler contains, got err=%v\n%s", err, out)
		}
		out, err = exec.Command(reference, "-check", path).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "error[E057]") {
			t.Fatalf("reference checker: want E057, got err=%v\n%s", err, out)
		}
	})
}

// The interpreter the compiler builds of itself holds a composite element as
// encoded text; this runs that build, not the -interp of the CLI, so the
// codec runs compiled.
func TestSelfHostInterpreterCompositeCells(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/interp_run.fern")
	bin := filepath.Join(dir, "interp")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", bin, filepath.Join(dir, "drivers/interp_run.fern"), cli.stdlib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile interpreter: %v\n%s", err, out)
	}
	// interp_run reads one program on stdin and resolves no imports, so the
	// case is the import-free one.
	cmd = runX86_64Bin(cli.runner, bin)
	cmd.Stdin = bytes.NewBufferString(compositeCellSources[1].source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-built interpreter: %v\n%s", err, out)
	}
}

func TestSelfHostArm64DarwinCompositeCells(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	for _, tc := range compositeCellSources {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			src, bin := filepath.Join(root, "cell.fern"), filepath.Join(root, "cell")
			if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := exec.Command(bin).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
		})
	}
}
