package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostImplBounds(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("impl_bounds_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/impl_bounds_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/impl_bounds_run.fern", "impl_bounds_run")

	const src = `trait Ord { function cmp(self: Self, other: Self): i32; }
trait Show { function show(self: Self): string; }

trait Greet {
    function name(self: Self): string;
    function hello(self: Self): string { return "hi " + self.name(); }
}

struct Box[T] { v: T }
struct Plain { n: i32 }

impl[T: Ord] Boxy for Box[T] {
    function rank(self: Self, o: T): i32 { return 0; }
    function make(v: T): Box[T] { return Box { v: v }; }
    function [U: Show] tag(self: Self, u: U): string { return "t"; }
}

impl[T: Ord] Show for Box[T] {
    function show(self: Self): string { return "b"; }
}

impl Greet for Plain {
    function name(self: Self): string { return "p"; }
}

impl[T: Ord] Greet for Box[T] {
    function name(self: Self): string { return "b"; }
}

function (self: Plain) bump(): i32 { return self.n + 1; }

function free_bounded[U: Show](x: U): i32 { return 0; }
function free_plain(x: i32): i32 { return x; }
`

	const want = "Box[T].rank\tT=Ord\n" +
		"Box[T].make\tT=Ord\n" +
		"Box[T].tag\tU=Show,T=Ord\n" +
		"Box[T].show\tT=Ord\n" +
		"Plain.name\t<none>\n" +
		"Box[T].name\tT=Ord\n" +
		"Plain.bump\t<none>\n" +
		"free_bounded\tU=Show\n" +
		"free_plain\t<none>\n" +
		"Plain.hello\t<none>\n" +
		"Box[T].hello\tT=Ord\n"

	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(src)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("impl_bounds_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("impl-block bounds mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
