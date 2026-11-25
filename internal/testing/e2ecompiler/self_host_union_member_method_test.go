package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Three types share the method name `label`. A struct with no `label` of its
// own takes the method of the union that holds it, also through a local.
const unionMemberMethodProg = `struct Cat { lives: i32 }
struct Dog { age: i32 }
type Pet = Cat | Dog;

function (p: Pet) label(): string {
  match (p) {
    Cat(c) => { return "cat " + c.lives.to_string(); },
    Dog(d) => { return "dog " + d.age.to_string(); }
  }
}

function (p: Pet) years(): i32 {
  match (p) {
    Cat(c) => { return c.lives; },
    Dog(d) => { return d.age; }
  }
}

struct Tag { n: i32 }

function (t: Tag) label(): string {
  return "tag " + t.n.to_string();
}

function (xs: i32[]) label(): string {
  return "array " + xs.len().to_string();
}

function main(): i32 {
  let c = Cat { lives: 9 };
  let p: Pet = Dog { age: 4 };
  let xs: i32[] = [1, 2, 3];
  let lines = [c.label(), Dog { age: 3 }.label(), p.label(), Tag { n: 2 }.label(), xs.label()];
  for line in lines {
    print(line);
  }
  if (lines[0] != "cat 9" || lines[1] != "dog 3" || lines[2] != "dog 4") {
    return 1;
  }
  if (lines[3] != "tag 2" || lines[4] != "array 3") {
    return 2;
  }
  if (c.years() != 9) {
    return 3;
  }
  return 0;
}
`

const unionMemberMethodWant = "cat 9\ndog 3\ndog 4\ntag 2\narray 3\n"

func TestSelfHostUnionMemberMethod(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(unionMemberMethodProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("interp: %v\n%s", err, out)
	}
	if string(out) != unionMemberMethodWant {
		t.Fatalf("interp output:\n%s\nwant:\n%s", out, unionMemberMethodWant)
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
