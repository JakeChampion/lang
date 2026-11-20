package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const carryPastSuppliedProgram = `enum List { Nil, Cons(i64, List) }

function partition(p: i64, xs: List, lo: List, hi: List): (List, List) {
    match (xs) {
        Nil => { return (lo, hi); },
        Cons(x, rest) => {
            if (x < p) { return partition(p, rest, Cons(x, lo), hi); }
            return partition(p, rest, lo, Cons(x, hi));
        },
    }
}

@noinline function append(xs: List, ys: List): List {
    return match (xs) { Cons(x, rest) => Cons(x, append(rest, ys)), Nil => ys };
}

// The compiler puts the loop of partition between the match of xs and
// Cons(p, ...). That loop builds cells from the cells of its own list.
@noinline function sort(xs: List): List {
    match (xs) {
        Nil => { return Nil; },
        Cons(p, rest) => {
            let (lo, hi) = partition(p, rest, Nil, Nil);
            return append(sort(lo), Cons(p, sort(hi)));
        },
    }
}

@noinline function count_down(n: i64): List {
    if (n == 0i64) { return Nil; }
    return Cons((n * 7i64) % 11i64, count_down(n - 1i64));
}

@noinline function digest(xs: List, i: i64, total: i64): i64 {
    return match (xs) { Cons(x, rest) => digest(rest, i + 1i64, total + i * x), Nil => total };
}

function main(): i32 {
    let kept: List = count_down(9i64);
    return ((digest(sort(kept), 1i64, 0i64) + digest(sort(count_down(6i64)), 1i64, 0i64) + digest(kept, 1i64, 0i64)) % 251i64) as i32;
}
`

// The cell that sort matched supplies Cons(p, ...), though a loop that builds
// cells of the same size is between them: each construction of that loop has
// its own cell, so the token of sort goes past it.
func TestSelfHostReuseTokenPassesSuppliedLoop(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "carry.fern")
	if err := os.WriteFile(src, []byte(carryPastSuppliedProgram), 0o644); err != nil {
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
		reused, fresh := 0, 0
		for i := 0; i+1 < len(lines); i++ {
			if lines[i] != "sort: constructor; ownership lowering" {
				continue
			}
			if strings.Contains(lines[i+1], "reason=reuse") {
				reused++
			}
			if strings.Contains(lines[i+1], "reason=no-donor") {
				fresh++
			}
		}
		if reused != 3 || fresh != 0 {
			t.Errorf("%s: sort has %d constructions with a donor and %d with none, want 3 and 0:\n%s", tg.target, reused, fresh, rows)
		}
		bin := filepath.Join(dir, "carry-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 245 {
			t.Errorf("%s: exit = %d, want 245", tg.target, code)
		}
	}
}
