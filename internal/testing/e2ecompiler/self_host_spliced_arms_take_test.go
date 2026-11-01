package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// headed is spliced into section. Its four arms each update the element,
// and the with that stores the element back sits in the join block. The
// first read of s.reqs stays, so the plan takes the element out of its slot
// and the update in each arm writes in place. Each round allocates the
// event box and the events buffer, and nothing for the Req.
const splicedArmsTakeProg = `import "std/array";
struct Req { phase: i32, length: i64, head: boolean, buf: u8[] }
enum Event { Headers(i64, string[]), Trailers(i64, string[]) }
struct Session { server: boolean, reqs: Req[], events: Event[], n: i32 }

function headed(r: Req, server: boolean, fields: string[], length: i64): Req {
  if (server) {
    return Req { ...r, phase: 2, length: length };
  }
  if (fields.len() > 3) {
    return Req { ...r, phase: 1 };
  }
  if (r.head || fields.len() == 0) {
    return Req { ...r, phase: 2, length: 0 as i64 };
  }
  return Req { ...r, phase: 2, length: length };
}

@noinline
function section(s: Session, i: i32, id: i64, fields: string[]): Session {
  if (i < 0) {
    return s;
  }
  let r: Req = s.reqs[i];
  if (r.phase == 3) {
    return Session { ...s, events: s.events.append(Trailers(id, fields)), reqs: s.reqs.with(i, Req { ...r, phase: 4 }) };
  }
  if (fields.len() > 8) {
    return Session { ...s, n: s.n - 1 };
  }
  return Session { ...s, events: s.events.append(Headers(id, fields)), reqs: s.reqs.with(i, headed(s.reqs[i], s.server, fields, id)) };
}

function main(): i32 {
  let reqs: Req[] = [];
  for i in 0..4 {
    reqs = reqs.append(Req { phase: 0, length: 0 as i64, head: i == 1, buf: [] });
  }
  let s: Session = Session { server: false, reqs: reqs, events: [], n: 0 };
  let fields: string[] = ["a", "b"];
  s = section(s, 0, 1 as i64, fields);
  let before: i64 = __heap_alloc_count();
  for round in 0..1000 {
    s = section(s, round % 4, round as i64, fields);
    s = Session { ...s, events: [] };
  }
  let allocs: i64 = __heap_alloc_count() - before;
  if (s.reqs[0].length != 996 as i64 || s.reqs[1].length != 0 as i64 || s.n != 0) {
    print("wrong " + s.reqs[0].length.to_string());
    return 1;
  }
  print(allocs.to_string());
  return 0;
}
`

func TestSelfHostElementTakeThroughSplicedArms(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, splicedArmsTakeProg, target, "", flags...)
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
					got, err := strconv.ParseInt(strings.TrimSpace(stdout.String()), 10, 64)
					if err != nil {
						t.Fatalf("output %q is not a count", stdout.String())
					}
					if got > 2100 {
						t.Errorf("1000 rounds allocate %d boxes, want at most 2100: the Req copies in the spliced arms", got)
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
