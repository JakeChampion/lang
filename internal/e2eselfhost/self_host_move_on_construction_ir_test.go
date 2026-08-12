package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostMoveOnConstructionIRX86_64 ports the structural half of native's
// internal/ir/move_on_construction_test.go (#4365) — the half the self-host has
// never had.
//
// Move-on-construction is the rule that `var s = Wrap { inner: x }`, where `x`
// is an owned rc local at its last use, MOVES x into the field: the field-init
// inc and x's exit-sweep dec cancel, so neither is emitted. The self-host's
// existing RC suites assert exit codes and `__rc_underflow_count()`, and an
// un-elided inc/dec PAIR satisfies both perfectly — it is correct at runtime,
// just wasted. Only a structural assertion can see it, which is exactly what
// #4365 says is missing.
//
// The port is measured at TWO layers, because the self-host splits the rule in
// a way native does not:
//
//   - `moved` is the analysis (`-rc-plan`'s movedLocals — irlower's
//     computeMovedLocals port). It decides which locals move.
//   - `incs` is what the emitter actually wrote (`-dump-fn`).
//
// Pinning both is what makes this test say something. The analysis agrees with
// native on every shape below — it marks the local moved exactly where native
// elides the inc, and declines exactly where native keeps it. The emitter does
// not read that verdict at a construction site, so `incs` is 1 whether the
// local moved or not. See the header on the incs column below.
//
// Native's closure-capture case has no row: `lower_module` does not run
// lift_lambdas, so a function containing a nested one does not lower through
// this driver at all and there is nothing to count. A row there would pin a
// property of the driver rather than of the compiler. Closure-capture moves
// are scoping cut (b) of the movedLocals port either way, declined
// deliberately and documented at irlower.fern's computeMovedLocals header.
func TestSelfHostMoveOnConstructionIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("irlower_run reads its program on stdin and runs natively")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")

	for _, c := range []struct {
		name string
		src  string
		// moved is whether the analysis marks the local as moved — the
		// self-host's answer to the question native answers by eliding.
		// It matches native's elision decision on every row.
		moved string
		// incs is how many `__fern_rc_inc` calls the emitter wrote into `f`.
		//
		// Every construction row is 1, INCLUDING the moved ones, where native
		// emits 0: the emitter does not consume the move verdict at a
		// construction site, so the inc it cancels against is still written
		// (and so is the exit-sweep dec, which is why the program is correct
		// and no value-or-underflow test could see this). Filed as #6726;
		// when it is fixed these rows go to 0 and this test says so.
		incs int
	}{
		// --- the analysis marks these moved, native emits no inc ----------
		{
			name: "alias-last-use",
			src: `function f(): i32 {
    var x: i32[] = [1, 2, 3];
    var b: i32[] = x;
    return b[0];
}
function main(): i32 { return f(); }`,
			moved: "x", incs: 1,
		},
		{
			name: "struct-field-last-use",
			src: `struct Wrap { inner: i32[] }
function f(): i32 {
    var x: i32[] = [1, 2, 3];
    var s: Wrap = Wrap { inner: x };
    return s.inner[0];
}
function main(): i32 { return f(); }`,
			moved: "x", incs: 1,
		},
		{
			name: "tuple-element",
			src: `function f(): i32 {
    var x: i32[] = [1, 2, 3];
    var t: (i32[], i32) = (x, 9);
    return t.0[0] + t.1;
}
function main(): i32 { return f(); }`,
			moved: "x", incs: 1,
		},
		{
			name: "array-element",
			src: `function f(): i32 {
    var x: i32[] = [1, 2, 3];
    var xs: i32[][] = [x];
    return xs[0][0];
}
function main(): i32 { return f(); }`,
			moved: "x", incs: 1,
		},
		{
			// Composes with move-on-return: x moves into s, s moves out to
			// the caller. Native carries zero rc traffic here.
			name: "composes-with-return",
			src: `struct Wrap { inner: i32[] }
function f(): Wrap {
    var x: i32[] = [1, 2, 3];
    var s: Wrap = Wrap { inner: x };
    return s;
}
function main(): i32 { return f().inner[0]; }`,
			moved: "x", incs: 1,
		},
		{
			// Move-on-destructure: the tuple box alias. The self-host emits
			// no box-alias inc for a destructure in the first place, so the
			// row pins the analysis and a already-zero emitter.
			name: "destructure-last-use",
			src: `function f(): i32 {
    var t: (i32[], i32) = ([1, 2, 3], 9);
    var (a, b) = t;
    return a[0] + b;
}
function main(): i32 { return f(); }`,
			moved: "t", incs: 0,
		},

		// --- the analysis declines these, native keeps the inc ------------
		{
			// Read again after the construction, so not at its last use.
			name: "struct-field-read-again",
			src: `struct Wrap { inner: i32[] }
function f(): i32 {
    var x: i32[] = [1, 2, 3];
    var s: Wrap = Wrap { inner: x };
    return s.inner[0] + x[1];
}
function main(): i32 { return f(); }`,
			moved: "", incs: 1,
		},
		{
			// Nested in a branch, so the construction does not dominate the
			// exit — the same guard the move-on-alias rule uses.
			name: "branched-construction",
			src: `struct Wrap { inner: i32[] }
function f(c: boolean): i32 {
    var x: i32[] = [1, 2, 3];
    if (c) {
        var s: Wrap = Wrap { inner: x };
        return s.inner[0];
    }
    return x[0];
}
function main(): i32 { return f(true); }`,
			moved: "", incs: 1,
		},
		{
			name: "destructure-read-again",
			src: `function f(): i32 {
    var t: (i32[], i32) = ([1, 2, 3], 9);
    var (a, b) = t;
    return a[0] + b + t.1;
}
function main(): i32 { return f(); }`,
			moved: "", incs: 0,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			plan := runIRLower(t, bin, "-rc-plan", c.src)
			if got := movedLocalsOf(plan, "f"); got != c.moved {
				t.Errorf("movedLocals for f = %q, want %q\n"+
					"This is the analysis half — which locals irlower decides are moved. "+
					"It agrees with native's elision decision on every shape in this table, "+
					"so a change here is the port drifting from native's rule.\n%s", got, c.moved, plan)
			}

			ops := runIRLower(t, bin, "-dump-fn", c.src, "f")
			if got := strings.Count(ops, "__fern_rc_inc"); got != c.incs {
				t.Errorf("__fern_rc_inc count in f = %d, want %d\n"+
					"A moved local's construction inc should not be emitted at all; a count that "+
					"MOVED (either way) means the emitter's relationship to the move verdict changed.\n%s",
					got, c.incs, ops)
			}
		})
	}
}

// runIRLower runs the driver over src with the given mode, returning stdout.
//
// The exit code is NOT a status here: `-dump-fn` returns the op count and the
// evaluating default returns the program's value, so the driver's contract is
// its stdout. An empty one is the failure — a bailed lowering or a function
// name that does not resolve prints nothing.
func runIRLower(t *testing.T, bin, mode, src string, extra ...string) string {
	t.Helper()
	args := append([]string{mode}, extra...)
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(src)
	out, _ := cmd.Output()
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Fatalf("irlower_run %v printed nothing — the module did not lower, or `f` did not resolve", args)
	}
	return string(out)
}

// movedLocalsOf reads the `movedLocals:` line out of the `-rc-plan` section for
// fn, or "" when the function has none. The dump is one `== <name>` header per
// function, then that function's non-empty table lines.
func movedLocalsOf(plan, fn string) string {
	inFn := false
	for _, line := range strings.Split(plan, "\n") {
		if strings.HasPrefix(line, "== ") {
			inFn = strings.TrimSpace(strings.TrimPrefix(line, "== ")) == fn
			continue
		}
		if inFn && strings.HasPrefix(line, "movedLocals:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "movedLocals:"))
		}
	}
	return ""
}
