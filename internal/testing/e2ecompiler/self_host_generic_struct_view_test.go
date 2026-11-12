package e2ecompiler

import (
	"bytes"
	"os/exec"
	"regexp"
	"testing"
)

// A view of a generic struct, as a parameter type and as the type argument
// of a generic function, names the instance of the struct.
const genericStructViewProg = `import "std/array";

struct El[M] { name: string, msgs: M[] }

enum Msg { A, B }

function text[M](name: string, m: M): El[M] {
  return El { name: name, msgs: [m] };
}

function names(els: [El[Msg]]): string {
  let out = "";
  for e in els {
    out += e.name;
  }
  return out;
}

function both[T](a: T[], b: [T]): T[] {
  let out: T[] = [];
  for x in a {
    out = out.append(x);
  }
  for y in b {
    out = out.append(y);
  }
  return out;
}

function main(): i32 {
  let top: El[Msg][] = [text("a", A)];
  let kids: El[Msg][] = [text("b", B), text("c", A)];
  let all = top.concat(kids);
  let twice = both(all, kids);
  print(names(all) + " " + names(twice) + " " + names(kids[1:2]) + " " + twice[3].msgs.len().to_string());
  return 0;
}
`

func TestSelfHostViewOfAGenericStruct(t *testing.T) {
	cli := buildSelfHostCLI(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	const want = "abc abcbc c 1\n"
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, sanitize := range []bool{false, true} {
				var flags []string
				if sanitize {
					flags = append(flags, "-sanitize")
				}
				bin := compileCPUProfile(t, cli, genericStructViewProg, target, "", flags...)
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
				if got := stdout.String(); got != want {
					t.Errorf("sanitize=%v: got %q, want %q", sanitize, got, want)
				}
				if !sanitize {
					continue
				}
				if m := leak.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
					t.Fatalf("unbalanced sanitized run: %s", &stderr)
				}
			}
		})
	}
}
