package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const rebuiltVariantProgram = `enum List { Nil, Cons(i64, List) }

// Each arm builds again the list that it matched: Cons(x, xt) is xs, and
// Cons(y, yt) is ys.
@noinline function merge(xs: List, ys: List): List {
    match (xs) {
        Nil => { return ys; },
        Cons(x, xt) => {
            match (ys) {
                Nil => { return Cons(x, xt); },
                Cons(y, yt) => {
                    if (x <= y) { return Cons(x, merge(xt, Cons(y, yt))); }
                    return Cons(y, merge(Cons(x, xt), yt));
                },
            }
        },
    }
}

// The payloads are in another order, so this is a new value.
@noinline function swapped(p: Pair): Pair {
    return match (p) { Two(a, b) => Two(b, a), One(a) => One(a) };
}

enum Pair { One(i64), Two(i64, i64) }

@noinline function count_up(from: i64, n: i64): List {
    if (n == 0i64) { return Nil; }
    return Cons(from, count_up(from + 2i64, n - 1i64));
}

@noinline function sum(xs: List, i: i64, total: i64): i64 {
    return match (xs) { Cons(x, rest) => sum(rest, i + 1i64, total + i * x), Nil => total };
}

function main(): i32 {
    let kept: List = count_up(1i64, 4i64);
    let merged: List = merge(kept, count_up(2i64, 4i64));
    let again: List = merge(kept, Nil);
    let s: i64 = match (swapped(Two(3i64, 4i64))) { Two(a, b) => a * 10i64 + b, One(a) => a };
    return (sum(merged, 1i64, 0i64) + sum(again, 1i64, 0i64) + sum(kept, 1i64, 0i64) + s) as i32;
}
`

// A construction that builds again the value that its arm matched is that
// value: the function allocates nothing for it and frees nothing.
func TestSelfHostRebuiltVariantIsItsSource(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "rebuilt.fern")
	if err := os.WriteFile(src, []byte(rebuiltVariantProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		out, err := exec.Command(h.cli, "-target", tg.target, "-array-report", src, h.stdlib).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: -array-report: %v\n%s", tg.target, err, out)
		}
		_, rows, ok := strings.Cut(string(out), "array report: primary Fern compiler, constructor storage\n")
		if !ok {
			t.Fatalf("%s: no constructor storage section:\n%s", tg.target, out)
		}
		lines := strings.Split(rows, "\n")
		built := map[string]int{}
		for i := 0; i+1 < len(lines); i++ {
			if owner, ok := strings.CutSuffix(lines[i], ": constructor; ownership lowering"); ok && !strings.Contains(lines[i+1], "reason=static") {
				built[owner]++
			}
		}
		// merge keeps Cons(x, merge(...)) and Cons(y, merge(...)). The three
		// constructions that build xs or ys again are gone.
		if built["merge"] != 2 {
			t.Errorf("%s: merge has %d constructions that are not static, want 2:\n%s", tg.target, built["merge"], rows)
		}
		if built["swapped"] != 1 {
			t.Errorf("%s: swapped has %d constructions that are not static, want 1 (Two(b, a); One(a) is p again):\n%s", tg.target, built["swapped"], rows)
		}
		bin := filepath.Join(dir, "rebuilt-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 91 {
			t.Errorf("%s: exit = %d, want 91", tg.target, code)
		}
	}
}
