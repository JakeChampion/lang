package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

// The view of s.buf is lent to the call that consumes s. Holding buf for
// the call lets s move, so step updates its record and the inner list in
// place, as it does when the view is of a local holding the same array.
const lentViewProg = `import "std/i64";
struct Inner { xs: i64[], n: i64 }
struct St { buf: u8[], inner: Inner[] }

@noinline function (x: Inner) bumped(d: [u8]): Inner {
  return Inner { ...x, xs: x.xs.with(0, x.xs[0] + d.len() as i64), n: x.n + 1 };
}

@noinline function step(s: St, d: [u8]): St {
  return St { ...s, inner: s.inner.with(0, s.inner[0].bumped(d)) };
}

function main(): i32 {
  let s: St = St { buf: __alloc_u8(64), inner: [Inner { xs: [0 as i64, 0 as i64], n: 0 as i64 }] };
  s = step(s, s.buf[0:8]);
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  while (i < 1000) {
    s = step(s, s.buf[0:8]);
    i = i + 1;
  }
  let field: i64 = __heap_alloc_count() - before;
  let buf: u8[] = s.buf;
  before = __heap_alloc_count();
  i = 0;
  while (i < 1000) {
    s = step(s, buf[0:8]);
    i = i + 1;
  }
  let local: i64 = __heap_alloc_count() - before;
  if (s.inner[0].n != 2001 as i64 || s.inner[0].xs[0] != 16008 as i64 || s.buf.len() != 64) {
    print("wrong " + s.inner[0].n.to_string());
    return 1;
  }
  print(field.to_string() + " " + local.to_string());
  return 0;
}
`

func TestSelfHostLentViewLetsItsRecordMove(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, lentViewProg, target, "", flags...)
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
					// wasm boxes each lent slice's descriptor (#13207), as many
					// through the field as through the local.
					want := "0 0\n"
					if target == "wasm32-wasi" {
						want = "1000 1000\n"
					}
					if got := stdout.String(); got != want {
						t.Errorf("1000 calls through the field and through a local allocate %q, want %q", got, want)
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
