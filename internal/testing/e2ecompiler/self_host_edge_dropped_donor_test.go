package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// __initial has a branch, so the one-block rule keeps it as a call that
// borrows cur. The split pass forwards the reads of the second update to
// the fields of the first, and keeps the second update whole only for the
// phi at the join. Inside the inner if, the third update names the second
// as its base, but the second is read nowhere in that block: it dies on
// the edge into it. The plan now holds a box that dies on an edge for the
// construction the target block claims it with. Each round allocates
// nothing for the Set.
const edgeDroppedDonorProg = `struct Item { id: i64, closed: boolean, n: i32 }
struct Set { server: boolean, list: Item[], local: i64[], peer: i64[], limits: i64[], opened: i64[], closed: i64[], retired: i32, index: i32[], base: i64 }

function (s: Set) __initial(t: i32): i64 {
  if ((t & 2) == 0) {
    return s.local[0];
  }
  return s.local[1];
}

function (st: Item) __closed(): Item {
  return Item { ...st, closed: true };
}

@noinline
function (s: Set) retire(i: i32): Set {
  let st: Item = s.list[i];
  if (st.closed) {
    return s;
  }
  let cur: Set = Set { ...s, retired: s.retired + 1 };
  if ((st.id & 1) == 0) {
    let t: i32 = (st.id & 3) as i32;
    let initial: i64 = cur.__initial(t);
    let target: i64 = cur.closed[t] + 1 + initial;
    cur = Set { ...cur, closed: cur.closed.with(t, cur.closed[t] + 1) };
    if (target - cur.limits[t] >= 1) {
      cur = Set { ...cur, limits: cur.limits.with(t, target) };
    }
  }
  cur = Set { ...cur, list: cur.list.with(i, cur.list[i].__closed()) };
  return cur;
}

function main(): i32 {
  let list: Item[] = [];
  for i in 0..64 {
    list = list.append(Item { id: i as i64, closed: false, n: i });
  }
  let s: Set = Set { server: true, list: list, local: [8 as i64, 4 as i64], peer: [0 as i64, 0 as i64], limits: [0 as i64, 0 as i64, 0 as i64, 0 as i64], opened: [0 as i64, 0 as i64], closed: [0 as i64, 0 as i64, 0 as i64, 0 as i64], retired: 0, index: [], base: 0 as i64 };
  let before: i64 = __heap_alloc_count();
  for i in 0..64 {
    s = s.retire(i);
  }
  let allocs: i64 = __heap_alloc_count() - before;
  if (s.retired != 64 || s.limits[0] != 24 as i64 || !s.list[63].closed) {
    print("wrong " + s.retired.to_string() + " " + s.limits[0].to_string());
    return 1;
  }
  print(allocs.to_string());
  return 0;
}
`

func TestSelfHostEdgeDroppedDonor(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, edgeDroppedDonorProg, target, "", flags...)
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
					if got > 4 {
						t.Errorf("64 rounds allocate %d boxes, want at most 4: the Set rebuilt in the inner if", got)
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
