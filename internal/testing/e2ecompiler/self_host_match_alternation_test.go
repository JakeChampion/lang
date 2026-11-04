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

const selectLetForm = `@noinline function picked(c: boolean, a: i32, b: i32): i32 {
  let base = if (c) { 7 } else { 3 };
  let ptr = if (c) { a } else { b };
  return base * 100 + ptr;
}
`

const selectAssignForm = `@noinline function picked(c: boolean, a: i32, b: i32): i32 {
  let base = 3;
  let ptr = b;
  if (c) {
    base = 7;
    ptr = a;
  }
  return base * 100 + ptr;
}
`

const selectMain = `
function main(): i32 {
  print(picked(true, 5, 6) + picked(false, 5, 6));
  return 0;
}
`

// TestSelfHostSelectJoinsFoldIntoHead pins #14074: two `let` bindings from
// `if` expressions on one condition become selects in one block, as the
// assignment form does. A block boundary between them made the register
// allocator load values again.
func TestSelfHostSelectJoinsFoldIntoHead(t *testing.T) {
	cli := buildSelfHostCLI(t)
	label := regexp.MustCompile(`(?m)^\.Lssa_picked_\d+:$`)
	for name, form := range map[string]string{"let": selectLetForm, "assign": selectAssignForm} {
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(form+selectMain), 0o644); err != nil {
			t.Fatal(err)
		}
		asm, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
		if err != nil {
			t.Fatal(err)
		}
		body := asmFunction(string(asm), "__fn_picked:")
		if body == "" {
			t.Fatalf("%s form: no __fn_picked in the emitted assembly", name)
		}
		if n := len(label.FindAllString(body, -1)); n != 1 {
			t.Errorf("%s form: picked has %d blocks, want 1:\n%s", name, n, body)
		}
		out, err := runX86_64Bin(cli.runner, cli.x86Binary(t, src)).CombinedOutput()
		if err != nil || string(out) != "1011\n" {
			t.Errorf("%s form: run: %v: got %q, want %q", name, err, out, "1011\n")
		}
	}
}
