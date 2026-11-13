package e2ecompiler

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"testing"
)

// The copy of an Order is nine boxes: the record, its string, its array,
// two items, their two strings, the tuple and the tuple's string. Each of
// the four strings takes over the byte array it copies through, so it adds no
// box. The table of copied boxes is one more allocation, and a second one
// where a box of the source can have more than one owner. The two lines are
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
				want := "11\nadanutrush3\n"
				if target == "wasm32-wasi" {
					want = "10\nadanutrush3\n"
				}
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

// Twenty-six levels, each with two references to the level below, are 27
// records, 52 Option boxes and 2^26 paths. The two lines are the allocation
// count of the copy and its depth.
const sendCopySharedProg = `import "std/i64";

struct N { l: Option[N], r: Option[N] }

function depth(n: N): i32 {
    return match (n.l) {
        Some(c) => depth(c) + 1,
        None => 0,
    };
}

function main(): i32 {
    let cur: N = N { l: None, r: None };
    for i in 0..26 {
        cur = N { l: Some(cur), r: Some(cur) };
    }
    let before: i64 = __heap_alloc_count();
    let c: N = __send_copy(cur);
    let after: i64 = __heap_alloc_count();
    print((after - before).to_string());
    print(depth(c).to_string());
    return 0;
}
`

// A box that two fields hold copies one time, and the two fields of the copy
// hold that one box (#15050).
func TestSelfHostSendCopyKeepsSharedBoxes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			bin := compileCPUProfile(t, cli, sendCopySharedProg, target, "", "-sanitize")
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
				t.Fatalf("%v: stdout %q; stderr %s", err, &stdout, &stderr)
			}
			var allocs, depth int
			if _, err := fmt.Sscanf(stdout.String(), "%d\n%d\n", &allocs, &depth); err != nil {
				t.Fatalf("stdout %q: %v", &stdout, err)
			}
			// The 79 boxes, the table, and its slot arrays as it grows.
			if allocs < 79 || allocs > 86 || depth != 26 {
				t.Errorf("the copy made %d allocations at depth %d, want 79 to 86 at depth 26", allocs, depth)
			}
			if m := leak.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
				t.Fatalf("unbalanced sanitized run: %s", &stderr)
			}
		})
	}
}
