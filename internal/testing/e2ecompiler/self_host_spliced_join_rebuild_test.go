package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// is_local, initial and max are spliced into retire, so the Set update chain
// is read apart and split into one value per field, with a phi per field
// at each join. Most of those phis are trivial. Unfolded, the list field of
// the parameter is read again on the edges into the joins, and the plan
// releases the parameter there, where no construction can take its box.
// Folded, the fields of the last update are the reads of the parameter
// itself, so the parameter's box is spent in the block of that update and
// the update reuses it. Each round allocates nothing.
const splicedJoinRebuildProg = `struct Item { id: i64, closed: boolean, fin: boolean, acked: boolean, n: i32 }
struct Set { refused: string, server: boolean, list: Item[], local: i64[], peer: i64[], limits: i64[], opened: i64[], dirty: boolean[], closed: i64[], retired: i32, index: i32[], base: i64 }

function is_local(s: Set, id: i64): boolean {
  if (s.server) {
    return (id & 1) == 1;
  }
  return (id & 1) == 0;
}

function initial(s: Set, t: i32): i64 {
  if ((t & 2) == 0) {
    return s.local[0];
  }
  return s.local[1];
}

function max(a: i64, b: i64): i64 {
  if (a > b) {
    return a;
  }
  return b;
}

function done(st: Item): boolean {
  return st.fin && st.acked;
}

function (st: Item) __closed(): Item {
  return Item { ...st, closed: true };
}

@noinline
function (s: Set) retire(i: i32): Set {
  let st: Item = s.list[i];
  if (st.closed || !done(st)) {
    return s;
  }
  let cur: Set = Set { ...s, retired: s.retired + 1 };
  if (!is_local(cur, st.id)) {
    let t: i32 = (st.id & 3) as i32;
    let init: i64 = initial(cur, t);
    let target: i64 = cur.closed[t] + 1 + init;
    cur = Set { ...cur, closed: cur.closed.with(t, cur.closed[t] + 1) };
    if (target - cur.limits[t] >= max(1 as i64, init / 8)) {
      cur = Set { ...cur, limits: cur.limits.with(t, target), dirty: cur.dirty.with(t, true) };
    }
  }
  cur = Set { ...cur, list: cur.list.with(i, cur.list[i].__closed()) };
  if (cur.retired >= 32 && cur.retired * 2 >= cur.list.len() + 100) {
    return cur.sweep();
  }
  return cur;
}

@noinline
function (s: Set) sweep(): Set {
  return Set { ...s, retired: 0 };
}

function main(): i32 {
  let list: Item[] = [];
  for i in 0..64 {
    list = list.append(Item { id: i as i64, closed: false, fin: true, acked: true, n: i });
  }
  let s: Set = Set { refused: "", server: true, list: list, local: [8 as i64, 4 as i64], peer: [0 as i64, 0 as i64], limits: [0 as i64, 0 as i64, 0 as i64, 0 as i64], opened: [0 as i64, 0 as i64], dirty: [false, false, false, false], closed: [0 as i64, 0 as i64, 0 as i64, 0 as i64], retired: 0, index: [], base: 0 as i64 };
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

func TestSelfHostSplicedJoinRebuild(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, splicedJoinRebuildProg, target, "", flags...)
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
						t.Errorf("64 rounds allocate %d boxes, want at most 4: the Set rebuilt after the spliced join", got)
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
