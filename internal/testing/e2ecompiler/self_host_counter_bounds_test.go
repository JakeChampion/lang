package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A counter that starts at 1 and steps up by 1 below a length is at least 1,
// so `a[i - 1]` needs no sign test. The loop guard bounds `i + 1` and `i - 1`
// below the length, so neither read keeps a check. A counter that starts at
// the length and steps down while it is above 0 is at most the length, and
// the guard makes `i - 1` not negative. A counter that starts at 0, or steps
// down while it is not negative, keeps the test, and its read aborts.
const counterFloorFuncs = `@noinline function smooth(a: i32[]): i32 {
  let s = 0;
  for i in 1..a.len() - 1 {
    s += a[i - 1] + a[i + 1];
  }
  return s;
}
@noinline function tail_sum(a: i32[]): i32 {
  let s = 0;
  let i = a.len();
  while (i > 0) {
    s += a[i - 1];
    i -= 1;
  }
  return s;
}
@noinline function from_zero(a: i32[]): i32 {
  let s = 0;
  for i in 0..a.len() {
    s += a[i - 1];
  }
  return s;
}
@noinline function down(a: i32[]): i32 {
  let s = 0;
  let i = a.len() - 1;
  while (i >= 0) {
    s += a[i - 1];
    i -= 1;
  }
  return s;
}
@noinline function seq(n: i32): i32[] {
  let a: i32[] = [];
  for i in 0..n { a = a.append(i * 2); }
  return a;
}
`

func TestSelfHostCounterBoundsProveTheRead(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "floor.fern")
	program := counterFloorFuncs + "function main(): i32 { if (smooth(seq(10)) != 144 || smooth(seq(2)) != 0 || tail_sum(seq(10)) != 90 || tail_sum(seq(0)) != 0) { return 1; } return 0; }\n"
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, program, target); code != 0 {
				t.Fatalf("smooth: exit %d, want 0\n%s", code, stderr)
			}
			for _, call := range []string{"from_zero(seq(4))", "down(seq(4))"} {
				out := counterFloorFuncs + "function main(): i32 { return " + call + "; }\n"
				stderr, code := cli.exitOf(t, out, target)
				if target == "wasm32-wasi" && code == 0 || target != "wasm32-wasi" && (code != 134 || !strings.Contains(stderr, "fern: array index out of range")) {
					t.Errorf("%s exited %d, want the array abort\n%s", call, code, stderr)
				}
			}
			if target == "wasm32-wasi" {
				return
			}
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"smooth", "tail_sum"} {
				body := asmFunction(string(asm), "__fn_"+name+":")
				if body == "" {
					t.Fatalf("no __fn_%s in the listing", name)
				}
				if strings.Contains(body, "oob_abort") {
					t.Errorf("%s keeps a check of its reads:\n%s", name, body)
				}
			}
		})
	}
}

// A read at `n - 1 - i` for `i` in `0..n` is in range when `n` is the length:
// the guard `i < n` gives `i <= n - 1`, and `i >= 0` keeps the read below
// `n`. A read at `n - i`, or with `n` above the length, keeps its check and
// aborts.
const reverseReadFuncs = `@noinline function rev_len(a: i32[]): i32 {
  let s = 0;
  for i in 0..a.len() {
    s = s * 3 + a[a.len() - 1 - i];
  }
  return s;
}
@noinline function rev_n(a: i32[]): i32 {
  let n = a.len();
  let s = 0;
  for i in 0..n {
    s = s * 3 + a[n - 1 - i];
  }
  return s;
}
@noinline function rev_past(a: i32[]): i32 {
  let s = 0;
  for i in 0..a.len() {
    s += a[a.len() - i];
  }
  return s;
}
@noinline function rev_wide(a: i32[]): i32 {
  let n = a.len() + 1;
  let s = 0;
  for i in 0..n {
    s += a[n - 1 - i];
  }
  return s;
}
@noinline function seq(n: i32): i32[] {
  let a: i32[] = [];
  for i in 0..n { a = a.append(i * 2); }
  return a;
}
`

