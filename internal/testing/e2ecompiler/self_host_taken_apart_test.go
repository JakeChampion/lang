package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// left_of takes a record apart and head_of takes a variant apart. Each gives
// one field to its caller, and the box dies there. main gives head_of a box
// with one count, and a box with two counts.
const takenApartProg = `enum Item { Num(i64), Text(string) }

struct Pair { left: Item, right: Item, weight: i64 }

enum Shape { Dot, Line(Item, Item), Ring(Item) }

// The record dies here. Its left item goes to the caller.
@noinline function left_of(own p: Pair): Item {
  return p.left;
}

// The variant dies here. Its first item goes to the caller.
@noinline function head_of(own s: Shape): Item {
  return match (s) { Dot => Num(0i64), Line(a, _) => a, Ring(a) => a };
}

@noinline function value(it: Item): i64 {
  return match (it) { Num(n) => n, Text(t) => t.len() as i64 };
}

function main(): i32 {
  let total = 0i64;
  for i in 0..1000 {
    let p = Pair { left: Num(i as i64), right: Text("ab"), weight: 3i64 };
    total += value(left_of(p));
    let s = Line(Num(2i64), Num(i as i64));
    total += value(head_of(s));
    let kept = Ring(Num(1i64));
    let again = kept;
    total += value(head_of(kept)) + value(head_of(again));
  }
  return if (total == 499500i64 + 2000i64 + 2000i64) { 42 } else { 1 };
}
`

// A match or a field read that takes a box with one count apart frees the box
// itself. A record, and an enum whose variants are boxes, have 1 to 255
// words, so the free gives the block to its free list with no test of the
// size (ssarc.small_boxes).
func TestSelfHostTakenApartBoxGoesToItsFreeList(t *testing.T) {
	h := selfHostCLIForHost(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "apart.fern")
	if err := os.WriteFile(src, []byte(takenApartProg), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := map[string][2]string{
		"x86-64-linux": {"call __fn___fern_box_free.u\n", "call __fn___fern_box_dec.u\n"},
		"arm64-linux":  {"bl __fn___fern_box_free.u\n", "bl __fn___fern_box_dec.u\n"},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		asm, err := os.ReadFile(cli.emit(t, src, target))
		if err != nil {
			t.Fatal(err)
		}
		text := string(asm)
		for _, fn := range []string{"left_of", "head_of"} {
			at := strings.Index(text, "\n__fn_"+fn+".r:\n")
			if at < 0 {
				t.Fatalf("%s: no %s in the listing", target, fn)
			}
			body := text[at:]
			if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
				body = body[:end]
			}
			if !strings.Contains(body, calls[target][0]) {
				t.Errorf("%s: %s does not call the direct free:\n%s", target, fn, body)
			}
			if strings.Contains(body, calls[target][1]) {
				t.Errorf("%s: %s frees its box through the release that tests the size:\n%s", target, fn, body)
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
		stderr, code := cli.exitOf(t, takenApartProg, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
		if code != 42 {
			t.Fatalf("%s: exit %d, want 42\n%s", target, code, stderr)
		}
		allocs, frees, live := parseLeakcheck(t, "taken-apart", stderr)
		if allocs != frees || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
		}
	}
}
