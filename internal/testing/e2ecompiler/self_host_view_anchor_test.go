package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Each function puts a view of an array in a container, reads the view out
// of the container and then reads an element. The array must stay in memory
// until the last read through the view. Under -harden a released block is
// zero, so a release that comes too early gives a sum of 0.
const viewAnchorSrc = `import "std/i32";
import "std/string";

struct Window { events: [i32] }
struct Outer { w: Window, n: i32 }
struct Bytes { b: [u8] }

@noinline
function make(k: i32): i32[] {
  let out: i32[] = [];
  out = out.append(k);
  return out.append(k + 1);
}

@noinline
function field(k: i32): i32 {
  let events: i32[] = make(k);
  let w: Window = Window { events: events[:] };
  return w.events[0] + w.events[1];
}

@noinline
function nested(k: i32): i32 {
  let events: i32[] = make(k);
  let o: Outer = Outer { w: Window { events: events[:] }, n: 1 };
  let w: Window = o.w;
  return w.events[0] + w.events[1];
}

@noinline
function pair(k: i32): i32 {
  let events: i32[] = make(k);
  let t: ([i32], i32) = (events[:], 1);
  let v: [i32] = t.0;
  return v[0] + v[1];
}

@noinline
function option(k: i32): i32 {
  let events: i32[] = make(k);
  let o: Option[[i32]] = Some(events[:]);
  if let Some(v) = o {
    return v[0] + v[1];
  }
  return 0;
}

@noinline
function twice(k: i32): i32 {
  let events: i32[] = make(k);
  let w: Window = Window { events: events[:] };
  let a: [i32] = w.events;
  let b: [i32] = w.events;
  return a[0] + b[1];
}

@noinline
function element(k: i32): i32 {
  let events: i32[] = make(k);
  let ws: Window[] = [Window { events: events[:] }];
  let w: Window = ws[0];
  return w.events[0] + w.events[1];
}

@noinline
function bytes(k: i32): i32 {
  let text: u8[] = (k.to_string() + "7").bytes();
  let w: Bytes = Bytes { b: text[:] };
  return (w.b[0] as i32) + (w.b[1] as i32);
}

function main(): i32 {
  print(f"field {field(5)}");
  print(f"nested {nested(5)}");
  print(f"pair {pair(5)}");
  print(f"option {option(5)}");
  print(f"twice {twice(5)}");
  print(f"element {element(5)}");
  print(f"bytes {bytes(5)}");
  return 0;
}
`

const viewAnchorWant = "field 11\nnested 11\npair 11\noption 11\ntwice 11\nelement 11\nbytes 108\n"

func TestSelfHostViewOutOfContainerKeepsItsArray(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(viewAnchorSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "main")
			build := runX86_64Bin(cli.runner, cli.bin, "-harden", "-target", target, "-o", bin, src, cli.stdlib)
			build.Env = childEnv()
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("self-host CLI: %v\n%s", err, out)
			}
			var c *exec.Cmd
			if target == "arm64-linux" {
				c = runArm64Bin(qemu, bin)
			} else {
				c = runX86_64Bin(cli.runner, bin)
			}
			var stdout, stderr bytes.Buffer
			c.Stdout, c.Stderr = &stdout, &stderr
			if err := c.Run(); err != nil {
				t.Fatalf("run: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
			}
			if got := stdout.String(); got != viewAnchorWant {
				t.Errorf("the program printed %q, want %q", got, viewAnchorWant)
			}
		})
	}
}