func TestSelfHostReverseReadsNeedNoCheck(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "reverse.fern")
	program := reverseReadFuncs + "function main(): i32 { if (rev_len(seq(4)) != 204 || rev_n(seq(4)) != 204 || rev_len(seq(0)) != 0 || rev_n(seq(0)) != 0) { return 1; } return 0; }\n"
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, program, target); code != 0 {
				t.Fatalf("reverse reads: exit %d, want 0\n%s", code, stderr)
			}
			for _, call := range []string{"rev_past(seq(4))", "rev_wide(seq(4))"} {
				out := reverseReadFuncs + "function main(): i32 { return " + call + "; }\n"
				stderr, code := cli.exitOf(t, out, target)
				if target == "wasm32-wasi" && code == 0 || target != "wasm32-wasi" && (code != 134 || !strings.Contains(stderr, "fern: array index out of range")) {
					t.Errorf("%s exited %d, want the array abort\n%s", call, code, stderr)
				}
			}
			if target == "wasm32-wasi" {
				return
			}
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"rev_len", "rev_n"} {
				body := asmFunction(string(asm), "__fn_"+name+":")
				if body == "" {
					t.Fatalf("no __fn_%s in the listing", name)
				}
				if strings.Contains(body, "oob_abort") {
					t.Errorf("%s keeps a check of its reads:\n%s", name, body)
				}
			}
		})
	}
}

// A binary search reads at `lo + (hi - lo) / 2`, which the guard `lo < hi`
// keeps below `hi`. With `lo` from 0 and `hi` from the length, the read
// needs no check. A search whose `hi` starts above the length, or whose `lo`
// starts below 0, keeps its check and aborts.
const bisectFuncs = `@noinline function find(a: i32[], key: i32): i32 {
  let lo = 0;
  let hi = a.len();
  while (lo < hi) {
    let mid = lo + (hi - lo) / 2;
    if (a[mid] < key) { lo = mid + 1; } else { hi = mid; }
  }
  return lo;
}
@noinline function find_shift(a: i32[], key: i32): i32 {
  let lo = 0;
  let hi = a.len();
  while (lo < hi) {
    let mid = lo + ((hi - lo) >> 1);
    if (a[mid] < key) { lo = mid + 1; } else { hi = mid; }
  }
  return lo;
}
@noinline function find_wide(a: i32[], key: i32): i32 {
  let lo = 0;
  let hi = a.len() + 1;
  while (lo < hi) {
    let mid = lo + (hi - lo) / 2;
    if (a[mid] < key) { lo = mid + 1; } else { hi = mid; }
  }
  return lo;
}
@noinline function find_low(a: i32[], key: i32): i32 {
  let lo = 0 - 4;
  let hi = a.len();
  while (lo < hi) {
    let mid = lo + (hi - lo) / 2;
    if (a[mid] < key) { lo = mid + 1; } else { hi = mid; }
  }
  return lo;
}
@noinline function seq(n: i32): i32[] {
  let a: i32[] = [];
  for i in 0..n { a = a.append(i * 2); }
  return a;
}
`

func TestSelfHostBisectReadNeedsNoCheck(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "bisect.fern")
	program := bisectFuncs + "function main(): i32 { if (find(seq(10), 7) != 4 || find(seq(10), 100) != 10 || find(seq(0), 3) != 0 || find_shift(seq(10), 7) != 4 || find_shift(seq(10), 0) != 0) { return 1; } return 0; }\n"
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, program, target); code != 0 {
				t.Fatalf("binary search: exit %d, want 0\n%s", code, stderr)
			}
			for _, call := range []string{"find_wide(seq(4), 100)", "find_low(seq(4), 0 - 1)"} {
				out := bisectFuncs + "function main(): i32 { return " + call + "; }\n"
				stderr, code := cli.exitOf(t, out, target)
				if target == "wasm32-wasi" && code == 0 || target != "wasm32-wasi" && (code != 134 || !strings.Contains(stderr, "fern: array index out of range")) {
					t.Errorf("%s exited %d, want the array abort\n%s", call, code, stderr)
				}
			}
			if target == "wasm32-wasi" {
				return
			}
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"find", "find_shift"} {
				body := asmFunction(string(asm), "__fn_"+name+":")
				if body == "" {
					t.Fatalf("no __fn_%s in the listing", name)
				}
				if strings.Contains(body, "oob_abort") {
					t.Errorf("%s keeps a check of its read:\n%s", name, body)
				}
			}
		})
	}
}
