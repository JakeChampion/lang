package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `grow` builds each node again with the colour that its arm tested, so the
// word that holds the colour and the key does not change. `flip` changes the
// colour. `main` keeps the tree before each change, so each function also
// runs on a node that has two counts.
const testedFieldProg = `enum Color { Red, Black }

enum Tree { Leaf, Node(Color, Tree, i32, boolean, Tree) }

@noinline function grow(t: Tree, k: i32): Tree {
    return match (t) {
        Node(Red(), l, kx, vx, r) => Node(Red, grow(l, k), kx, vx, r),
        Node(Black(), l, kx, vx, r) => Node(Black, l, kx, vx, grow(r, k)),
        Leaf => Node(Red, Leaf, k, k % 2 == 0, Leaf)
    };
}

@noinline function flip(t: Tree): Tree {
    return match (t) {
        Node(Red(), l, kx, vx, r) => Node(Black, flip(l), kx, vx, r),
        Node(Black(), l, kx, vx, r) => Node(Red, l, kx, vx, flip(r)),
        Leaf => Leaf
    };
}

@noinline function sum(t: Tree, depth: i64): i64 {
    return match (t) {
        Leaf => depth,
        Node(c, l, k, v, r) => {
            let colour: i64 = match (c) { Red() => 3i64, Black() => 5i64 };
            let flag: i64 = if (v) { 7i64 } else { 11i64 };
            sum(l, depth + 1i64) * 31i64 + sum(r, depth + 2i64) * 17i64 + (k as i64) * colour + flag
        }
    };
}

function main(): i32 {
    let t: Tree = Leaf;
    let total: i64 = 0i64;
    let i: i32 = 0;
    while (i < 40) {
        let before = t;
        t = grow(before, i);
        total = total * 3i64 + sum(before, 0i64) % 1000003i64 + sum(t, 0i64) % 1000003i64;
        if (i % 3 == 0) {
            let kept = t;
            t = flip(kept);
            total += sum(kept, 0i64) % 1000003i64;
        }
        if (i % 5 == 0) {
            t = flip(t);
        }
        total = total % 1000000007i64;
        i += 1;
    }
    if (total != 395344369i64) { return 1; }
    return 42;
}
`

func TestSelfHostTestedFieldKeepsItsWord(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "tested.fern")
	if err := os.WriteFile(src, []byte(testedFieldProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "tested.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	grow := testedFieldFunction(t, string(asm), "grow")
	if strings.Contains(grow, "andq $-256") {
		t.Errorf("grow builds the word again, and each arm tested the colour that it writes:\n%s", grow)
	}
	unique := strings.Index(grow, "cmpl $1, -8(")
	word := regexp.MustCompile(`movq 0\(%r\w+\), %r\w+`).FindStringIndex(grow)
	if unique < 0 || word != nil && word[0] < unique {
		t.Errorf("grow loads the word before it tests the count, and only a node with two counts reads the word:\n%s", grow)
	}
	if flip := testedFieldFunction(t, string(asm), "flip"); !strings.Contains(flip, "andq $-256") {
		t.Errorf("flip changes the colour and does not build the word again:\n%s", flip)
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}

func testedFieldFunction(t *testing.T, asm, name string) string {
	t.Helper()
	loc := regexp.MustCompile(`(?m)^__fn_(\w*__)?` + name + `\.r:\n`).FindStringIndex(asm)
	if loc == nil {
		t.Fatalf("no function %s in the listing", name)
	}
	body := asm[loc[1]:]
	if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
		body = body[:end]
	}
	return body
}
