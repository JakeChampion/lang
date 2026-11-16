package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ssaholecell.cells on loops made by hand: a loop that fills its hole at each
// turn and before its return, a result that a call reads, and a return with
// no fill before it.
func TestSelfHostSSAHoleCell(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	const driver = "drivers/ssaholecell_run.fern"
	copySelfHostDriver(t, dir, driver)
	bin := buildSelfHostBin(t, gcc, dir, driver, "ssaholecell")
	cmd := exec.Command(bin)
	if len(runner) != 0 {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "failures=0\n" {
		t.Fatalf("SSA hole cell: %v\n%s", err, out)
	}
}

// inc_all and append fill one hole, the second field of the cell before.
// insert fills the left or the right field of the node above. main gives each
// of them a box with one count and a box with two counts.
const holeCellProg = `enum L { Nil, Cons(i64, L) }

enum T { Leaf, Node(T, i64, T) }

// One hole: the second field of the cell before.
@noinline function inc_all(xs: L): L {
  return match (xs) { Nil => Nil, Cons(h, t) => Cons(h + 1i64, inc_all(t)) };
}

// Two holes: the left or the right field of the node above.
@noinline function insert(t: T, k: i64): T {
  match (t) {
    Leaf => {
      return Node(Leaf, k, Leaf);
    },
    Node(l, v, r) => {
      if (k < v) {
        return Node(insert(l, k), v, r);
      }
      return Node(l, v, insert(r, k));
    }
  }
}

function append(xs: L, ys: L): L {
  return match (xs) { Nil => ys, Cons(h, t) => Cons(h, append(t, ys)) };
}

// Two loops in one function, each with its own result.
@noinline function chain(xs: L, ys: L, zs: L): L {
  return append(append(xs, ys), zs);
}

@noinline function upto(n: i64): L {
  let xs = Nil;
  let i = n;
  while (i > 0i64) {
    xs = Cons(i, xs);
    i -= 1i64;
  }
  return xs;
}

function sum(xs: L): i64 {
  let cur = xs;
  let t = 0i64;
  while (true) {
    match (cur) {
      Nil => {
        return t;
      },
      Cons(h, rest) => {
        t += h;
        cur = rest;
      }
    }
  }
  return t;
}

function total(t: T): i64 {
  return match (t) { Leaf => 0i64, Node(l, v, r) => total(l) + v + total(r) };
}

function main(): i32 {
  let ok = 0;
  let xs = upto(100i64);
  let kept = xs;
  let ys = inc_all(xs);
  if (sum(ys) == 5150i64 && sum(kept) == 5050i64) {
    ok += 10;
  }
  if (sum(inc_all(Nil)) == 0i64) {
    ok += 1;
  }
  let t = Leaf;
  for i in 0..200 {
    t = insert(t, ((i * 37) % 101) as i64);
  }
  let shared = t;
  let more = insert(t, 500i64);
  if (total(more) == total(shared) + 500i64) {
    ok += 10;
  }
  let joined = chain(upto(10i64), upto(20i64), upto(30i64));
  if (sum(joined) == 55i64 + 210i64 + 465i64 && sum(chain(Nil, Nil, Nil)) == 0i64) {
    ok += 21;
  }
  return ok;
}
`

// A cons-tail loop keeps its result in a cell of the frame, and its hole
// starts at the address of that cell (ssaholecell.cells). A fill is then one
// store: the loop of inc_all tests no register against null.
func TestSelfHostHoleStartsAtAFrameCell(t *testing.T) {
	h := selfHostCLIForHost(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "cell.fern")
	if err := os.WriteFile(src, []byte(holeCellProg), 0o644); err != nil {
		t.Fatal(err)
	}
	cells := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`(?m)^\s+leaq -\d+\(%rbp\), %r`),
		"arm64-linux":  regexp.MustCompile(`(?m)^\s+sub x\d+, x29, #\d+$`),
	}
	nulls := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`(?m)^\s+cmpq \$0, %r`),
		"arm64-linux":  regexp.MustCompile(`(?m)^\s+(cbz|cbnz) x\d+,|^\s+cmp x\d+, #0$`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		asm, err := os.ReadFile(cli.emit(t, src, target))
		if err != nil {
			t.Fatal(err)
		}
		text := string(asm)
		for _, fn := range []string{"inc_all", "insert", "append"} {
			at := strings.Index(text, "\n__fn_"+fn+".r:\n")
			if at < 0 {
				t.Fatalf("%s: no %s in the listing", target, fn)
			}
			body := text[at:]
			if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
				body = body[:end]
			}
			if !cells[target].MatchString(body) {
				t.Errorf("%s: %s takes no address of a frame cell:\n%s", target, fn, body)
			}
			if fn == "inc_all" {
				if m := nulls[target].FindString(body); m != "" {
					t.Errorf("%s: inc_all tests a register against null (%q):\n%s", target, strings.TrimSpace(m), body)
				}
			}
		}
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		stderr, code := cli.exitOf(t, holeCellProg, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
		if code != 42 {
			t.Fatalf("%s: exit %d, want 42\n%s", target, code, stderr)
		}
		allocs, frees, live := parseLeakcheck(t, "hole-cell", stderr)
		if allocs != frees || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
		}
	}
}
