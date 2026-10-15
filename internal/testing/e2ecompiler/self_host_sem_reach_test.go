package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// semreach drops what only a field no reachable construction sets could
// reach. In semReachProg `fast` is false wherever an `Opts` is built unless
// `with_fast` is called, and `Framed` is built only on paths that need
// `fast` or a `Framed` already: so without the call, `heavy` and the lambda
// only `scale`'s fast path builds are dropped, and the release of the other
// `(i32) => i32` matches no box against the dropped lambda's environment.
// With the call, or with FERN_NO_SEM_REACH=1, both are kept. Every build
// answers as the interpreter does.
const semReachProg = `struct Opts { fast: boolean, level: i32 }

function opts(): Opts {
  return Opts { fast: false, level: 3 };
}

function (o: Opts) with_fast(): Opts {
  return Opts { ...o, fast: true };
}

enum Mode { Plain, Framed(i32[]) }

@noinline function heavy(xs: i32[]): i32 {
  let t: i32 = 0;
  for x in xs {
    t = t + x * 7 + 1;
  }
  return t;
}

@noinline function scale(o: Opts): (i32) => i32 {
  let k: i32 = o.level;
  if (o.fast) {
    return (x: i32): i32 => heavy([x, k]);
  }
  return (x: i32): i32 => x + k;
}

@noinline function step(o: Opts, m: Mode): Mode {
  match (m) {
    Framed(xs) => {
      return Framed(xs.append(heavy(xs)));
    },
    Plain => {}
  }
  if (o.fast) {
    return Framed([o.level]);
  }
  return Plain;
}

@noinline function count(m: Mode): i32 {
  match (m) {
    Framed(xs) => {
      return xs.len();
    },
    Plain => {
      return 0;
    }
  }
  return 0;
}

function main(): i32 {
  let o: Opts = OPTS;
  let m: Mode = step(o, step(o, Plain));
  let f: (i32) => i32 = scale(o);
  return count(m) * 10 + f(2);
}
`

func TestSelfHostSemReach(t *testing.T) {
	cli := buildSelfHostCLI(t)
	fern := buildLangBinForInterp(t)
	heavy := regexp.MustCompile(`(?m)^__fn_heavy:`)
	lambda := regexp.MustCompile(`(?m)^__fn_scale\$clo0:`)
	for _, tc := range []struct {
		name, opts string
		want       int
		kept       bool
	}{
		{"never-fast", "opts()", 5, false},
		{"fast", "opts().with_fast()", 57, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(semReachProg, "OPTS", tc.opts, 1)
			if got := interpExitStdin(t, fern, src, ""); got != tc.want {
				t.Fatalf("the interpreter exited %d, want %d", got, tc.want)
			}
			path := filepath.Join(t.TempDir(), "reach.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux"} {
				asm := readFile(t, cli.emit(t, path, target))
				if heavy.MatchString(asm) != tc.kept || lambda.MatchString(asm) != tc.kept {
					t.Errorf("%s: heavy emitted %v and scale's fast lambda %v, want both %v", target, heavy.MatchString(asm), lambda.MatchString(asm), tc.kept)
				}
				off := readFile(t, cli.emit(t, path, target, "FERN_NO_SEM_REACH=1"))
				if !heavy.MatchString(off) || !lambda.MatchString(off) {
					t.Errorf("%s FERN_NO_SEM_REACH=1: heavy or scale's fast lambda dropped", target)
				}
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				if _, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s: exit %d, want %d", target, code, tc.want)
				}
			}
		})
	}
}

// A closure's environment is built by its construction, not by a record
// literal, so what a closure captures is any value: `pick`'s lambda, which
// escapes into `each`, branches on its captured `flag` both ways.
const semReachCaptureProg = `@noinline function each(xs: i32[], f: (i32) => i32): i32 {
  let t: i32 = 0;
  for x in xs {
    t = t + f(x);
  }
  return t;
}

@noinline function pick(flag: boolean, xs: i32[]): i32 {
  return each(xs, (x: i32): i32 => {
    if (flag) {
      return x * 2;
    }
    return x + 1;
  });
}

function main(): i32 {
  return pick(args().len() > 0, [1, 2, 3]) + pick(args().len() > 5, [4]);
}
`

func TestSelfHostSemReachCapture(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		if _, code := cli.exitOf(t, semReachCaptureProg, target); code != 17 {
			t.Errorf("%s: exit %d, want 17", target, code)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
