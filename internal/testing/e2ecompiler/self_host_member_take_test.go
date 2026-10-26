package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

// Each step takes a struct-union value out of an array slot and grows an
// array field of its member, 20,000 times. A grown array that nothing else
// holds must grow in place, so every count printed must be 0 (#13415):
//   - call: the member is handed to a callee that rebuilds it.
//   - spliced: the same callee, small enough to be spliced into the caller.
//   - method: the slot is emptied by a method the record is handed to.
const memberTakeProg = `struct VI { v: i32 }
struct VA { items: V[], tag: string }
type V = VI | VA;
struct Env { names: string[], values: V[] }

@noinline
function grow(av: VA, x: V): V {
  return VA { ...av, items: av.items.append(x) };
}

function grow_or_reset(av: VA, x: V): V {
  if (av.tag.len() > 0) {
    let none: V[] = [];
    return VA { ...av, items: none.append(x) };
  }
  return VA { ...av, items: av.items.append(x) };
}

@noinline
function (e: Env) update(name: string, v: V): Env {
  let i: i32 = e.names.len() - 1;
  while (i >= 0) {
    if (e.names[i] == name) {
      return Env { ...e, values: e.values.with(i, v) };
    }
    i = i - 1;
  }
  return e;
}

@noinline
function step_call(env: Env, i: i32): Env {
  let taken: V = env.values[1];
  let emptied: Env = Env { ...env, values: env.values.with(1, VI { v: 0 }) };
  if let VA(cur) = taken {
    return Env { ...emptied, values: emptied.values.with(1, grow(cur, VI { v: i })) };
  }
  return emptied;
}

@noinline
function step_spliced(env: Env, i: i32): Env {
  let taken: V = env.values[1];
  let emptied: Env = Env { ...env, values: env.values.with(1, VI { v: 0 }) };
  if let VA(cur) = taken {
    return Env { ...emptied, values: emptied.values.with(1, grow_or_reset(cur, VI { v: i })) };
  }
  return emptied;
}

@noinline
function step_method(env: Env, i: i32): Env {
  let taken: V = env.values[1];
  let emptied: Env = env.update("xs", VI { v: 0 });
  if let VA(cur) = taken {
    return Env { ...emptied, values: emptied.values.with(1, VA { ...cur, items: cur.items.append(VI { v: i }) }) };
  }
  return emptied;
}

function fresh(): Env {
  let none: V[] = [];
  return Env { names: ["i", "xs"], values: [VI { v: 0 }, VA { items: none, tag: "" }] };
}

function check(env: Env): boolean {
  if let VA(a) = env.values[1] {
    if (a.items.len() != 20000) {
      return false;
    }
    if let VI(last) = a.items[19999] {
      return last.v == 19999;
    }
  }
  return false;
}

function main(): i32 {
  let names: string[] = ["call", "spliced", "method"];
  let k: i32 = 0;
  while (k < 3) {
    let env: Env = fresh();
    let before: i32 = __arr_push_shared_count();
    let i: i32 = 0;
    while (i < 20000) {
      if (k == 0) {
        env = step_call(env, i);
      } else if (k == 1) {
        env = step_spliced(env, i);
      } else {
        env = step_method(env, i);
      }
      i = i + 1;
    }
    if (!check(env)) {
      print(names[k] + " wrong");
      return 1;
    }
    print(names[k] + " " + (__arr_push_shared_count() - before).to_string());
    k = k + 1;
  }
  return 0;
}
`

func TestSelfHostMemberTakeGrowsInPlace(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, memberTakeProg, target, "", flags...)
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
				if want := "call 0\nspliced 0\nmethod 0\n"; stdout.String() != want {
					t.Errorf("sanitize=%v: printed %q, want %q", sanitize, stdout.String(), want)
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
