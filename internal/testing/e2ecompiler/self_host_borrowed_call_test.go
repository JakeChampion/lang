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

// set reads the map out of the walk, then hands the walk to flag, which
// only reads it, and then updates the map. The read moves down below the
// call, so the update takes the map from the walk rather than copying it.
const borrowedCallSameBlockProg = `import "core/map";
struct Walk { seen: i32, flags: Map[string, i32], list: string[] }

@noinline function flag(w: Walk, h: string): i32 {
  return w.flags.get_or(h, 0);
}

@noinline function set(w: Walk, h: string, f: i32): Walk {
  return Walk { ...w, flags: w.flags.insert(h, flag(w, h) ^ f) };
}

function main(): i32 {
  let w = Walk { seen: 0, flags: Map { }, list: ["a"] };
  w = set(w, "k", 1);
  let before: i64 = __heap_alloc_count();
  let i = 0;
  while (i < 1000) {
    w = set(w, "k", i);
    i += 1;
  }
  let allocs: i64 = __heap_alloc_count() - before;
  if (w.flags.get_or("k", -1) != 1 || w.flags.len() != 1 || w.list[0] != "a") {
    print("wrong");
    return 1;
  }
  print(allocs.to_string());
  return 0;
}
`

// probe gives a field of a pack to env, whose result holds no view. The
// call lends the store to nothing, so read counts its store and updates
// the map of the store in place. The count leaves out what env allocates,
// which is not zero on each target.
const borrowedBuiltinProg = `import "core/map";
struct Pack { name: string, n: i32 }
struct Store { packs: Pack[], seen: Map[string, i32], reads: i32 }

@noinline function probe(p: Pack): i32 {
  if let Some(v) = env(p.name) {
    return v.len();
  }
  return p.n;
}

@noinline function read(s: Store): Store {
  let n = probe(s.packs[0]);
  return Store { ...s, seen: s.seen.insert("k", s.seen.get_or("k", 0) + n), reads: s.reads + 1 };
}

function main(): i32 {
  let s = Store { packs: [Pack { name: "FERN_TEST_UNSET_NAME", n: 1 }], seen: Map { }, reads: 0 };
  s = read(s);
  let start: i64 = __heap_alloc_count();
  let i = 0;
  let n = 0;
  while (i < 1000) {
    n += probe(s.packs[0]);
    i += 1;
  }
  let probes: i64 = __heap_alloc_count() - start;
  let before: i64 = __heap_alloc_count();
  i = 0;
  while (i < 1000) {
    s = read(s);
    i += 1;
  }
  let allocs: i64 = __heap_alloc_count() - before - probes;
  if (n != 1000) {
    print("wrong probe");
    return 1;
  }
  if (s.seen.get_or("k", 0) != 1001 || s.reads != 1001) {
    print("wrong");
    return 1;
  }
  print(allocs.to_string());
  return 0;
}
`

func TestSelfHostBorrowedCallLetsTheSlotMove(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	progs := []struct{ name, src string }{
		{"table", borrowedCallProg},
		{"same-block", borrowedCallSameBlockProg},
		{"builtin", borrowedBuiltinProg},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, prog := range progs {
			t.Run(target+"/"+prog.name, func(t *testing.T) {
				borrowedCallRun(t, cli, leak, prog.src, target)
			})
		}
	}
}

func borrowedCallRun(t *testing.T, cli *selfHostCLI, leak *regexp.Regexp, src, target string) {
	t.Helper()
	for _, sanitize := range []bool{false, true} {
		var flags []string
		if sanitize {
			flags = append(flags, "-sanitize")
		}
		bin := compileCPUProfile(t, cli, src, target, "", flags...)
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
}
