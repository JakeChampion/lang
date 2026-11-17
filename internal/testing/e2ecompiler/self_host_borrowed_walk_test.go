package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// has returns the result of its self call through `||`, and below through
// `&&` in the arm of a value match. far is the same walk with statements.
// probe keeps its list after each walk.
const borrowedWalkProg = `enum List { Nil, Cons(i32, List) }

function has(xs: List, k: i32): boolean {
  return match (xs) { Nil => false, Cons(v, rest) => v == k || has(rest, k) };
}

function below(xs: List, k: i32): boolean {
  return match (xs) { Nil => true, Cons(v, rest) => v < k && below(rest, k) };
}

function far(xs: List, k: i32, gap: i32): boolean {
  match (xs) {
    Nil => {
      return true;
    },
    Cons(v, rest) => {
      if (v == k || v == k + gap || v == k - gap) {
        return false;
      }
      return far(rest, k, gap + 1);
    }
  }
}

@noinline function probe(xs: List, k: i32): i32 {
  let total = 0;
  if (has(xs, k)) {
    total += 1;
  }
  if (below(xs, 100)) {
    total += 2;
  }
  if (far(xs, 500, 1)) {
    total += 4;
  }
  if (has(xs, k + 1000)) {
    total += 8;
  }
  return total;
}

@noinline function build(n: i32): List {
  let xs = Nil;
  for i in 0..n {
    xs = Cons(i, xs);
  }
  return xs;
}

function main(): i32 {
  let xs = build(50);
  let sum = 0;
  for round in 0..6 {
    sum += probe(xs, round * 7);
  }
  return sum;
}
`

// A walk that is a self tail call becomes a loop, also when the result of the
// call returns through the join of `&&` or `||` (ssasem.returned_self_calls).
// The walk is then inlined, and its loop borrows the list that the caller
// keeps (ownership.kept_alive, ssaunits.borrows_entry): probe counts nothing
// and calls no release.
func TestSelfHostWalkOfAKeptListBorrows(t *testing.T) {
	h := selfHostCLIForHost(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "walk.fern")
	if err := os.WriteFile(src, []byte(borrowedWalkProg), 0o644); err != nil {
		t.Fatal(err)
	}
	counts := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`(?m)^\s+(addl|subl) \$1, -8\(|call __fn___sem_(release|drop)_|call __fn___fern_box_dec`),
		"arm64-linux":  regexp.MustCompile(`(?m)^\s+stur w\d+, \[x\d+, #-8\]|bl __fn___sem_(release|drop)_|bl __fn___fern_box_dec`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		asm, err := os.ReadFile(cli.emit(t, src, target))
		if err != nil {
			t.Fatal(err)
		}
		text := string(asm)
		at := strings.Index(text, "\n__fn_probe.r:\n")
		if at < 0 {
			t.Fatalf("%s: no probe in the listing", target)
		}
		body := text[at:]
		if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
			body = body[:end]
		}
		if strings.Contains(body, "__fn_has") || strings.Contains(body, "__fn_below") || strings.Contains(body, "__fn_far") {
			t.Errorf("%s: probe calls a walk, so the walks are not loops in probe:\n%s", target, body)
		}
		if m := counts[target].FindString(body); m != "" {
			t.Errorf("%s: probe counts or releases the list that it keeps (%q):\n%s", target, strings.TrimSpace(m), body)
		}
	}
	// Each of the six rounds finds its key, and the two other walks pass.
	const want = 6 * (1 + 2 + 4)
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != want {
			t.Errorf("%s: exit %d, want %d", tg.target, got, want)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		stderr, code := cli.exitOf(t, borrowedWalkProg, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
		if code != want {
			t.Fatalf("%s: exit %d, want %d\n%s", target, code, want, stderr)
		}
		allocs, frees, live := parseLeakcheck(t, "borrowed-walk", stderr)
		if allocs != frees || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
		}
	}
}

