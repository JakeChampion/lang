package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The trees share subtrees, and one node holds the same subtree twice, so the
// drop meets a next node with one count, with many counts, and with a count
// that the drop of the other field lowers to one. Stmt continues through the
// last element of an array.
const dropChainProg = `enum Tree { Leaf, Node(Tree, i64, Tree), Tagged(string, Tree) }

@noinline function spine(n: i64, right: Tree): Tree {
    let t: Tree = Leaf;
    let i: i64 = 0i64;
    while (i < n) {
        if (i % 3i64 == 0i64) {
            t = Tagged("k" + i.to_string(), t);
        } else {
            t = Node(t, i, right);
        }
        i += 1i64;
    }
    return t;
}

@noinline function twice(t: Tree): Tree {
    return Node(t, 7i64, t);
}

@noinline function size(t: Tree, acc: i64): i64 {
    return match (t) { Leaf => acc, Node(l, _, r) => size(r, size(l, acc + 1i64)), Tagged(_, r) => size(r, acc + 1i64) };
}

enum Stmt { Atom(string), Block(string, Stmt[]), Seq(Stmt, string, Stmt) }

@noinline function nest(n: i32): Stmt {
    let c: Stmt = Atom("z");
    for i in 0..n {
        if (i % 2 == 0) {
            c = Block("a" + i.to_string(), [Atom("p"), c]);
        } else {
            c = Seq(Atom("q"), "b" + i.to_string(), c);
        }
    }
    return c;
}

@noinline function count(c: Stmt): i64 {
    return match (c) {
        Atom(_) => 1i64,
        Block(_, xs) => {
            let n = 1i64;
            for x in xs {
                n += count(x);
            }
            n
        },
        Seq(a, _, b) => count(a) + count(b) + 1i64
    };
}

function main(): i32 {
    if (count(nest(200)) != 401i64) { return 2; }
    let total: i64 = 0i64;
    let round: i64 = 0i64;
    while (round < 20i64) {
        let shared = spine(5i64, Leaf);
        let a = spine(40i64, shared);
        let b = spine(30i64, shared);
        let both = twice(spine(4i64, Leaf));
        total += size(a, 0i64) + size(b, 0i64) + size(both, 0i64);
        round += 1i64;
    }
    if (total != 20i64 * (40i64 + 26i64 * 5i64 + 30i64 + 20i64 * 5i64 + 9i64)) { return 1; }
    return 42;
}
`

func TestSelfHostDropReadsTheNextNodeFirst(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "chain.fern")
	if err := os.WriteFile(src, []byte(dropChainProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "chain.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	loc := regexp.MustCompile(`(?m)^__fn___sem_drop_\w*Tree\.r:\n`).FindIndex(asm)
	if loc == nil {
		t.Fatal("no drop helper for Tree in the listing")
	}
	body := string(asm[loc[1]:])
	if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
		body = body[:end]
	}
	next := regexp.MustCompile(`movq 16\(%r\w+\), %r\w+`).FindStringIndex(body)
	down := strings.Index(body, "call __fn___sem_drop_")
	if next == nil || down < 0 || next[0] > down {
		t.Errorf("the drop of Tree goes down a field before it reads the next node:\n%s", body)
	}
	if regexp.MustCompile(`call __fn___sem_release_\w*Tree`).MatchString(body) {
		t.Errorf("the drop of Tree releases a Tree field through the release helper, which is a second call for each level:\n%s", body)
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}
