package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// concat takes its second operand as a view: a slice handed to it is read
// where it points, so appending one to an array with room allocates nothing.
// The other cases cover a growing concat, wide and pointer-width elements,
// a byte view of a string, and string elements, which take the generic path.
const concatViewProg = `import "std/array";

@noinline function grow(own out: u8[], src: u8[], from: i32, to: i32): u8[] {
  return out.concat(src[from:to]);
}

@noinline function grow_view(own out: u8[], src: [u8]): u8[] {
  return out.concat(src);
}

@noinline function wide(own out: i64[], src: i64[], from: i32, to: i32): i64[] {
  return out.concat(src[from:to]);
}

@noinline function words(own out: i32[], src: [i32]): i32[] {
  return out.concat(src);
}

@noinline function texts(own out: string[], src: string[], from: i32, to: i32): string[] {
  return out.concat(src[from:to]);
}

function sum(xs: [u8]): i32 {
  let n: i32 = 0;
  for x in xs {
    n = n + x as i32;
  }
  return n;
}

function main(): i32 {
  unsafe {
    let src: u8[] = [1, 2, 3, 4, 5, 6, 7, 8];
    let out: u8[] = __alloc_u8(4096);
    __arr_set_len(out, 0);
    out = grow(out, src, 0, 1);
    let before: i64 = __heap_alloc_count();
    for i in 0..1000 {
      out = grow(out, src, i % 4, i % 4 + 3);
    }
    let allocs: i64 = __heap_alloc_count() - before;
    let grown: u8[] = grow([9], src, 2, 6);
    let text: string = "hi";
    let viewed: u8[] = grow_view([0], text.as_bytes());
    let w: i64[] = wide([10 as i64], [1 as i64, 2 as i64, 3 as i64, 4 as i64], 1, 3);
    let four: i32[] = [1, 2, 3, 4];
    let ws: i32[] = words([5], four[2:4]);
    let ts: string[] = texts(["a"], ["b", "c", "d"], 1, 3);
    print(allocs.to_string() + " " + out.len().to_string() + " " + sum(out).to_string() + " " + sum(grown).to_string() + " " + grown.len().to_string() + " " + sum(viewed).to_string() + " " + (w[0] + w[1] + w[2]).to_string() + " " + (ws[0] * 100 + ws[1] * 10 + ws[2]).to_string() + " " + ts[0] + ts[1] + ts[2]);
    return 0;
  }
}
`

func TestSelfHostConcatReadsAView(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	const want = "0 3001 10501 27 5 209 15 534 acd\n"
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, concatViewProg, target, "", flags...)
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
				got := stdout.String()
				if !sanitize {
					if got != want {
						t.Errorf("got %q, want %q", got, want)
					}
					continue
				}
				if _, rest, _ := strings.Cut(got, " "); rest != strings.SplitN(want, " ", 2)[1] {
					t.Errorf("sanitized: got %q, want %q after the count", got, want)
				}
				if m := leak.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
					t.Fatalf("unbalanced sanitized run: %s", &stderr)
				}
			}
		})
	}
}
