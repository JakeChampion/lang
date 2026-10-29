package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const reuseCarryProgram = `struct Tab { a: i32[], b: i32[], c: i32[] }
enum Kind { Pip, Idle, Bad(i32) }
@noinline function note(k: Kind): i32 {
    match (k) { Pip => { return 1; }, Idle => { return 2; }, Bad(n) => { return n; } }
    return 0;
}
function took(t: Tab, at: i32, i: i32): (Tab, boolean, Kind) {
    t = Tab { ...t, a: t.a.with(at, i), c: t.c.with(at, i + 1) };
    let kind: Kind = Idle;
    if (i % 3 == 0) { kind = Pip; } else if (i % 3 == 1) { kind = Bad(i); }
    let keep: boolean = i % 2 == 0;
    return (Tab { ...t, b: t.b.with(at, i) }, keep, kind);
}
@noinline function weigh(t: Tab, k: boolean): i32 {
    if (k) { return t.a[0] + t.b[0] + t.c[0]; }
    return t.b[0];
}
@noinline function rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: Tab = Tab { a: [0, 0], b: [0, 0], c: [0, 0] };
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let took: (Tab, boolean, Kind) = took(t, 0, i);
        t = took.0;
        total = total + note(took.2) + weigh(t, took.1);
        i = i + 1;
    }
    return total * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(rounds()); print("");
    return 0;
}
`

var reuseCarryProduced = []string{"took", "note", "weigh", "rounds"}

func TestSelfHostReuseCarry(t *testing.T) {
	runSemanticProgram(t, "reusecarry", reuseCarryProgram, reuseCarryProduced,
		semInlineWants("11617037\n"), "note", "weigh")
}

// Each read below retires its stream, and the retire rebuilds that stream in
// its own box.
const reuseCarryBlockDonorProgram = `import "std/quic/streams";
import "std/quic/frames";
import "std/quic/params";
function limits(n: i64): params.Params {
  return params.Params { ...params.defaults(), initial_max_streams_bidi: n, initial_max_streams_uni: n, initial_max_stream_data_uni: 100 as i64, initial_max_data: 1000000 as i64 };
}
function main(): i32 {
  let s: streams.Streams = streams.table(true, limits(1000 as i64)).with_peer(limits(1000 as i64));
  let reads: i64 = 0;
  let k: i64 = 0;
  while (k < 200) {
    let id: i64 = k * 4 + 2;
    s = s.on_frame(frames.StreamFrame(frames.StreamData { stream: id, offset: 0 as i64, data: [1, 2, 3], fin: true }));
    let before: i64 = __heap_alloc_count();
    s = s.read(id);
    reads = reads + (__heap_alloc_count() - before);
    k = k + 1;
  }
  print(s.retired.to_string() + " " + reads.to_string());
  return 0;
}
`

// A construction that a value dying in its own block can build in keeps that
// donor. A donor carried from an earlier block does not take its place.
func TestSelfHostReuseCarryKeepsBlockDonor(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(reuseCarryBlockDonorProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "8 9\n"
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var cmd *exec.Cmd
			switch target {
			case "x86-64-linux":
				cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src))
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				cmd = runArm64Bin(qemu, cli.arm64Binary(t, src))
			default:
				cmd = exec.Command(e2eharness.Wasmtime(t), "run", cli.emit(t, src, target))
			}
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != want {
				t.Fatalf("%s: %v: got %q, want %q (retired streams, allocations over 200 reads)", target, err, out, want)
			}
		})
	}
}
