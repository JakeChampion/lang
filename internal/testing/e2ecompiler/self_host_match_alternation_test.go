package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

const alternationSource = `@noinline function hit(x: i32): i32 {
  return x * 3 + 1;
}

@noinline function classify(x: i32): i32 {
  match (x) {
    1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 => {
      return hit(x);
    },
    10..=11 | 13 => {
      return hit(x + 100);
    },
    _ => {
      return 0;
    },
  }
}

function main(): i32 {
  let total = 0;
  for x in 0..16 {
    total = total + classify(x);
  }
  print(total);
  return 0;
}
`

// TestSelfHostLiteralAlternationSharesBody pins #14074: the alternatives of
// one literal match arm share one copy of the arm body. A copy for each
// alternative made a large arm push its function over the inliner's budget.
func TestSelfHostLiteralAlternationSharesBody(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(alternationSource), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "1121\n"
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			body := asmFunction(string(asm), "__fn_classify:")
			call := regexp.MustCompile(`(?m)^\s*(call|jmp) __fn_hit\b`)
			if target == "wasm32-wasi" {
				bodies := wasmFuncBodies(string(asm), "$classify")
				if len(bodies) != 1 {
					t.Fatalf("found %d wasm functions named classify, want 1", len(bodies))
				}
				body = bodies[0]
				call = regexp.MustCompile(`call \$hit\b`)
			}
			if body == "" {
				t.Fatal("no classify function in the emitted assembly")
			}
			if n := len(call.FindAllString(body, -1)); n != 2 {
				t.Errorf("classify calls hit at %d sites, want 2 (one for each written arm):\n%s", n, body)
			}
			var cmd *exec.Cmd
			if target == "x86-64-linux" {
				cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src))
			} else {
				cmd = exec.Command("wasmtime", "run", cli.emit(t, src, target))
			}
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != want {
				t.Errorf("run: %v: got %q, want %q", err, out, want)
			}
		})
	}
}
