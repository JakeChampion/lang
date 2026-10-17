package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// `take` is inlined into `walk`, and its `Result[Got, Bad]` is split into its
// parts: every return passes the payloads of the variant it built and a zero
// for each of the others. A zero's unit is the null word, so the paths that
// leave the loop must drop the zeros they carry without a call.
const zeroDropProg = `struct Bad { code: i32, why: string }
struct Item { tag: i32, body: u8[] }
enum Got { Whole(Item, i32), More }

function take(buf: u8[], at: i32): Result[Got, Bad] {
  if (buf.len() - at < 2) {
    return Ok(More);
  }
  let n: i32 = buf[at] as i32;
  if (n > 9) {
    return Err(Bad { code: 1, why: "too long" });
  }
  if (buf.len() - at - 2 < n) {
    return Ok(More);
  }
  let kind: i32 = buf[at + 1] as i32;
  if (kind == 7) {
    return Err(Bad { code: 2, why: "seven" });
  }
  return Ok(Whole(Item { tag: kind, body: __bytes_range(buf, at + 2, at + 2 + n) }, n + 2));
}

function walk(buf: u8[]): i32 {
  let at: i32 = 0;
  let sum: i32 = 0;
  while (true) {
    match (take(buf, at)) {
      Ok(got) => {
        match (got) {
          Whole(item, n) => {
            sum = sum + item.tag + item.body.len();
            at = at + n;
          },
          More => {
            return sum;
          }
        }
      },
      Err(e) => {
        return 0 - e.code;
      }
    }
  }
  return sum;
}

function main(): i32 {
  if (walk([3, 1, 9, 9, 9, 0, 2, 2, 4]) != 6) {
    return 1;
  }
  if (walk([12, 1]) != 0 - 1 || walk([1, 7, 0]) != 0 - 2) {
    return 2;
  }
  print("0");
  return 0;
}
`

// A release helper called on a constant null, per target.
var zeroDropCall = map[string]*regexp.Regexp{
	"x86-64-linux": regexp.MustCompile(`xorl %eax, %eax\n\s+call __fn___sem_(release|drop)_`),
	"arm64-linux":  regexp.MustCompile(`mov x0, #0\n\s+bl __fn___sem_(release|drop)_`),
	"wasm32-wasi":  regexp.MustCompile(`i32\.const 0\n\s+call \$__sem_(release|drop)_`),
}

func TestSelfHostZeroDropsNoCall(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(zeroDropProg), 0o644); err != nil {
		t.Fatal(err)
	}
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			raw, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			if n := len(zeroDropCall[target].FindAllIndex(raw, -1)); n != 0 {
				t.Errorf("%d release calls on a constant null", n)
			}
			bin := compileCPUProfile(t, cli, zeroDropProg, target, "", "-sanitize")
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
			if err := cmd.Run(); err != nil || stdout.String() != "0\n" {
				t.Fatalf("%v: stdout %q; stderr %s", err, &stdout, &stderr)
			}
			if m := leak.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
				t.Fatalf("unbalanced sanitized run: %s", &stderr)
			}
		})
	}
}
