package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const ownReusableProgram = `enum List { Nil, Cons(i64, List) }

// Nothing here stores or returns a part of xs. The loop takes each cell
// apart and builds a cell of the same type, so the frame owns the list and
// each cell supplies the construction of its turn.
@noinline function bump_rev(xs: List): List {
    let acc: List = Nil;
    let cur: List = xs;
    let go: boolean = true;
    while (go) {
        match (cur) {
            Cons(y, more) => { acc = Cons(y + 1i64, acc); cur = more; },
            Nil => { go = false; },
        }
    }
    return acc;
}

// This function builds nothing, so it only reads its list.
@noinline function sum(xs: List, t: i64): i64 {
    return match (xs) { Cons(x, r) => sum(r, t + x), Nil => t };
}

@noinline function count_up(n: i64): List {
    let acc: List = Nil;
    let i: i64 = 0i64;
    while (i < n) { acc = Cons(i, acc); i = i + 1i64; }
    return acc;
}

function main(): i32 {
    let kept: List = count_up(6i64);
    let shared: i64 = sum(bump_rev(kept), 0i64);
    let alone: i64 = sum(bump_rev(bump_rev(count_up(6i64))), 0i64);
    return (shared + alone + sum(kept, 0i64)) as i32;
}
`

// A function owns a value that it takes apart when it also builds a variant
// of the same type, so the box of the value can supply the construction.
func TestSelfHostOwnsABoxItCanReuse(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "own.fern")
	if err := os.WriteFile(src, []byte(ownReusableProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		out, err := exec.Command(h.cli, "-g", "-target", tg.target, "-array-report", src, h.stdlib).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: -array-report: %v\n%s", tg.target, err, out)
		}
		_, rows, ok := strings.Cut(string(out), "array report: primary Fern compiler, constructor storage\n")
		if !ok {
			t.Fatalf("%s: no constructor storage section:\n%s", tg.target, out)
		}
		lines := strings.Split(rows, "\n")
		found := false
		for i := 0; i+1 < len(lines); i++ {
			if lines[i] == "bump_rev: constructor; ownership lowering" && strings.Contains(lines[i+1], "stage=Cons@") {
				found = true
				if !strings.Contains(lines[i+1], "(cur)") || !strings.Contains(lines[i+1], "reason=reuse") {
					t.Errorf("%s: the cell of cur does not supply the construction of bump_rev: %q", tg.target, lines[i+1])
				}
			}
		}
		if !found {
			t.Errorf("%s: no construction row for bump_rev:\n%s", tg.target, rows)
		}
		bin := filepath.Join(dir, "own-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 63 {
			t.Errorf("%s: exit = %d, want 63 (21 for the list held by main, 27 for the list given away, 15 for the list that main keeps)", tg.target, code)
		}
	}
}
