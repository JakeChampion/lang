package e2ecompiler

import (
	"strings"
	"testing"
)

// Under -harden the link inside a free block is encoded with the process
// canary and the block's page, and a decoded link must be aligned and inside
// the arena (SEC-023). A raw write through a dangling pointer that forges the
// link of the head block aborts at the next allocation of its class.
func freeLinkForgeSrc(size string) string {
	return `function main(): i32 {
  unsafe {
    let a: usize = __alloc(` + size + `);
    let b: usize = __alloc(` + size + `);
    __free(a, ` + size + `);
    __free(b, ` + size + `);
    print("freed two blocks");
    __store_i64(b, 4096i64);
    let c: usize = __alloc(` + size + `);
    print("took a block after the forge");
    return 0;
  }
}
`
}

// The control releases twelve blocks of one class and takes twelve again:
// every block comes back once, through the encoded links and the random pick
// among the first eight.
func freeLinkRoundTripSrc(size string) string {
	return `function main(): i32 {
  unsafe {
    let got: i64[] = [];
    let i: i32 = 0;
    while (i < 12) {
      let p: usize = __alloc(` + size + `);
      __store_i64(p, i as i64);
      got = got.append(p as i64);
      i = i + 1;
    }
    i = 0;
    while (i < 12) {
      __free(got[i] as usize, ` + size + `);
      i = i + 1;
    }
    let again: i64[] = [];
    i = 0;
    while (i < 12) {
      again = again.append(__alloc(` + size + `) as i64);
      i = i + 1;
    }
    let ok: boolean = true;
    i = 0;
    while (i < 12) {
      let found: i32 = 0;
      let j: i32 = 0;
      while (j < 12) {
        if (again[i] == got[j]) {
          found = found + 1;
        }
        if (j != i && again[j] == again[i]) {
          ok = false;
        }
        j = j + 1;
      }
      if (found != 1) {
        ok = false;
      }
      i = i + 1;
    }
    print(ok.to_string());
    return 0;
  }
}
`
}

func TestSelfHostFreeListLinkForgery(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, size := range []string{"64", "1048576"} {
			t.Run(target+"/"+size, func(t *testing.T) {
				stdout, stderr, code := runRawFreeProbe(t, cli, qemu, target, freeLinkForgeSrc(size), "-harden")
				want := "freed two blocks\n"
				if code != 134 || stdout != want || !strings.HasPrefix(stderr, "fern: corrupt free list link\n") {
					t.Fatalf("exit %d, stdout %q, stderr %q; want 134 after %q with the corrupt-link cause", code, stdout, stderr, want)
				}
			})
		}
	}
}

func TestSelfHostHardenFreeListRoundTrip(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, size := range []string{"64", "1048576"} {
			t.Run(target+"/"+size, func(t *testing.T) {
				stdout, stderr, code := runRawFreeProbe(t, cli, qemu, target, freeLinkRoundTripSrc(size), "-harden")
				if code != 0 || stdout != "true\n" {
					t.Fatalf("exit %d, stdout %q, stderr %q; want 0 and true", code, stdout, stderr)
				}
			})
		}
	}
}

// A string that read_file returns has its bytes in a block of their own, so
// its release pushes the three-word box through the string free path. The
// next allocation of that class must decode the box's link.
const freeLinkStringBoxSrc = `import "std/i32";
import "std/string";

enum Kind { Circle(i32), Empty }

@noinline
function kind(k: i32): Kind {
  if (k == 1) {
    return Kind.Circle(5);
  }
  return Kind.Empty;
}

function main(): i32 {
  match (kind(args().len())) {
    Kind.Circle(r) => { print("circle " + r.to_string()); },
    Kind.Empty => { print("empty"); }
  }
  let n: i32 = 0;
  match (read_file("/proc/self/maps")) {
    Ok(m) => {
      for line in m.split("\n") {
        n = n + line.split("-")[0].len();
      }
    },
    Err(_) => { print("no maps"); }
  }
  print((n > 0).to_string());
  return 0;
}
`

func TestSelfHostHardenFreeListStringBoxes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stdout, stderr, code := runRawFreeProbe(t, cli, qemu, target, freeLinkStringBoxSrc, "-harden")
			if code != 0 || stdout != "circle 5\ntrue\n" {
				t.Fatalf("exit %d, stdout %q, stderr %q; want 0 and the two lines", code, stdout, stderr)
			}
		})
	}
}
