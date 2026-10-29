package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

const fullRecordUpdateProg = `struct P { low: i64, high: i64 }
struct H { ps: P[], d: i64 }

function raised(r: P, x: i64): P {
  return P { ...r, high: x };
}

function spanned(r: P, lo: i64, hi: i64): P {
  return P { ...r, low: lo, high: hi };
}

function (h: H) partial(x: i64): H {
  return H { ...h, ps: h.ps.with(0, raised(h.ps[0], x)) };
}

function (h: H) full(x: i64): H {
  return H { ...h, ps: h.ps.with(0, spanned(h.ps[0], x - 1i64, x)) };
}

function main(): i32 {
  let h: H = H { ps: [P { low: 0i64, high: 0i64 }], d: 0i64 };
  h = h.partial(1i64);
  h = h.full(2i64);
  let a0: i64 = __heap_alloc_count();
  let i: i64 = 0i64;
  while (i < 1000i64) {
    h = h.partial(i);
    i = i + 1i64;
  }
  let a1: i64 = __heap_alloc_count();
  i = 0i64;
  while (i < 1000i64) {
    h = h.full(i);
    i = i + 1i64;
  }
  let a2: i64 = __heap_alloc_count();
  if (h.ps[0].low != 998i64 || h.ps[0].high != 999i64) {
    print("wrong");
    return 1;
  }
  print((a1 - a0).to_string() + " " + (a2 - a1).to_string());
  return 0;
}
`

// A record update that writes every field builds in the box of the element it
// replaces, as an update that keeps a field does (#13334).
func TestSelfHostFullRecordUpdateReusesItsBase(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, fullRecordUpdateProg, target, "", flags...)
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
					if got := stdout.String(); got != "0 0\n" {
						t.Errorf("1000 partial and 1000 full updates allocate %q times, want \"0 0\"", got)
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
