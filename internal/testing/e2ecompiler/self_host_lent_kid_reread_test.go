package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

// The entry block reads w.e and lends its defs field to two calls. The
// update block must read w.e again, so that the take of e0.defs.insts is
// unique and the append does not copy. The donor rule of the kept-read
// check once took the lend as a consumption and kept the first read, so
// each call copied the three arrays (#14179).
const lentKidRereadProg = `struct SInst { kind: i32, result: i32, args: i32[], str: string }
struct Head { of: i32, ins: SInst }
struct Defs { insts: SInst[], blocks: i32[] }
struct Edits { defs: Defs, added: i32[], heads: Head[], base: i32, kept: boolean[] }
struct Walk { e: Edits, value: i32, phis: i32[], parts: i32[] }
struct Part { ok: boolean, value: i32 }

@noinline function no_inst(): SInst { return SInst { kind: 0, result: 0 - 1, args: [], str: "" }; }
@noinline function through_copies(d: Defs, v: i32): i32 { return v; }
@noinline function def_inst(d: Defs, x: i32): SInst { if (x >= 0 && x < d.insts.len()) { return d.insts[x]; } return no_inst(); }
@noinline function builds(k: i32): boolean { return k == 7; }
@noinline function index_of(xs: i32[], x: i32): i32 { for i in 0..xs.len() { if (xs[i] == x) { return i; } } return 0 - 1; }
@noinline function made_part(e: Edits, x: i32): i32 { for h in e.heads { if (h.of == x) { return h.ins.result; } } return 0 - 1; }
@noinline function part_value(own w: Walk, p: Part): Walk { return Walk { ...w, value: p.value }; }

function part_of(own w: Walk, v: i32, depth: i32): Walk {
  let x: i32 = through_copies(w.e.defs, v);
  let d: SInst = def_inst(w.e.defs, x);
  if (builds(d.kind)) {
    return part_value(w, Part { ok: true, value: 0 - 1 });
  }
  let seen: i32 = index_of(w.phis, x);
  if (seen >= 0) {
    return Walk { ...w, value: w.parts[seen] };
  }
  let made: i32 = made_part(w.e, x);
  if (made >= 0) {
    return Walk { ...w, value: made };
  }
  let e0: Edits = w.e;
  let d0: Defs = e0.defs;
  let nv: i32 = e0.base + e0.added.len();
  let pb: i32 = d0.blocks.len();
  let cur: Walk = Walk {
    e: Edits { ...e0, defs: Defs { insts: d0.insts.append(no_inst()), blocks: d0.blocks.append(pb) }, added: e0.added.append(x) },
    value: 0 - 1, phis: w.phis.append(x), parts: w.parts.append(nv),
  };
  let args: i32[] = [];
  for a in d.args {
    cur = part_of(cur, a, depth + 1);
    args = args.append(cur.value);
  }
  let ins: SInst = SInst { kind: 8, result: nv, args: args, str: "" };
  let e1: Edits = cur.e;
  let d1: Defs = e1.defs;
  return Walk { ...cur, e: Edits { ...e1, defs: Defs { ...d1, insts: d1.insts.with(nv, ins) }, heads: e1.heads.append(Head { of: x, ins: ins }) }, value: nv };
}

function main(): i32 {
  let w: Walk = Walk { e: Edits { defs: Defs { insts: [], blocks: [] }, added: [], heads: [], base: 0, kept: [] }, value: 0, phis: [], parts: [] };
  let before: i32 = __arr_push_shared_count();
  for i in 0..2000 {
    w = part_of(w, 1000000 + i, 0);
  }
  let crossings: i32 = __arr_push_shared_count() - before;
  print(crossings);
  return if (crossings == 0) { 0 } else { 1 };
}
`

func TestSelfHostLentKidDoesNotKeepFirstRead(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, lentKidRereadProg, target, "", flags...)
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
						t.Errorf("2000 calls cross the append cliff %q times, want 0", got)
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
