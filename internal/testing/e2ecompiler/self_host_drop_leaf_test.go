package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Num has no field that holds a count, so the drop of E does not call the
// drop helper for a unique Num. Pair has two waiting fields of its own type,
// so a Pair waits on a stack and the drop of E has no call. Kids keeps its
// next nodes in an array, so the drop of G calls itself for its first field.
// F has a drop, and its drop of each node runs it, the next node of the chain
// too.
const dropLeafProg = `import "core/mem";

enum E { Num(i64), Pair(E, E, E), Name(string, E), Nil }

enum G { Val(i64), Kids(G, G[]), End }

enum F { Leaf(i64), Two(F, F) }

impl mem.Drop for F {
    function drop(self: Self): void {
        match (self) {
            Leaf(n) => { print("leaf " + n.to_string()); },
            Two(_, _) => { print("two"); }
        }
    }
}

@noinline function build(n: i64): E {
    if (n <= 0i64) {
        return Nil;
    }
    if (n % 3i64 == 0i64) {
        return Name("n" + n.to_string(), build(n - 1i64));
    }
    return Pair(Num(n), Nil, build(n - 1i64));
}

@noinline function sum(e: E): i64 {
    return match (e) { Num(n) => n, Pair(a, b, c) => sum(a) + sum(b) + sum(c), Name(_, b) => sum(b) + 1i64, Nil => 0i64 };
}

@noinline function kids(n: i64): G {
    if (n <= 0i64) {
        return End;
    }
    return Kids(Val(n), [kids(n - 1i64), Val(n)]);
}

@noinline function count(g: G): i64 {
    return match (g) { Val(n) => n, Kids(a, rest) => count(a) + rest.len() as i64, End => 0i64 };
}

@noinline function grow(n: i64): F {
    if (n <= 0i64) {
        return Leaf(7i64);
    }
    return Two(Leaf(n), grow(n - 1i64));
}

function main(): i32 {
    let total = 0i64;
    for i in 0..50 {
        let e = build(30i64);
        let kept = build(4i64);
        let both = Pair(kept, Nil, Pair(kept, Num(1i64), Nil));
        total += sum(e) + sum(both);
    }
    let g = kids(5i64);
    total += count(g) - 7i64;
    let f = grow(2i64);
    return if (total == 50i64 * 327i64) { 42 } else { 1 };
}
`

func TestSelfHostDropSkipsAChildlessBox(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "leaf.fern")
	if err := os.WriteFile(src, []byte(dropLeafProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "leaf.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	helper := func(name string) string {
		loc := regexp.MustCompile(`(?m)^__fn___sem_drop_\w*` + name + `\.r:\n`).FindIndex(asm)
		if loc == nil {
			t.Fatalf("no drop helper for %s in the listing", name)
		}
		body := string(asm[loc[1]:])
		if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
			body = body[:end]
		}
		return body
	}
	if body := helper("E"); strings.Contains(body, "call __fn___sem_drop_") {
		t.Errorf("the drop of E calls a drop helper:\n%s", body)
	}
	body := helper("G")
	down := strings.Index(body, "call __fn___sem_drop_")
	if down < 0 {
		t.Fatalf("the drop of G does not call itself:\n%s", body)
	}
	before := body[:down]
	unique := strings.LastIndex(before, "cmpl $1, -8(%r")
	shape := regexp.MustCompile(`-4\(%r\w+\)`).FindAllStringIndex(before, -1)
	if unique < 0 || len(shape) == 0 || shape[len(shape)-1][0] < unique {
		t.Errorf("the drop of G calls itself for a unique child with no test of its shape:\n%s", body)
	}
	want := "two\nleaf 2\ntwo\nleaf 1\nleaf 7\n"
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if stdout, got := h.runProduced(t, tg, bin); got != 42 || stdout != want {
			t.Errorf("%s: exit %d, want 42; output %q, want %q", tg.target, got, stdout, want)
		}
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		stderr, code := cli.exitOf(t, dropLeafProg, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
		if code != 42 {
			t.Fatalf("%s: exit %d, want 42\n%s", target, code, stderr)
		}
		allocs, frees, live := parseLeakcheck(t, "drop-leaf", stderr)
		if allocs != frees || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
		}
	}
}
