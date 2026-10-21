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

// What a closure returns is what its body built, so a field the lambda
// `mk` never sets keeps `audit` out; and an array of a record nothing
// builds is empty, so the loop over `jobs` never runs and `run_job` goes.
// Building a `Job`, or setting `loud` in the lambda, keeps them.
const semReachTransportProg = `struct Cfg { loud: boolean, n: i32 }

struct Job { id: i32 }

@noinline function audit(c: Cfg): i32 {
  let t: i32 = 0;
  let i: i32 = 0;
  while (i < c.n) {
    t = t + i * 3 + 1;
    i = i + 1;
  }
  return t;
}

@noinline function run_job(j: Job): i32 {
  let t: i32 = 0;
  let i: i32 = 0;
  while (i < j.id) {
    t = t + i * 5 + 2;
    i = i + 1;
  }
  return t;
}

@noinline function made(mk: () => Cfg): Cfg {
  return mk();
}

@noinline function drain(jobs: Job[]): i32 {
  let t: i32 = 0;
  for j in jobs {
    t = t + run_job(j);
  }
  return t;
}

function main(): i32 {
  let c: Cfg = made((): Cfg => Cfg { loud: LOUD, n: args().len() + 2 });
  let jobs: Job[] = [];
  if (JOBS) {
    jobs = jobs.append(Job { id: c.n });
  }
  let r: i32 = drain(jobs);
  if (c.loud) {
    r = r + audit(c);
  }
  return r + c.n;
}
`

func TestSelfHostSemReachTransport(t *testing.T) {
	cli := buildSelfHostCLI(t)
	fern := buildLangBinForInterp(t)
	audit := regexp.MustCompile(`(?m)^__fn_audit:`)
	runJob := regexp.MustCompile(`(?m)^__fn_run_job:`)
	for _, tc := range []struct {
		name, loud, jobs string
		want             int
	}{
		{"neither", "false", "false", 3},
		{"loud", "true", "false", 15},
		{"jobs", "false", "true", 24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.NewReplacer("LOUD", tc.loud, "JOBS", tc.jobs).Replace(semReachTransportProg)
			if got := interpExitStdin(t, fern, src, ""); got != tc.want {
				t.Fatalf("the interpreter exited %d, want %d", got, tc.want)
			}
			path := filepath.Join(t.TempDir(), "transport.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux"} {
				asm := readFile(t, cli.emit(t, path, target))
				if audit.MatchString(asm) != (tc.loud == "true") {
					t.Errorf("%s: audit emitted %v with loud %s", target, audit.MatchString(asm), tc.loud)
				}
				if runJob.MatchString(asm) != (tc.jobs == "true") {
					t.Errorf("%s: run_job emitted %v with jobs %s", target, runJob.MatchString(asm), tc.jobs)
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

// serveWithoutH2Prog takes TLS and HTTP/3 from the environment, as the
// nightly's hello does, but not HTTP/2, which the hello also offers.
const serveWithoutH2Prog = `import "std/http";
import "std/serve";
import "std/platform";
import "std/tls/identity";
import "std/tls/server";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
  return http.ok("hello");
}

function main(): i32 {
  let opts: serve.Config = serve.Config { ...serve.config(), max_connections: 16384 };
  match (env("TLS_PEM")) {
    Some(pem) => {
      match (identity.from_pem(pem, pem)) {
        Ok(id) => {
          opts = opts.with_tls(server.config(id));
        },
        Err(e) => {
          eprint(e.message());
          return 2;
        }
      }
    },
    None => {}
  }
  if (env("H3").is_some()) {
    opts = opts.with_h3();
  }
  return serve.supervise(8080, opts, handle);
}
`

// A server that never calls `with_h2` links no HTTP/2: no reachable
// construction sets `Config.h2`, and the compiler-generated folds
// (`__smm_*`, `__arrm_*`) that carry a Config are reached only through the
// calls the bodies make. With the pass off, the HTTP/2 code is there.
func TestSelfHostSemReachServeWithoutH2(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := filepath.Join(t.TempDir(), "server.fern")
	if err := os.WriteFile(path, []byte(serveWithoutH2Prog), 0o644); err != nil {
		t.Fatal(err)
	}
	h2 := regexp.MustCompile(`(?m)^__fn_h2__\w+:`)
	asm := readFile(t, cli.emit(t, path, "x86-64-linux", "FERN_SIZE=1"))
	if fns := h2.FindAllString(asm, 4); len(fns) > 0 {
		t.Errorf("the server links HTTP/2: %v", fns)
	}
	off := readFile(t, cli.emit(t, path, "x86-64-linux", "FERN_SIZE=1", "FERN_NO_SEM_REACH=1"))
	if !h2.MatchString(off) {
		t.Errorf("FERN_NO_SEM_REACH=1: the server links no HTTP/2 either, so this test shows nothing")
	}
}

// serveWithoutH3Prog is serveWithoutH2Prog with HTTP/3 never asked for.
const serveWithoutH3Prog = `import "std/http";
import "std/serve";
import "std/platform";
import "std/tls/identity";
import "std/tls/server";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
  return http.ok("hello");
}

function main(): i32 {
  let opts: serve.Config = serve.Config { ...serve.config(), max_connections: 16384 };
  match (env("TLS_PEM")) {
    Some(pem) => {
      match (identity.from_pem(pem, pem)) {
        Ok(id) => {
          opts = opts.with_tls(server.config(id));
        },
        Err(e) => {
          eprint(e.message());
          return 2;
        }
      }
    },
    None => {}
  }
  return serve.supervise(8080, opts, handle);
}
`

// A server that never calls `with_h3` links no HTTP/3 and no QUIC: with no
// reachable construction setting `Config.h3`, the worker opens no QUIC
// socket, and the loop asks whether a ready descriptor is that socket only
// once there is one (`__quic_owns`), so the datagram path is never reached.
// With the pass off, the HTTP/3 code is there.
func TestSelfHostSemReachServeWithoutH3(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := filepath.Join(t.TempDir(), "server.fern")
	if err := os.WriteFile(path, []byte(serveWithoutH3Prog), 0o644); err != nil {
		t.Fatal(err)
	}
	h3 := regexp.MustCompile(`(?m)^__fn_(h3q|h3|qpack|packet|recovery)__\w+:`)
	asm := readFile(t, cli.emit(t, path, "x86-64-linux", "FERN_SIZE=1"))
	if fns := h3.FindAllString(asm, 4); len(fns) > 0 {
		t.Errorf("the server links HTTP/3: %v", fns)
	}
	off := readFile(t, cli.emit(t, path, "x86-64-linux", "FERN_SIZE=1", "FERN_NO_SEM_REACH=1"))
	if !h3.MatchString(off) {
		t.Errorf("FERN_NO_SEM_REACH=1: the server links no HTTP/3 either, so this test shows nothing")
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
