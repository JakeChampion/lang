package semir

import (
	"fmt"
	"strings"
	"testing"
)

var conditionalCleanupCases = []struct{ name, source, want string }{
	{"conditional-cleanup-exact-traces", conditionalCleanupTraceSource(), "traces\n"},
	{"conditional-cleanup-body-flow-and-pressure", `function pilot(): string {
  var i = 0i32;
  while (i < 32i32) { work(i < 16i32); i = i + 1i32; }
  return "pressure";
}
function work(flag: boolean): void {
  var items = [[17i32]]; var saved = items[0];
  defer check(items[0][0], saved[0], flag);
  if (flag) { defer {
    var j = 0i32;
    while (j < 8i32) { items = [[23i32]]; j = j + 1i32; }
    if (flag) { items = [[31i32]] } else { items = [[43i32]] }
  } }
  items = [[29i32]];
}
function check(now: i32, saved: i32, flag: boolean): void {
  if (saved != 17i32 || (flag && now != 31i32) || (!flag && now != 29i32)) {
    var bad: i32[] = []; var v = bad[0];
  }
}`, "pressure\n"},
	{"conditional-cleanup-taken-and-skipped", `function pilot(): string { work(true); work(false); return "both"; }
function work(flag: boolean): void {
  var seen = 0i32; defer check(seen, flag);
  if (flag) { defer seen = 9i32; }
}

function check(seen: i32, flag: boolean): void {
  if ((flag && seen != 9i32) || (!flag && seen != 0i32)) { var bad: i32[] = []; var v = bad[0]; }
}`, "both\n"},
	{"conditional-cleanup-late-array", `function pilot(): string { work(true); work(false); return "late"; }
function work(flag: boolean): void {
  var items: i32[] = [1]; defer check(items[0], flag);
  if (flag) { defer items = [items[0] + 6i32]; } items = [3];
}
function check(n: i32, flag: boolean): void {
  if ((flag && n != 9i32) || (!flag && n != 3i32)) { var bad: i32[] = []; var v = bad[0]; }
}`, "late\n"},
	{"conditional-cleanup-branch-local", `function pilot(): string { work(true); work(false); return "local"; }
function work(flag: boolean): void { if (flag) { var items = ["local"]; defer sink([items]); } }
function sink(own items: string[][]): void {}`, "local\n"},
	{"conditional-cleanup-empty-capture-skip", `function pilot(): string { work(false); return "skipped"; }
function work(flag: boolean): void { if (flag) { defer fault(); } }
function fault(): void { var bad: i32[] = []; var v = bad[0]; }`, "skipped\n"},
	{"conditional-cleanup-saved-return", `function pilot(): string { return work(true); }
function work(flag: boolean): string {
  var items = ["saved"]; if (flag) { defer items = ["replaced"]; } return items[0];
}`, "saved\n"},
	{"conditional-cleanup-iteration-reset", `function pilot(): string {
  var seen = 0i32; var i = 0i32;
  while (i < 4i32) { if (i == 0i32 || i == 2i32) { defer seen = seen * 10i32 + i; } i = i + 1i32; }
  check(seen, 13i32); return "epochs";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "epochs\n"},
	{"conditional-cleanup-labelled-continue", `function pilot(): string {
  var seen = 0i32; var i = 0i32;
  outer: while (i < 2i32) {
    i = i + 1i32; if (i == 1i32) { defer seen = seen * 10i32 + 1i32; }
    loop { if (i == 2i32) { defer seen = seen * 10i32 + 2i32; } continue outer; }
  }
  check(seen, 12i32); return "continue";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "continue\n"},
	{"conditional-cleanup-labelled-break", `function pilot(): string { work(true); work(false); return "break"; }
function work(flag: boolean): void {
  var seen = 0i32; defer check(seen, flag);
  outer: loop { if (flag) { defer seen = seen * 10i32 + 1i32; }
    loop { defer seen = seen * 10i32 + 2i32; break outer; }
  }
}
function check(n: i32, flag: boolean): void {
  if ((flag && n != 21i32) || (!flag && n != 2i32)) { var bad: i32[] = []; var v = bad[0]; }
}`, "break\n"},
	{"conditional-cleanup-exclusive-order", `function pilot(): string { work(true); work(false); return "order"; }
function work(flag: boolean): void {
  var seen = 0i32; defer check(seen, flag);
  if (flag) { defer seen = seen * 10i32 + 1i32; } else { defer seen = seen * 10i32 + 2i32; }
  defer seen = seen * 10i32 + 3i32;
}
function check(n: i32, flag: boolean): void {
  if ((flag && n != 31i32) || (!flag && n != 32i32)) { var bad: i32[] = []; var v = bad[0]; }
}`, "order\n"},
}

// Enumerate every subset of four registrations. The expected trace comes from
// a plain host-language stack, independently of the compiler's projected-state
// proof and the interpreter's defer implementation. The first registered action
// checks the trace after all later actions, so saved return values cannot mask
// a missing, duplicated or out-of-order replay.
func conditionalCleanupTraceSource() string {
	var source strings.Builder
	source.WriteString(`function pilot(): string {`)
	for mask := 0; mask < 16; mask++ {
		var pending []int
		for action := 0; action < 4; action++ {
			if mask&(1<<action) != 0 {
				pending = append(pending, action+1)
			}
		}
		trace := 0
		for i := len(pending) - 1; i >= 0; i-- {
			trace = trace*10 + pending[i]
		}
		fmt.Fprintf(&source, "work(%t, %t, %t, %t, %di32);", mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0, trace)
	}
	source.WriteString(`return "traces"; }
function work(a: boolean, b: boolean, c: boolean, d: boolean, expected: i32): void {
  var trace = 0i32; defer check(trace, expected);
  if (a) { defer trace = trace * 10i32 + 1i32; }
  if (b) { defer trace = trace * 10i32 + 2i32; }
  if (c) { defer trace = trace * 10i32 + 3i32; }
  if (d) { defer trace = trace * 10i32 + 4i32; }
}
function check(actual: i32, expected: i32): void {
  if (actual != expected) { var bad: i32[] = []; var v = bad[0]; }
}`)
	return source.String()
}

func TestGuardedCleanupSourceBuilds(t *testing.T) {
	for _, tc := range conditionalCleanupCases {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			p, err := BuildProgram(prog, info)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := LowerARM64SSA(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestARM64TypedGuardedCleanupKeepsObservableEffects(t *testing.T) {
	armLauncher(t)
	for _, optimize := range []bool{false, true} {
		out := lowerCheckedARM64(t, `function pilot(): string { work(true); return "missing action"; }
function work(flag: boolean): void { if (flag) { defer fault(); } }
function fault(): void { var bad: i32[] = []; var v = bad[0]; }`)
		stdout, stderr, code := runARM64Pilot(t, armExecutable(t, out, printHarness(out), optimize))
		if code != 134 || stdout != "" {
			t.Fatalf("optimized=%v: exit %d stdout %q stderr %q; expected bounds abort", optimize, code, stdout, stderr)
		}
	}
}

func BenchmarkGuardedCleanupBuild(b *testing.B) {
	for _, count := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("actions-%d", count), func(b *testing.B) {
			var source strings.Builder
			source.WriteString(`function pilot(flag: boolean): void {`)
			for range count {
				source.WriteString(`if (flag) { var items: i32[] = [1]; defer items = [2]; }`)
			}
			source.WriteString(`}`)
			prog, info := checkedProgram(b, source.String())
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				p, err := BuildProgram(prog, info)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := LowerARM64SSA(p); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
