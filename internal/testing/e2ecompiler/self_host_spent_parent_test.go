package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

// walk reads a record's array element in one block and uses it again after
// the record goes whole into a call, as semsource.iterate_array does with the
// map type it reads off its state. semsink must not read the element again
// through the record after that call: the record is the call's, and a re-read
// keeps it alive, so the call copies it. Without the check, 30 calls allocate
// 250 boxes on x86-64, against 210.
const spentParentProg = `import "std/i32";
import "std/i64";
struct St { ok: boolean, tag: string, values: string[] }
struct Val { s: St, id: i32 }

@noinline function produce(names: string[], at: i32): Val {
  let fresh: string[] = [];
  for n in names {
    fresh = fresh.append(n);
  }
  return Val { s: St { ok: true, tag: "t", values: fresh }, id: at };
}

@noinline function column(own s: St, key: string): St {
  return St { ...s, tag: s.tag + key, values: s.values.append(key) };
}

@noinline function fail(own s: St, why: string): St {
  return St { ...s, ok: false, tag: why };
}

@noinline function spelling(x: string): string {
  return "<" + x + ">";
}

@noinline function walk(names: string[], at: i32): St {
  let v: Val = produce(names, at);
  let s: St = v.s;
  if (!s.ok) {
    return s;
  }
  let it: i32 = v.id;
  let wide: boolean = spelling(s.values[it]).len() > 5;
  let extra: string = "";
  if (wide) {
    extra = "w";
  }
  if (spelling(s.values[it]).len() > 3) {
    let m: string = s.values[it];
    if (m.len() == 2) {
      return fail(s, "short " + spelling(m));
    }
    let k: St = column(s, m);
    if (!k.ok) {
      return k;
    }
    let j: St = column(k, m);
    return St { ...j, tag: j.tag + m + extra };
  }
  return s;
}

function main(): i32 {
  let names: string[] = ["alpha", "be", "cee"];
  let total: i32 = 0;
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 30) {
    let st: St = walk(names, i % 3);
    total = total + st.tag.len() + st.values.len();
    i = i + 1;
  }
  let allocs: i64 = __heap_alloc_count() - before;
  print(total.to_string() + " " + allocs.to_string());
  return 0;
}
`

func TestSelfHostRereadNotThroughSpentParent(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	want := map[string]string{"x86-64-linux": "500 210\n", "arm64-linux": "500 210\n", "wasm32-wasi": "500 230\n"}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, spentParentProg, target, "", flags...)
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
					if got := stdout.String(); got != want[target] {
						t.Errorf("got %q, want %q", got, want[target])
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
