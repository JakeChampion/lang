package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

const optionToResultProg = `struct E { code: i32 }

@noinline function some(i: i64): Option[i64[]] {
  if (i < 0i64) { return None; }
  return Some([i, i + 1i64]);
}

@noinline function to_result(o: Option[i64[]], e: E): Result[i64[], E] {
  match (o) {
    Some(a) => { return Ok(a); },
    None => { return Err(e); }
  }
}

function main(): i32 {
  let e: E = E { code: 7 };
  let n: i64 = 0i64;
  let i: i64 = 0i64;
  let a0: i64 = __heap_alloc_count();
  while (i < 1000i64) {
    match (some(i)) { Some(a) => { n = n + a[1]; }, None => {} }
    i = i + 1i64;
  }
  let a1: i64 = __heap_alloc_count();
  i = 0i64;
  while (i < 1000i64) {
    match (to_result(some(i), e)) { Ok(a) => { n = n + a[1]; }, Err(x) => { n = n + x.code; } }
    i = i + 1i64;
  }
  let a2: i64 = __heap_alloc_count();
  match (to_result(some(0i64 - 1i64), e)) { Ok(a) => {}, Err(x) => { n = n + x.code; } }
  if (n != 1001007i64) {
    print("wrong");
    return 1;
  }
  print(((a2 - a1) - (a1 - a0)).to_string());
  return 0;
}
`

// A match that takes a unique Some box builds the Ok in that box, though the
// two variants are of different enum types (#13788).
func TestSelfHostOptionToResultReusesTheSomeBox(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, optionToResultProg, target, "", flags...)
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
						t.Errorf("1000 conversions allocate %q times more than the Some values alone, want \"0\"", got)
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
