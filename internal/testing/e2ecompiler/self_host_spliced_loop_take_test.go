package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// past_loss is spliced into detect_lost. Its loop reads the element, and
// the update that the with stores back sits after the loop. The caller
// reads the array twice in its first block, once for the element that
// lost_of borrows and once for the with. The with counts for the first
// read of the slot, so the sink pass keeps that read, and the plan takes
// the element out of its slot before the loop. Each round allocates
// nothing: the Space update reuses the element's box.
const splicedLoopTakeProg = `import "std/array";
struct Sent { pn: i64, at: i64, size: i32 }
struct Space { sent: Sent[], largest_acked: i64, loss_time: i64 }
struct Recovery { spaces: Space[], lost: i32, rtt: i64 }

function is_lost(s: Space, p: Sent, lost_before: i64): boolean {
  return p.at < lost_before || p.pn + 3 as i64 <= s.largest_acked;
}

@noinline
function lost_of(s: Space, lost_before: i64): i32 {
  let n: i32 = 0;
  for p in s.sent {
    if (is_lost(s, p, lost_before)) {
      n = n + 1;
    }
  }
  return n;
}

function past_loss(s: Space, lost_before: i64, delay: i64): Space {
  let kept: Sent[] = [];
  let loss_time: i64 = 0;
  for p in s.sent {
    if (!is_lost(s, p, lost_before)) {
      kept = kept.append(p);
      let due: i64 = p.at + delay;
      if (p.pn <= s.largest_acked && (loss_time == 0 as i64 || due < loss_time)) {
        loss_time = due;
      }
    }
  }
  return Space { ...s, sent: kept, loss_time: loss_time };
}

@noinline
function detect_lost(r: Recovery, space: i32, now: i64): Recovery {
  let delay: i64 = r.rtt * 9 / 8;
  let lost: i32 = lost_of(r.spaces[space], now - delay);
  return Recovery { ...r, spaces: r.spaces.with(space, past_loss(r.spaces[space], now - delay, delay)), lost: r.lost + lost };
}

function main(): i32 {
  let spaces: Space[] = [];
  for i in 0..3 {
    spaces = spaces.append(Space { sent: [Sent { pn: 1 as i64, at: 1000000 as i64, size: 9 }], largest_acked: 5 as i64, loss_time: 0 as i64 });
  }
  let r: Recovery = Recovery { spaces: spaces, lost: 0, rtt: 8 as i64 };
  r = detect_lost(r, 0, 100 as i64);
  let before: i64 = __heap_alloc_count();
  for round in 0..1000 {
    r = detect_lost(r, round % 3, round as i64);
  }
  let allocs: i64 = __heap_alloc_count() - before;
  if (r.lost != 3 || r.spaces[0].sent.len() != 0) {
    print("wrong " + r.lost.to_string());
    return 1;
  }
  print(allocs.to_string());
  return 0;
}
`

func TestSelfHostElementTakeThroughSplicedLoop(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, splicedLoopTakeProg, target, "", flags...)
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
					if got > 10 {
						t.Errorf("1000 rounds allocate %d boxes, want at most 10: the Space copies after the spliced loop", got)
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