// lookup is a method that calls itself on a part of its receiver. probe keeps
// its scope after each lookup.
const borrowedMethodWalkProg = `struct Scope { outer: Option[Scope], names: string[], values: i32[] }

@noinline function (e: Scope) lookup(name: string): i32 {
  let i = e.names.len() - 1;
  while (i >= 0) {
    if (e.names[i] == name) {
      return e.values[i];
    }
    i -= 1;
  }
  return match (e.outer) { Some(parent) => parent.lookup(name), None => 0 };
}

@noinline function probe(e: Scope, name: string): i32 {
  return e.lookup(name) + e.lookup("c") + e.lookup("none");
}

function main(): i32 {
  let g = Scope { outer: None, names: ["a", "b"], values: [1, 2] };
  let f = Scope { outer: Some(g), names: ["c"], values: [4] };
  let sum = 0;
  for round in 0..3 {
    sum += probe(f, "a") + probe(f, "b");
  }
  return sum;
}
`

// The graph of a method has the bare name of the method, and a call has the
// name of the receiver type before it. The inference finds the row and the
// self calls of a method by the name of the call, so the walk borrows a
// receiver that each caller keeps: lookup counts nothing and calls no
// release.
func TestSelfHostMethodWalkOfAKeptChainBorrows(t *testing.T) {
	h := selfHostCLIForHost(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "walk.fern")
	if err := os.WriteFile(src, []byte(borrowedMethodWalkProg), 0o644); err != nil {
		t.Fatal(err)
	}
	counts := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`(?m)^\s+(addl|subl) \$1, -8\(|call __fn___sem_(release|drop)_|call __fn___fern_box_dec|call __fn___fern_str_free`),
		"arm64-linux":  regexp.MustCompile(`(?m)^\s+stur w\d+, \[x\d+, #-8\]|bl __fn___sem_(release|drop)_|bl __fn___fern_box_dec|bl __fn___fern_str_free`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		asm, err := os.ReadFile(cli.emit(t, src, target))
		if err != nil {
			t.Fatal(err)
		}
		text := string(asm)
		at := strings.Index(text, "\n__fn_Scope__lookup.r:\n")
		if at < 0 {
			t.Fatalf("%s: no lookup in the listing", target)
		}
		body := text[at:]
		if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
			body = body[:end]
		}
		if m := counts[target].FindString(body); m != "" {
			t.Errorf("%s: lookup counts or releases the scope that its caller keeps (%q):\n%s", target, strings.TrimSpace(m), body)
		}
	}
	// Each round reads a (1) and b (2), and c (4) two times.
	const want = 3 * (1 + 2 + 4 + 4)
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != want {
			t.Errorf("%s: exit %d, want %d", tg.target, got, want)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		stderr, code := cli.exitOf(t, borrowedMethodWalkProg, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
		if code != want {
			t.Fatalf("%s: exit %d, want %d\n%s", target, code, want, stderr)
		}
		allocs, frees, live := parseLeakcheck(t, "borrowed-method-walk", stderr)
		if allocs != frees || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
		}
	}
}

