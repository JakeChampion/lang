package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

// A loop reads two array fields of a record's member, rebuilds the record
// around an update of that member, and then walks one of the two arrays: the
// shape of std/http/h3q's applied loop. semsink keeps the walked array's
// first read rather than read it again from the record after the update, and
// takes the update's donor as spent there. So the record dies at its update
// and is rebuilt in its own box. Before, each call copied the record as well
// as the member: 2,000 allocations over 1,000 calls. The member's copy stays.
const keptReadProg = `import "std/i64";
struct S { id: i64, actions: i32[], events: i64[], name: string }
struct C { session: S, total: i64, n: i64 }

@noinline function (s: S) taken(): S {
  return S { ...s, actions: [], events: [] };
}

@noinline function (c: C) on_event(e: i64): C {
  return C { ...c, n: c.n + e };
}

@noinline function (c: C) applied(): C {
  let cur: C = c;
  while (true) {
    let a: i32[] = cur.session.actions;
    let evs: i64[] = cur.session.events;
    if (a.len() == 0 && evs.len() == 0) {
      return cur;
    }
    cur = C { ...cur, session: cur.session.taken(), total: cur.total + a.len() as i64 };
    for e in evs {
      cur = cur.on_event(e);
    }
  }
  return cur;
}

@noinline function (c: C) feed(acts: i32[], evs: i64[]): C {
  return C { ...c, session: S { ...c.session, actions: acts, events: evs } };
}

function main(): i32 {
  let acts: i32[] = [1, 2, 3];
  let evs: i64[] = [4 as i64, 5 as i64];
  let c: C = C { session: S { id: 1 as i64, actions: [], events: [], name: "x" }, total: 0 as i64, n: 0 as i64 };
  c = c.feed(acts, evs).applied();
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 1000) {
    c = c.feed(acts, evs).applied();
    i = i + 1;
  }
  let allocs: i64 = __heap_alloc_count() - before;
  if (c.total != 3003 as i64 || c.n != 9009 as i64) {
    print("wrong " + c.total.to_string() + " " + c.n.to_string());
    return 1;
  }
  print(allocs.to_string());
  return 0;
}
`

func TestSelfHostKeptReadPastUpdate(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, keptReadProg, target, "", flags...)
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
					if got := stdout.String(); got != "1000\n" {
						t.Errorf("1000 calls allocate %q times, want 1000: one copy of the member each", got)
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
