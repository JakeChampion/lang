package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

// step holds its first read of b.insts, so the planner asks the takes again.
// That ask takes a re-read of b.insts and then an element out of it, but
// slot_held refuses the re-read, since the held read still loads the slot.
// The element take must go too: insts is unique to its block, so the take
// empties the slot in place, and emit's next call reads the hole. Before, the
// x86-64 build crashed here, as the compiler built by itself did in
// asm_ir.ssa_block_inst.
const twinSourceProg = `import "std/i32";
struct Inst { kind: i32, result: i32, args: i32[], tag: string }
struct Blk { insts: Inst[], name: string }

@noinline function run_of(b: Blk, i: i32): i32[] {
  let lo: i32 = i;
  while (lo > 0 && b.insts[lo - 1].kind == 2) {
    lo = lo - 1;
  }
  return [lo, i + 1];
}

function fuse(b: Blk, i: i32): i32 {
  if (i < 1 || i >= b.insts.len() || b.insts[i].kind != 2) {
    return 0;
  }
  let first: i32 = b.insts[i].args[0];
  let run: i32[] = run_of(b, i);
  if (run[0] < 1) {
    return 0;
  }
  let prev: Inst = b.insts[run[0] - 1];
  if (prev.result != first) {
    return 0;
  }
  return prev.args[0] + prev.args[1];
}

@noinline function step(b: Blk, i: i32): Blk {
  let ins: Inst = b.insts[i];
  if (ins.kind == 3) {
    return b;
  }
  if (fuse(b, i + 1) > 0) {
    return Blk { insts: [], name: "f" };
  }
  return Blk { insts: [], name: ins.tag };
}

struct Func { blocks: Blk[] }

@noinline function emit(f: Func, own out: string): string {
  let bi: i32 = 0;
  while (bi < f.blocks.len()) {
    let blk: Blk = f.blocks[bi];
    let ii: i32 = 0;
    while (ii < blk.insts.len()) {
      out = out + step(blk, ii).name;
      ii = ii + 1;
    }
    bi = bi + 1;
  }
  return out;
}

@noinline function make_insts(n: i32): Inst[] {
  let insts: Inst[] = [];
  let k: i32 = 0;
  while (k < n) {
    let kind: i32 = 1;
    if (k % 3 == 1) {
      kind = 2;
    }
    insts = insts.append(Inst { kind: kind, result: k, args: [k - 1, k + 1], tag: "t" + k.to_string() });
    k = k + 1;
  }
  return insts;
}

function main(): i32 {
  let f: Func = Func { blocks: [Blk { insts: make_insts(12), name: "a" }, Blk { insts: make_insts(9), name: "b" }] };
  let out: string = "";
  let round: i32 = 0;
  while (round < 3) {
    out = emit(f, out);
    round = round + 1;
  }
  print(out.len().to_string() + " " + f.blocks[1].insts[3].args.len().to_string());
  return 0;
}
`

func TestSelfHostSecondTakeNeedsItsSource(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, twinSourceProg, target, "", flags...)
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
				if got := stdout.String(); got != "117 2\n" {
					t.Errorf("sanitize=%v: got %q, want %q", sanitize, got, "117 2\n")
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
