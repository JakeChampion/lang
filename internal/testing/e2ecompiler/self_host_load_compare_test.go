package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// kind tests the tag of a box, wide tests a field against an immediate, and
// count tests a counter against the length of an array. Each value that the
// test reads goes to no other instruction.
const loadCompareProg = `enum Shape { Dot(i64), Line(i64, i64) }

@noinline function note(n: i32): i32 {
  return n + 1;
}

@noinline function kind(s: Shape): i32 {
  match (s) {
    Line(_, _) => {
      return note(6);
    },
    _ => {
      return 9;
    },
  }
}

@noinline function wide(s: Shape): i32 {
  match (s) {
    Line(_, m) => {
      if (m > 40i64) {
        return note(2);
      }
      return 0;
    },
    _ => {
      return 1;
    },
  }
}

@noinline function count(shapes: Shape[]): i32 {
  let n = 0;
  let i = 0;
  while (i < shapes.len()) {
    n += kind(shapes[i]);
    i += 1;
  }
  return n;
}

function main(): i32 {
  let shapes: Shape[] = [Line(1i64, 2i64), Dot(3i64), Line(4i64, 5i64)];
  if (count(shapes) != 23) {
    return 1;
  }
  if (wide(Line(1i64, 41i64)) != 3) {
    return 2;
  }
  if (wide(Line(1i64, 40i64)) != 0) {
    return 3;
  }
  if (wide(Line(1i64, -5000000000i64)) != 0) {
    return 4;
  }
  if (wide(Line(1i64, 5000000000i64)) != 3) {
    return 5;
  }
  if (wide(Dot(41i64)) != 1) {
    return 6;
  }
  return 0;
}
`

// On x86-64 a compare takes an operand from memory when the load of the
// operand is next to the compare and gives its value to no other instruction.
func TestSelfHostCompareReadsItsOperandFromMemory(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(loadCompareProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(asm)
	want := map[string]*regexp.Regexp{
		"kind":  regexp.MustCompile(`cmpl \$\d+, -4\(%r\w+\)`),
		"wide":  regexp.MustCompile(`cmpq \$40, 8\(%r\w+\)`),
		"count": regexp.MustCompile(`cmpq 0\(%r\w+\), %r\w+`),
	}
	for _, name := range []string{"kind", "wide", "count"} {
		body := tailCallBody(t, text, name)
		if !want[name].MatchString(body) {
			t.Errorf("%s has no compare that matches %s:\n%s", name, want[name], body)
		}
	}
	if body := tailCallBody(t, text, "kind"); strings.Contains(body, "movl -4(") {
		t.Errorf("kind loads the tag to a register:\n%s", body)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, env := range []string{"", "FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
			if stderr, code := cli.exitOf(t, loadCompareProg, target, env); code != 0 {
				t.Errorf("%s %s: exit %d, want 0\n%s", target, env, code, stderr)
			}
		}
	}
}

// The box of `inner` is loaded to %rcx, and `k` is in a frame slot. The
// compare reads `limit` from the box, so `k` must go to a different register.
const loadCompareSpilledProg = `struct Inner { used: i64, limit: i64, a: i64, b: i64, c: i64, d: i64 }
struct Outer { inner: Inner, pad: i64 }

@noinline function note(n: i64): i64 {
  return n + 1i64;
}

// Six values live across the calls take the saved registers and one frame
// slot. Six more are live at the compare, so the box of inner is in %rcx.
@noinline function over(o: Outer, a: i64, b: i64, c: i64, d: i64, e: i64): i64 {
  let k = note(a);
  let p = note(b) + a;
  let q = note(c) + b;
  let r = note(d) + c;
  let t = note(e) + d;
  let u = note(k) + e;
  let v1 = p * 3i64;
  let v2 = q * 5i64;
  let v3 = r * 7i64;
  let v4 = t * 11i64;
  let v5 = u * 13i64;
  let v6 = a * 17i64;
  if (o.inner.limit > k) {
    return v1 + v2 + v3 + v4 + v5 + v6 + p + q + r + t + u;
  }
  return v1 - v2 + v3 - v4 + v5 - v6 + p + q + r + t + u + k;
}

function main(): i32 {
  let o = Outer { inner: Inner { used: 1i64, limit: 5i64, a: 0i64, b: 0i64, c: 0i64, d: 0i64 }, pad: 0i64 };
  let x = over(o, 1i64, 2i64, 3i64, 4i64, 5i64);
  let y = over(o, 9i64, 2i64, 3i64, 4i64, 5i64);
  print(x.to_string() + " " + y.to_string());
  return 0;
}
`

func TestSelfHostCompareFromMemoryKeepsItsBase(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(loadCompareSpilledProg), 0o644); err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := tailCallBody(t, string(asm), "over")
	if !regexp.MustCompile(`cmpq %rdx, 8\(%rcx\)`).MatchString(body) {
		t.Errorf("over does not compare a register with the field of the box in %%rcx:\n%s", body)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, env := range []string{"", "FERN_SANITIZE=1"} {
			stdout, code := cli.exitOf(t, loadCompareSpilledProg, target, env)
			if code != 0 {
				t.Errorf("%s %s: exit %d, want 0\n%s", target, env, code, stdout)
			}
		}
	}
}
