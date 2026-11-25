package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The union `Pet` and its member `Dog` both declare `label`. A receiver whose
// static type is `Pet` runs the method of `Pet`, also when a generic method
// gives that type. `Box` and `Boxes` share a method name and a name prefix,
// so the result type of `get` must come from the right receiver.
const unionStaticMethodProg = `struct Cat { lives: i32 }
struct Dog { age: i32 }
type Pet = Cat | Dog;

function (p: Pet) label(): string {
  return "pet";
}

function (d: Dog) label(): string {
  return "dog";
}

struct Box[T] { v: T }
struct Boxes[T] { vs: T[] }

function (b: Box[T]) get(): T {
  return b.v;
}

function (b: Boxes[T]) get(): T {
  return b.vs[0];
}

function main(): i32 {
  let p: Pet = Dog { age: 4 };
  let b = Box[Pet] { v: p };
  let bs: Boxes[Pet] = Boxes[Pet] { vs: [p] };
  let lines = [p.label(), b.get().label(), bs.get().label()];
  for line in lines {
    print(line);
  }
  if (lines[0] != "pet" || lines[1] != "pet" || lines[2] != "pet") {
    return 1;
  }
  return 0;
}
`

const unionStaticMethodWant = "pet\npet\npet\n"

func TestSelfHostUnionStaticMethod(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(unionStaticMethodProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	if string(out) != unionStaticMethodWant {
		t.Fatalf("interp output:\n%s\nwant:\n%s", out, unionStaticMethodWant)
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, src, target, nil)
			if code != 0 {
				t.Fatalf("exit %d\n%s", code, stderr)
			}
		})
	}
}
