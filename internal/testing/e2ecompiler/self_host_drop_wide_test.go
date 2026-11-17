package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Node has two waiting fields of its own type, and Quad has three. Each
// field of each variant is the spine of a tree of 1,000,000 levels in turn. A
// drop that calls itself for each level of such a spine needs more than the
// 8 MB of a machine stack.
const dropWideProg = `enum Tri { Leaf, Node(Tri, Tri, i64, Tri), Tip(i64), Quad(Tri, Tri, Tri, Tri), Two(Tri, i64, Tri) }

@noinline function spine(n: i64, side: i64, other: Tri): Tri {
    let t: Tri = Leaf;
    let i: i64 = 0i64;
    while (i < n) {
        let o: Tri = if (i % 4i64 == 0i64) { Tip(i) } else { other };
        if (side == 0i64) {
            t = Node(t, o, i, o);
        } else if (side == 1i64) {
            t = Node(o, t, i, o);
        } else if (side == 2i64) {
            t = Node(o, o, i, t);
        } else if (side == 3i64) {
            t = Quad(t, o, o, o);
        } else if (side == 4i64) {
            t = Quad(o, o, t, o);
        } else if (side == 5i64) {
            t = Two(t, i, o);
        } else {
            t = Two(o, i, t);
        }
        i += 1i64;
    }
    return t;
}

@noinline function full(depth: i64, k: i64): Tri {
    if (depth == 0i64) {
        return Tip(k);
    }
    if (depth % 2i64 == 0i64) {
        return Quad(full(depth - 1i64, k), full(depth - 1i64, k + 1i64), full(depth - 1i64, k + 2i64), full(depth - 1i64, k + 3i64));
    }
    return Node(full(depth - 1i64, k), full(depth - 1i64, k + 1i64), k, full(depth - 1i64, k + 2i64));
}

@noinline function size(t: Tri, acc: i64): i64 {
    return match (t) {
        Leaf => acc,
        Tip(_) => acc + 1i64,
        Node(a, b, _, c) => size(c, size(b, size(a, acc + 1i64))),
        Quad(a, b, c, d) => size(d, size(c, size(b, size(a, acc + 1i64)))),
        Two(a, _, b) => size(b, size(a, acc + 1i64))
    };
}

@noinline function tag(t: Tri): i64 {
    return match (t) { Leaf => 0i64, Tip(_) => 1i64, Node(_, _, _, _) => 2i64, Quad(_, _, _, _) => 3i64, Two(_, _, _) => 4i64 };
}

function main(): i32 {
    let total = 0i64;
    for round in 0..3 {
        let shared = full(3i64, 1i64);
        for side in 0..7 {
            let a = spine(200i64, side as i64, shared);
            total += size(a, 0i64);
        }
        let kept = full(4i64, 3i64);
        let c = Quad(kept, kept, Tip(2i64), kept);
        total += size(c, 0i64) + size(kept, 0i64);
    }
    for side in 0..7 {
        let deep = spine(1000000i64, side as i64, Leaf);
        total += tag(deep);
    }
    return if (total == 336434i64) { 42 } else { 1 };
}
`

// A node with three or four fields of its own type waits on one stack for
// each waiting field, so the drop of Tri does not call itself.
func TestSelfHostDropOfAWideNodeHasNoCall(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "wide.fern")
	if err := os.WriteFile(src, []byte(dropWideProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		cmd := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", out, src, h.stdlib)
		if combined, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("emitting %s: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		loc := regexp.MustCompile(`(?m)^__fn___sem_drop_\w*Tri\.r:\n`).FindIndex(asm)
		if loc == nil {
			t.Fatalf("%s: no drop helper for Tri in the listing", target)
		}
		body := string(asm[loc[1]:])
		for _, end := range []string{".cfi_endproc", ".ltorg"} {
			if at := strings.Index(body, end); at >= 0 {
				body = body[:at]
			}
		}
		if m := regexp.MustCompile(`(?m)^\s+(call|bl) __fn___sem_drop_.*$`).FindString(body); m != "" {
			t.Errorf("%s: the drop of Tri calls a drop helper (%q):\n%s", target, strings.TrimSpace(m), body)
		}
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
		stderr, code := cli.exitOf(t, dropWideProg, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
		if code != 42 {
			t.Fatalf("%s: exit %d, want 42\n%s", target, code, stderr)
		}
		allocs, frees, live := parseLeakcheck(t, "drop-wide", stderr)
		if allocs != frees || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
		}
	}
}
