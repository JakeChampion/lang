package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A capture-free closure passed to a function binds the parameter in a clone
// of the callee, so the calls through it are direct and the callee is
// spliced. The capturing closure and the closure kept in a record stay
// indirect.
const calleeSpecialiseSrc = `import "core/cmp";

struct Box { f: (i32) => i32, n: i32 }

function twice(x: i32): i32 {
  return x * 2;
}

function inc(x: i32): i32 {
  return x + 1;
}

function by_len(a: string, b: string): i32 {
  return a.len() - b.len();
}

@noinline function step[T](x: T, f: (T) => T): T {
  return f(x);
}

@noinline function map_all[T](own xs: T[], f: (T) => T): T[] {
  let i: i32 = 0;
  while (i < xs.len()) {
    xs = xs.with(i, step(xs[i], f));
    i = i + 1;
  }
  return xs;
}

@noinline function fold(xs: i32[], i: i32, acc: i32, f: (i32) => i32): i32 {
  if (i >= xs.len()) {
    return acc;
  }
  return fold(xs, i + 1, acc + f(xs[i]), f);
}

@noinline function boxed(f: (i32) => i32, n: i32): Box {
  return Box { f: f, n: f(n) };
}

function main(): i32 {
  let xs: i32[] = [3, 1, 2];
  xs = map_all(xs, twice);
  xs = map_all(xs, inc);
  if (xs[0] != 7 || xs[1] != 3 || xs[2] != 5) {
    return 1;
  }
  if (fold(xs, 0, 0, twice) != 30) {
    return 2;
  }
  let k: i32 = xs.len() + 7;
  let add_k = (x: i32): i32 => x + k;
  if (fold(xs, 0, 0, add_k) != 45) {
    return 3;
  }
  let b: Box = boxed(inc, 4);
  if (b.n != 5 || b.f(b.n) != 6) {
    return 4;
  }
  let ys: i32[] = cmp.sort_in_place([5, 9, 1, 7]);
  if (ys[0] != 1 || ys[3] != 9) {
    return 5;
  }
  let zs: string[] = cmp.sort_desc_in_place(["pear", "fig", "apple"]);
  if (zs[0] != "pear" || zs[2] != "apple") {
    return 6;
  }
  let ws: string[] = cmp.sort_by_in_place(["ccc", "a", "bb"], by_len);
  if (ws[0] != "a" || ws[2] != "ccc") {
    return 7;
  }
  return 0;
}
`

var asmFunc = regexp.MustCompile(`^(__fn_[^ ]*):$`)

// indirectCallers names each function of an x86-64 listing that calls
// through a register or memory operand.
func indirectCallers(asm string) []string {
	var out []string
	fn := ""
	for _, line := range strings.Split(asm, "\n") {
		if m := asmFunc.FindStringSubmatch(line); m != nil {
			fn = strings.TrimSuffix(m[1], ".r")
		}
		if strings.Contains(line, "call *") && (len(out) == 0 || out[len(out)-1] != fn) {
			out = append(out, fn)
		}
	}
	return out
}

func TestSelfHostCalleeSpecialisation(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, calleeSpecialiseSrc, target, "FERN_STRICT_IR=1"); code != 0 {
				t.Fatalf("exit %d (the number names the failed check in main)\n%s", code, stderr)
			}
			san := *cli
			san.flags = []string{"-sanitize"}
			stderr, code := san.exitOf(t, calleeSpecialiseSrc, target)
			if m := lentViewLeak.FindStringSubmatch(stderr); code != 0 || len(m) != 3 || m[1] != m[2] {
				t.Fatalf("-sanitize: exit %d, want 0 and a balanced census\n%s", code, stderr)
			}
		})
	}
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(calleeSpecialiseSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, clone := range []string{
		"__fn_map_all__i32$twice$wrap$1:", "__fn_map_all__i32$inc$wrap$1:",
		"__fn_fold$twice$wrap$3:", "__fn_boxed$inc$wrap$0:",
		"__fn_cmp____cmp_sift_down__string$by_len$wrap$3:",
	} {
		if !strings.Contains(string(asm), "\n"+clone+"\n") {
			t.Errorf("no clone %s in the listing", strings.TrimSuffix(clone, ":"))
		}
	}
	if got, want := strings.Join(indirectCallers(string(asm)), " "), "__fn_fold __fn_main"; got != want {
		t.Errorf("functions with an indirect call: %q, want %q (fold with the capturing closure, main with the record field)", got, want)
	}
}
