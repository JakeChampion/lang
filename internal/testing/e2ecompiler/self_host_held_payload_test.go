package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

// The callee takes c owned and reads k. The caller reads k out of c, so it
// must keep k alive across the call. Holding k keeps c unique, and the update
// in apply reuses its box. Pinning c instead shares it, and the update copies.
const heldPayloadProg = `import "std/i64";
struct K { id: i64, name: string }
struct C { key: Option[K], total: i64, last: string }

@noinline function (c: C) apply(k: K, n: i64): C {
  if (k.id == 0 as i64) {
    return c;
  }
  return C { ...c, total: c.total + n * k.id };
}

@noinline function (c: C) step(n: i64): C {
  let Some(k) = c.key else {
    return c;
  };
  return c.apply(k, n);
}

function main(): i32 {
  let c: C = C { key: Some(K { id: 7 as i64, name: "k" }), total: 0 as i64, last: "" };
  c = c.step(1 as i64);
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 1000) {
    c = c.step(1 as i64);
    i = i + 1;
  }
  let allocs: i64 = __heap_alloc_count() - before;
  if (c.total != 7007 as i64) {
    print("wrong " + c.total.to_string());
    return 1;
  }
  print(allocs.to_string());
  return 0;
}
`

func TestSelfHostHeldPayloadBesideMovedRoot(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, heldPayloadProg, target, "", flags...)
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
						t.Errorf("1000 updates beside a held payload allocate %q, want 0", got)
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
