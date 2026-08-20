package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostImplBounds pins the trait bounds an impl block contributes to the
// methods parsed out of it (#7224).
//
// `bound_traits` runs PARALLEL to `type_params` — parser.fern states it on the
// field. finalize_impl_method merges an impl block's bounded type params into
// every method it desugars, and used to write `bound_traits: []` while doing so,
// which dropped BOTH halves: the impl's bound on the merged param, and the
// method's own bound on the params it already had. Every type param of an
// `impl[T: Ord] Box[T]` method then read back unbounded, and the checker's E021
// conformance walk reads exactly that field (checker.fern's with_tparams).
//
// The loss was silent rather than a crash because tp_bound_for guards its read
// (`if (k < bound_traits.len())`), so a short list answers "unbounded" for every
// index past its end. That is what the driver renders as <missing>.
//
// The corpus is built so each row fails for a distinct reason:
//
//	Box[T].make   the associated-function arm (no `self` to peel)
//	Box[T].rank   the receiver arm
//	Box[T].tag    a method with its OWN [U: Show] on top of the impl's [T: Ord].
//	              Its own bounds must be padded out to its own type_params
//	              before the impl's are appended, or the impl's bound lands on
//	              the method's parameter — a wrong bound instead of a missing one.
//	Box[T].hello  a trait DEFAULT method, synthesised onto the impl by
//	              parse_module rather than written in the block. It reaches
//	              finalize_impl_method through ImplInfo, which is why the bounds
//	              have to ride on that struct and not just on the local.
//	Box[T].show   a trait impl rather than an inherent one
//
// The unbounded rows are the controls: free_plain and Plain.* have no type
// params at all, and free_bounded keeps its bound without going through
// finalize_impl_method, so a driver that simply printed <missing> everywhere
// would not reproduce them.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the map.
func TestSelfHostImplBounds(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("impl_bounds_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "impl_bounds_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "impl_bounds_run.fern", "impl_bounds_run")

	const src = `trait Ord { function cmp(self: Self, other: Self): i32; }
trait Show { function show(self: Self): string; }

trait Greet {
    function name(self: Self): string;
    function hello(self: Self): string { return "hi " + self.name(); }
}

struct Box[T] { v: T }
struct Plain { n: i32 }

impl[T: Ord] Box[T] {
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

impl Plain {
    function bump(self: Self): i32 { return self.n + 1; }
}

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
