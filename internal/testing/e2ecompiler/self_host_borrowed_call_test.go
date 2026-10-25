package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

// on_data lends the table and a half of one of its items to past, which
// only reads them, then updates that item in its slot. The call leaves the
// table alive, so the item's slot is read again after it and the update
// takes the item and its half rather than copying both.
const borrowedCallProg = `import "std/i64";
struct Half { buf: u8[], at: i64 }
struct Item { id: i64, half: Half }
struct Table { items: Item[], limit: i64, refused: i64 }

@noinline function (t: Table) past(h: Half, n: i64): boolean {
  return h.at + n > t.limit;
}

@noinline function (h: Half) fed(n: i64): Half {
  return Half { ...h, at: h.at + n };
}

@noinline function (it: Item) fed(n: i64): Item {
  return Item { ...it, half: it.half.fed(n) };
}

@noinline function (t: Table) on_data(i: i32, n: i64): Table {
  let it: Item = t.items[i];
  if (t.past(it.half, n)) {
    return Table { ...t, refused: t.refused + 1 };
  }
  return Table { ...t, items: t.items.with(i, t.items[i].fed(n)) };
}

function main(): i32 {
  let t: Table = Table { items: [Item { id: 1 as i64, half: Half { buf: __alloc_u8(8), at: 0 as i64 } }, Item { id: 2 as i64, half: Half { buf: __alloc_u8(8), at: 0 as i64 } }], limit: 1500 as i64, refused: 0 as i64 };
  t = t.on_data(1, 1 as i64);
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 1000) {
    t = t.on_data(1, 1 as i64);
    i = i + 1;
  }
  let allocs: i64 = __heap_alloc_count() - before;
  while (i < 2000) {
    t = t.on_data(1, 1 as i64);
    i = i + 1;
  }
  if (t.items[1].half.at != 1500 as i64 || t.refused != 501 as i64 || t.items[0].half.at != 0 as i64) {
    print("wrong " + t.items[1].half.at.to_string() + " " + t.refused.to_string());
    return 1;
  }
  print(allocs.to_string());
  return 0;
}
`

func TestSelfHostBorrowedCallLetsTheSlotMove(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, borrowedCallProg, target, "", flags...)
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
						t.Errorf("1000 updates after a borrowed call allocate %q, want 0", got)
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
