package e2ecompiler

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"testing"
)

const questionSplitConnProg = `struct Conn { n: i32, read: i32[], a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32, k: i32, l: i32, m: i32, o: i32, p: i32 }
struct Server { conn: Conn, state: i32 }
struct Run { server: Server, out: u8[] }
struct Absorbed { conn: Conn, out: u8[] }

@noinline function advance(r: i32[]): i32[] {
  return r.with(0, r[0] + 1);
}

@noinline function drain(s: Server, out: u8[]): Run {
  return Run { server: s, out: out };
}

@noinline function absorb_r(c: Conn, out: u8[]): Result[Absorbed, string] {
  if (c.n < 0) {
    return Err("neg");
  }
  return Ok(Absorbed { conn: Conn { ...c, read: advance(c.read) }, out: out });
}

@noinline function absorb(c: Conn, out: u8[]): Absorbed {
  return Absorbed { conn: Conn { ...c, read: advance(c.read) }, out: out };
}

@noinline function run_r(s: Server, out: u8[]): Result[Run, string] {
  let r: Run = drain(s, out);
  let i: i32 = 0;
  while (i < 2) {
    let a: Absorbed = absorb_r(r.server.conn, r.out)?;
    let state: i32 = r.server.state;
    r = drain(Server { ...r.server, conn: a.conn, state: state }, a.out);
    i = i + 1;
  }
  return Ok(r);
}

@noinline function run(s: Server, out: u8[]): Run {
  let r: Run = drain(s, out);
  let i: i32 = 0;
  while (i < 2) {
    let a: Absorbed = absorb(r.server.conn, r.out);
    let state: i32 = r.server.state;
    r = drain(Server { ...r.server, conn: a.conn, state: state }, a.out);
    i = i + 1;
  }
  return r;
}

@noinline function mk(): Server {
  return Server { conn: Conn { n: 1, read: [0, 0], a: 0, b: 0, c: 0, d: 0, e: 0, f: 0, g: 0, h: 0, i: 0, j: 0, k: 0, l: 0, m: 0, o: 0, p: 0 }, state: 0 };
}

function main(): i32 {
  let s: Server = mk();
  let out: u8[] = [1u8];
  let t: i32 = 0;
  let a0: i64 = __heap_alloc_count();
  let k: i32 = 0;
  while (k < 1000) {
    match (run_r(s, out)) {
      Ok(r) => { s = r.server; out = r.out; },
      Err(e) => { return 1; }
    }
    k = k + 1;
  }
  let a1: i64 = __heap_alloc_count();
  k = 0;
  while (k < 1000) {
    let r: Run = run(s, out);
    s = r.server;
    out = r.out;
    k = k + 1;
  }
  let a2: i64 = __heap_alloc_count();
  print("with ? " + (a1 - a0).to_string() + " without " + (a2 - a1).to_string() + " read " + s.conn.read[0].to_string());
  return 0;
}
`

// A record two fields deep goes to a call before a `?`. The callee then owns
// it and rebuilds it in place, as it does with no `?` (#12619).
func TestSelfHostQuestionSplitPassesOwnedField(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, questionSplitConnProg, target, "", flags...)
				var cmd *exec.Cmd
				switch target {
				case "x86-64-linux":
					cmd = runX86_64Bin(cli.runner, bin)
				case "arm64-linux":
					_, qemu := arm64Tooling(t)
					cmd = runArm64Bin(qemu, bin)
				default:
					cmd = exec.Command("wasmtime", "run", bin)
				}
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err != nil {
					t.Fatalf("sanitize=%v: %v: stdout %q; stderr %s", sanitize, err, &stdout, &stderr)
				}
				if !sanitize {
					var with, without, read int
					if _, err := fmt.Sscanf(stdout.String(), "with ? %d without %d read %d", &with, &without, &read); err != nil || read != 4000 {
						t.Fatalf("output %q: %v", &stdout, err)
					}
					if with-without >= 100 {
						t.Errorf("1000 calls allocate %d times with ? and %d times without: a Conn copy on each call adds 2000", with, without)
					}
					continue
				}
				if m := leak.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
					t.Fatalf("unbalanced sanitized run: %s", &stderr)
				}
			}
		})
	}
}