// sum and count walk with a cursor in a while loop. after returns its cursor,
// and tails puts the rest of its cursor in a new cell at each step. probe
// owns its list and reads it after the walks. bagged owns a record, and its
// cursor starts at a part of that record. emptied takes its list from a cell
// that each step empties.
const whileWalkProg = `enum List { Nil, Cons(i32, List) }

function sum(xs: List): i32 {
  let cur = xs;
  let total = 0;
  while (true) {
    match (cur) {
      Nil => {
        return total;
      },
      Cons(v, rest) => {
        total += v;
        cur = rest;
      }
    }
  }
  return total;
}

function count(xs: List): i32 {
  let cur = xs;
  let n = 0;
  while (true) {
    match (cur) {
      Nil => {
        return n;
      },
      Cons(_, rest) => {
        n += 1;
        cur = rest;
      }
    }
  }
  return n;
}

// The cells of xs after its first n cells. The cursor leaves the loop.
function after(xs: List, n: i32): List {
  let cur = xs;
  for i in 0..n {
    match (cur) {
      Nil => {},
      Cons(_, rest) => {
        cur = rest;
      }
    }
  }
  return cur;
}

// Each step puts the rest of the cursor in a new cell.
function tails(xs: List): i32 {
  let cur = xs;
  let kept = Nil;
  let n = 0;
  while (true) {
    match (cur) {
      Nil => {
        return n + count(kept);
      },
      Cons(v, rest) => {
        kept = Cons(v, rest);
        n += 1;
        cur = rest;
      }
    }
  }
  return n;
}

function first(xs: List): i32 {
  return match (xs) { Nil => 0, Cons(v, _) => v };
}

@noinline function probe(n: i32): i32 {
  let xs = build(n);
  let total = sum(xs) + count(xs) * 100;
  return total + first(xs);
}

@noinline function mixed(xs: List): i32 {
  let rest = after(xs, 3);
  return sum(rest) + tails(xs) + count(xs);
}

@noinline function gone(n: i32): i32 {
  let rest = after(build(n), 2);
  return sum(rest);
}

struct Bag { items: List, size: i32 }

function bag_sum(b: Bag): i32 {
  let cur = b.items;
  let total = 0;
  while (true) {
    match (cur) {
      Nil => {
        return total;
      },
      Cons(v, rest) => {
        total += v;
        cur = rest;
      }
    }
  }
  return total;
}

@noinline function size_of(b: Bag): i32 {
  return b.size;
}

// The cursor starts at a part of a record that the function owns.
@noinline function bagged(n: i32): i32 {
  let b = Bag { items: build(n), size: n };
  return bag_sum(b) + size_of(b);
}

// Each step empties the cell that the list came from.
@noinline function emptied(n: i32): i32 {
  let c: Cell[List] = cell_new(build(n));
  let first = c.get();
  let cur = first;
  let total = 0;
  while (true) {
    match (cur) {
      Nil => {
        return total + count(first) + count(c.get());
      },
      Cons(v, rest) => {
        total += v;
        cur = rest;
        c.set(Nil);
      }
    }
  }
  return total;
}

@noinline function build(n: i32): List {
  let xs = Nil;
  for i in 0..n {
    xs = Cons(i, xs);
  }
  return xs;
}

function main(): i32 {
  let xs = build(50);
  let ok = 0;
  for round in 0..4 {
    if (probe(50) == 6274) {
      ok += 1;
    }
    if (mixed(xs) == 1182) {
      ok += 2;
    }
  }
  if (gone(10) == 28) {
    ok += 10;
  }
  if (bagged(10) == 55) {
    ok += 10;
  }
  if (emptied(10) == 55) {
    ok += 10;
  }
  return ok;
}
`

// The cursor of a while loop borrows the list that the function keeps
// (ssaunits.borrows_entry). The walks are inlined in probe and in bagged,
// which own the list: they retain nothing. The certifier leaves the unit
// with the list, because nothing spends the cursor (rccertify.spent_phis). A
// cursor that leaves its loop, that goes into a box, or whose list a cell
// held still gets a count: the census is balanced.
func TestSelfHostWhileWalkOfAKeptListBorrows(t *testing.T) {
	h := selfHostCLIForHost(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "walk.fern")
	if err := os.WriteFile(src, []byte(whileWalkProg), 0o644); err != nil {
		t.Fatal(err)
	}
	retains := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`(?m)^\s+addl \$1, -8\(`),
		"arm64-linux":  regexp.MustCompile(`(?m)^\s+add w\d+, w\d+, #1\n\s+stur w\d+, \[x\d+, #-8\]`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		asm, err := os.ReadFile(cli.emit(t, src, target))
		if err != nil {
			t.Fatal(err)
		}
		text := string(asm)
		for _, fn := range []string{"probe", "bagged"} {
			at := strings.Index(text, "\n__fn_"+fn+".r:\n")
			if at < 0 {
				t.Fatalf("%s: no %s in the listing", target, fn)
			}
			body := text[at:]
			if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
				body = body[:end]
			}
			if strings.Contains(body, "__fn_sum") || strings.Contains(body, "__fn_count") || strings.Contains(body, "__fn_bag_sum") {
				t.Errorf("%s: %s calls a walk, so the walk is not a loop in it:\n%s", target, fn, body)
			}
			if m := retains[target].FindString(body); m != "" {
				t.Errorf("%s: %s retains the list that it owns (%q):\n%s", target, fn, strings.TrimSpace(m), body)
			}
		}
	}
	// Four rounds of probe and mixed, and one each of gone, bagged and emptied.
	const want = 4*(1+2) + 10 + 10 + 10
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != want {
			t.Errorf("%s: exit %d, want %d", tg.target, got, want)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		stderr, code := cli.exitOf(t, whileWalkProg, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
		if code != want {
			t.Fatalf("%s: exit %d, want %d\n%s", target, code, want, stderr)
		}
		allocs, frees, live := parseLeakcheck(t, "while-walk", stderr)
		if allocs != frees || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
		}
	}
}
