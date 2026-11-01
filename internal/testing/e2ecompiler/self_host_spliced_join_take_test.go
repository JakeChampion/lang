package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const splicedJoinTakeSource = `import "std/array";

enum RBody { Text(string), Bytes(u8[]) }
struct Resp { status: i32, body: RBody }
struct Conn { n: i64, date: string, log: u8[] }
struct Q { conns: Conn[], k: i32 }
struct Conns { quic: Q, date: string, serial: i32 }

function (r: Resp) body_bytes(): u8[] {
  match (r.body) {
    Text(s) => { return s.bytes(); },
    Bytes(b) => { return b; }
  }
}

@noinline
function (c: Conn) with_date(d: string): Conn {
  if (d == c.date) { return c; }
  return Conn { ...c, date: d };
}

@noinline
function (c: Conn) __respond_with(id: i64, bytes: [u8]): Conn {
  return Conn { ...c, n: c.n + id + bytes.len() as i64, log: c.log.append(bytes.len() as u8) };
}

RESPOND_ATTRIBUTE
function respond(c: Conn, id: i64, resp: Resp): Conn {
  match (resp.body) {
    Text(s) => { return c.__respond_with(id, s.as_bytes()); },
    _ => { return c.__respond_with(id, resp.body_bytes()); }
  }
}

@noinline
function h3_respond(own c: Conns, i: i32, id: i64, resp: Resp): Conns {
  if (i < 0) { return c; }
  let q: Q = c.quic;
  return Conns { ...c, quic: Q { ...q, conns: q.conns.with(i, respond(q.conns[i].with_date(c.date), id, resp)) } };
}

function main(): void {
  let conns: Conn[] = [];
  for i in 0..4 {
    conns = conns.append(Conn { n: i as i64, date: "", log: [] });
  }
  let c: Conns = Conns { quic: Q { conns: conns, k: 0 }, date: "today", serial: 7 };
  for round in 0..1000 {
    let body: RBody = if (round % 2 == 0) { Text("hello") } else { Bytes([1 as u8, 2 as u8]) };
    c = h3_respond(c, round % 4, round as i64, Resp { status: 200, body: body });
  }
  print(c.quic.conns[0].n);
  print(c.quic.conns[1].log.len());
}
`

func TestSelfHostArrayElementTakeAcrossSplicedJoin(t *testing.T) {
	_, targets, skip := hostTargets()
	if skip != "" {
		t.Skip(skip)
	}
	driver := e2eharness.SelfHostCLI(t)
	targets = append(targets, ssaBackendTarget{target: e2eharness.TargetWasm32Wasi})
	forms := []struct {
		name      string
		attribute string
		method    bool
	}{
		{"spliced", "", false},
		{"call", "@noinline", false},
		{"method-spliced", "@inline", true},
		{"method-call", "@noinline", true},
	}
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range targets {
		t.Run(target.target, func(t *testing.T) {
			allocs := map[string]int64{}
			for _, form := range forms {
				t.Run(form.name, func(t *testing.T) {
					program := strings.ReplaceAll(splicedJoinTakeSource, "RESPOND_ATTRIBUTE", form.attribute)
					if form.method {
						program = strings.NewReplacer(
							"function respond(c: Conn, ", "function (c: Conn) respond(",
							"respond(q.conns[i].with_date(c.date), id, resp)", "q.conns[i].with_date(c.date).respond(id, resp)",
						).Replace(program)
					}
					source := filepath.Join(t.TempDir(), "main.fern")
					if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
						t.Fatal(err)
					}
					binary := filepath.Join(t.TempDir(), "probe")
					args := []string{"-target", target.target, "-sanitize"}
					runner := target.runner
					if target.target == e2eharness.TargetWasm32Wasi {
						args = append(args, "-emit", "core-module")
						runner = []string{e2eharness.Wasmtime(t), "run", "--env", "FERN_LEAKCHECK=1"}
					}
					build := exec.Command(driver, append(args, "-o", binary, source, e2eharness.SelfHostStdlibRoot(t))...)
					build.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1", "FERN_RC_CERTIFY_MEMO=0")
					if out, err := build.CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, out)
					}
					run := exec.Command(binary)
					if len(runner) != 0 {
						run = exec.Command(runner[0], append(runner[1:], binary)...)
					}
					run.Env = childEnv("FERN_LEAKCHECK=1")
					var stdout, stderr bytes.Buffer
					run.Stdout, run.Stderr = &stdout, &stderr
					if err := run.Run(); err != nil {
						t.Fatalf("run: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
					}
					if got := stdout.String(); got != "125750\n250\n" {
						t.Fatalf("output %q, want 125750 and 250", got)
					}
					assertBalancedCensus(t, stderr.String())
					m := leak.FindStringSubmatch(stderr.String())
					if len(m) != 3 {
						t.Fatalf("missing census: %s", &stderr)
					}
					count, err := strconv.ParseInt(m[1], 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					allocs[form.name] = count
					t.Logf("allocations: %d", count)
				})
			}
			for _, pair := range [][2]string{{"spliced", "call"}, {"method-spliced", "method-call"}} {
				if spliced, ok := allocs[pair[0]]; ok {
					if call, ok := allocs[pair[1]]; ok && spliced != call {
						t.Errorf("%s allocates %d times, %s allocates %d", pair[0], spliced, pair[1], call)
					}
				}
			}
		})
	}
}
