package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

// The copy of an Order is nine boxes: the record, its string, its array,
// two items, their two strings, the tuple and the tuple's string. Each of
// the four strings copies through one temporary byte array, which the
// helper frees, so the copy makes thirteen allocations. The two lines are
// the allocation count of the copy and the copy's contents.
const sendCopyProg = `import "std/i64";

struct Item { name: string, qty: i32 }

struct Order { who: string, items: Item[], tags: (i32, string) }

function main(): i32 {
    let o: Order = Order { who: "ada", items: [Item { name: "bolt", qty: 3 }, Item { name: "nut", qty: 9 }], tags: (2, "rush") };
    let before: i64 = __heap_alloc_count();
    let c: Order = __send_copy(o);
    let after: i64 = __heap_alloc_count();
    print((after - before).to_string());
    print(c.who + c.items[1].name + c.tags.1 + c.items[0].qty.to_string());
    return 0;
}
`

// __send_copy builds a fresh tree: every box of the copy is a new
// allocation, and a sanitized run frees all of them and the temporaries.
func TestSelfHostSendCopyAllocatesFreshBoxes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, sendCopyProg, target, "", flags...)
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
				want := "13\nadanutrush3\n"
				if got := stdout.String(); got != want {
					t.Errorf("sanitize=%v: stdout %q, want %q", sanitize, got, want)
				}
				if sanitize {
					if m := leak.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
						t.Fatalf("unbalanced sanitized run: %s", &stderr)
					}
				}
			}
		})
	}
}
