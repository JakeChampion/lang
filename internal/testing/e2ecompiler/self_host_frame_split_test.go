package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// count returns from a leaf with no call. fib returns from its guard, and its
// parameter lives across its calls. tally returns early, and then runs a loop
// that calls. deep has an early return after a test of a second parameter.
const frameSplitProg = `enum Expr { Val(i64), Var(i64), Add(Expr, Expr), Neg(Expr) }

@noinline function count(e: Expr): i64 {
  return match (e) { Val(_) | Var(_) => 1i64, Add(a, b) => count(a) + count(b), Neg(a) => count(a) };
}

@noinline function fib(n: i64): i64 {
  if (n < 2i64) {
    return n;
  }
  return fib(n - 1i64) + fib(n - 2i64);
}

@noinline function tally(n: i64, e: Expr): i64 {
  if (n <= 0i64) {
    return 0i64;
  }
  let total = 0i64;
  let i = 0i64;
  while (i < n) {
    total += count(e);
    i += 1i64;
  }
  return total;
}

@noinline function deep(e: Expr, limit: i64): i64 {
  if (limit == 0i64) {
    return 7i64;
  }
  match (e) {
    Val(v) => {
      return v;
    },
    Var(_) => {
      return 3i64;
    },
    Add(a, b) => {
      return deep(a, limit - 1i64) + deep(b, limit - 1i64);
    },
    Neg(a) => {
      return 0i64 - deep(a, limit - 1i64);
    }
  }
}

@noinline function build(n: i64): Expr {
  if (n == 0i64) {
    return Val(2i64);
  }
  if (n % 3i64 == 0i64) {
    return Neg(build(n - 1i64));
  }
  return Add(build(n - 1i64), Var(n));
}

function main(): i32 {
  let e = build(30i64);
  let ok = fib(20i64) == 6765i64 && count(e) == 21i64 && tally(5i64, e) == 105i64 && tally(0i64, e) == 0i64 && deep(e, 100i64) == 2i64 && deep(e, 2i64) == 0i64 - 14i64;
  return if (ok) { 42 } else { 1 };
}
`

// A function whose frame only saves registers makes the frame on the paths
// that need it (ssa.frame_split). A leaf of count and the guard of fib return
// with no save. The parameter of fib waits in the register of its argument.
func TestSelfHostFrameSplit(t *testing.T) {
	h := selfHostCLIForHost(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "split.fern")
	if err := os.WriteFile(src, []byte(frameSplitProg), 0o644); err != nil {
		t.Fatal(err)
	}
	save := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`(?m)^\s+pushq `),
		"arm64-linux":  regexp.MustCompile(`(?m)^\s+stp x29, x30,`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		asm, err := os.ReadFile(cli.emit(t, src, target))
		if err != nil {
			t.Fatal(err)
		}
		text := string(asm)
		for _, fn := range []string{"count", "fib"} {
			at := strings.Index(text, "\n__fn_"+fn+".r:\n")
			if at < 0 {
				t.Fatalf("%s: no %s in the listing", target, fn)
			}
			body := text[at:]
			if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
				body = body[:end]
			}
			ret := regexp.MustCompile(`(?m)^\s+ret$`).FindStringIndex(body)
			if ret == nil {
				t.Fatalf("%s: %s has no return:\n%s", target, fn, body)
			}
			if save[target].MatchString(body[:ret[0]]) {
				t.Errorf("%s: %s makes its frame before its first return:\n%s", target, fn, body)
			}
			if n := len(save[target].FindAllString(body, -1)); n == 0 {
				t.Errorf("%s: %s makes no frame for its calls:\n%s", target, fn, body)
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
		stderr, code := cli.exitOf(t, frameSplitProg, target, "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
		if code != 42 {
			t.Fatalf("%s: exit %d, want 42\n%s", target, code, stderr)
		}
		allocs, frees, live := parseLeakcheck(t, "frame-split", stderr)
		if allocs != frees || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", target, allocs, frees, live)
		}
	}
}
