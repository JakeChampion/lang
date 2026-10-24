package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

const elementRereadProg = `import "std/i32";
import "std/i64";
struct Strm { id: i32, pending: u8[], window: i32, done: boolean }
struct Conn { phase: i32, streams: Strm[], window: i32, handling: i32, client: boolean, inbox: u8[] }
struct Run { conn: Conn, out: u8[], sent: i32 }

@noinline function flush(r: Run, at: i32): Run {
  let out: Run = r;
  while (true) {
    let c: Conn = out.conn;
    let s: Strm = c.streams[at];
    if (s.done) {
      return out;
    }
    if (s.window <= 0) {
      return out;
    }
    let sent: Strm = Strm { ...s, window: s.window - 1, done: s.window == 1 };
    out = Run { ...out, conn: Conn { ...c, window: c.window - 1, streams: c.streams.with(at, sent) }, sent: out.sent + s.pending.len() };
  }
  return out;
}

@noinline function finished(r: Run, at: i32): Run {
  if (r.conn.client) {
    return r;
  }
  let s: Strm = r.conn.streams[at];
  if (s.window < 0) {
    return r;
  }
  let c: Conn = r.conn;
  let handed: Strm = Strm { ...s, window: 3, done: false };
  c = Conn { ...c, streams: c.streams.with(at, handed), handling: c.handling + 1 };
  return Run { ...r, conn: c, sent: r.sent + s.id };
}

function main(): i32 {
  let streams: Strm[] = [];
  let i: i32 = 0;
  while (i < 4) {
    streams = streams.append(Strm { id: i, pending: [1, 2, 3], window: 3, done: false });
    i = i + 1;
  }
  let r: Run = Run { conn: Conn { phase: 1, streams: streams, window: 1000000, handling: 0, client: false, inbox: [] }, out: [], sent: 0 };
  r = finished(flush(r, 0), 0);
  let before: i64 = __heap_alloc_count();
  i = 0;
  while (i < 1000) {
    r = finished(flush(r, i % 4), i % 4);
    i = i + 1;
  }
  let allocs: i64 = __heap_alloc_count() - before;
  if (r.conn.handling != 1001 || r.sent != 10509) {
    print("wrong " + r.conn.handling.to_string() + " " + r.sent.to_string());
    return 1;
  }
  print(allocs.to_string());
  return 0;
}
`

func TestSelfHostElementRereadReuse(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, elementRereadProg, target, "", flags...)
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
					if got := stdout.String(); got != "0\n" {
						t.Errorf("1000 rounds allocate %q times, want 0", got)
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
