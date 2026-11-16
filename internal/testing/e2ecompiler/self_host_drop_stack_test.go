package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Node has a waiting field and a next field of its own type. The trees share
// subtrees, one node holds the same subtree twice, and one left spine is
// 200,000 nodes deep. A drop that calls itself for each level of that spine
// runs out of stack on wasm32.
const dropStackProg = `enum Tree { Leaf, Node(Tree, i64, Tree), Tip(i64) }

@noinline function spine(n: i64, right: Tree): Tree {
    let t: Tree = Leaf;
    let i: i64 = 0i64;
    while (i < n) {
        if (i % 4i64 == 0i64) {
            t = Node(t, i, Tip(i));
        } else {
            t = Node(t, i, right);
        }
        i += 1i64;
    }
    return t;
}

@noinline function full(depth: i64, k: i64): Tree {
    if (depth == 0i64) {
        return Tip(k);
    }
    return Node(full(depth - 1i64, k * 2i64), k, full(depth - 1i64, k * 2i64 + 1i64));
}

@noinline function twice(t: Tree): Tree {
    return Node(t, 7i64, t);
}

@noinline function left_depth(t: Tree): i64 {
    let n = 0i64;
    let cur = t;
    while (true) {
        match (cur) {
            Node(l, _, _) => {
                n += 1i64;
                cur = l;
            },
            _ => {
                return n;
            }
        }
    }
    return n;
}

@noinline function size(t: Tree, acc: i64): i64 {
    return match (t) { Leaf => acc, Tip(_) => acc + 1i64, Node(l, _, r) => size(r, size(l, acc + 1i64)) };
}

function main(): i32 {
    let total = 0i64;
    for round in 0..10 {
        let shared = full(3i64, 1i64);
        let a = spine(2000i64, shared);
        let b = spine(300i64, shared);
        let both = twice(full(4i64, 1i64));
        let kept = full(5i64, 3i64);
        let c = Node(kept, 1i64, Tip(2i64));
        total += size(a, 0i64) + size(b, 0i64) + size(both, 0i64) + size(c, 0i64) + size(kept, 0i64);
    }
    let deep = spine(200000i64, Leaf);
    total += left_depth(deep) % 7i64;
    return if (total == 10i64 * (2000i64 + 500i64 * 1i64 + 1500i64 * 15i64 + 300i64 + 75i64 + 225i64 * 15i64 + 1i64 + 62i64 + 1i64 + 63i64 + 1i64 + 63i64) + 200000i64 % 7i64) { 42 } else { 1 };
}
`

// The drop of Tree keeps its waiting nodes in a stack in the dropped nodes,
// so it does not call itself.
func TestSelfHostDropKeepsAStackInItsNodes(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "stack.fern")
	if err := os.WriteFile(src, []byte(dropStackProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "stack.s")
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
	if strings.Contains(body, "call __fn___sem_drop_") {
		t.Errorf("the drop of Tree calls a drop helper:\n%s", body)
	}
	// A box that a test of the drop proved unique is freed with no test of
	// its count: the childless child, the cell that the drop pops and the node
	// of the loop.
	lines := strings.Split(body, "\n")
	sole := 0
	for i, line := range lines {
		if strings.TrimSpace(line) != "call __fn___fern_box_dec.u" {
			continue
		}
		tested := false
		for j := i - 1; j >= 0 && j >= i-4; j-- {
			tested = tested || strings.HasPrefix(strings.TrimSpace(lines[j]), "js ")
		}
		if !tested {
			sole++
		}
	}
	if sole < 3 {
		t.Errorf("the drop of Tree frees %d unique boxes with no count test, want 3 or more:\n%s", sole, body)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		stderr, code := cli.exitOf(t, dropStackProg, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
		if code != 42 {
			t.Fatalf("%s: exit %d, want 42\n%s", target, code, stderr)
		}
		allocs, frees, live := parseLeakcheck(t, "drop-stack", stderr)
		if allocs != frees || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
		}
	}
}
