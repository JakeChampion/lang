package semir

import (
	"fmt"
	"strings"
	"testing"
)

var iterationCleanupCases = []struct{ name, source, want string }{
	{"iteration-cleanup-tail", `function pilot(): string {
  var seen = 0i32; var i = 0i32;
  while (i < 3i32) { var items = [i]; defer seen = seen * 3i32 + items[0]; i = i + 1i32; }
  check(seen, 5i32); return "tail";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "tail\n"},
	{"iteration-cleanup-break", `function pilot(): string {
  var seen = 0i32;
  loop { var items = [2i32]; defer seen = items[0]; items = [9i32]; break; }
  check(seen, 9i32); return "break";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "break\n"},
	{"iteration-cleanup-continue", `function pilot(): string {
  var seen = 0i32; var i = 0i32;
  while (i < 3i32) { i = i + 1i32; defer seen = seen * 3i32 + i; continue; }
  check(seen, 18i32); return "continue";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "continue\n"},
	{"iteration-cleanup-labelled-continue", `function pilot(): string {
  var seen = 0i32; var i = 0i32;
  outer: while (i < 2i32) {
    i = i + 1i32; defer seen = seen * 10i32 + 1i32;
    loop { defer seen = seen * 10i32 + 2i32; continue outer; }
  }
  check(seen, 2121i32); return "labelled continue";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "labelled continue\n"},
	{"iteration-cleanup-labelled-break", `function pilot(): string {
  var seen = 0i32; defer check(seen, 21i32);
  outer: loop { defer seen = seen * 10i32 + 1i32;
    loop { defer seen = seen * 10i32 + 2i32; break outer; }
  }
  check(seen, 21i32); return "labelled break";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "labelled break\n"},
	{"iteration-cleanup-return-projection", `function pilot(): string {
  var items = ["saved"];
  loop { defer items = ["replacement"]; return items[0]; }
}`, "saved\n"},
	{"iteration-cleanup-return-before-register", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  loop { if (flag) { return "early"; } defer fault(); break; }
  return "late";
}
function fault(): void { var bad: i32[] = []; var v = bad[0]; }`, "early\n"},
	{"iteration-cleanup-early-continue", `function pilot(): string {
  var seen = 0i32; var i = 0i32;
  while (i < 3i32) {
    i = i + 1i32; if (i == 1i32) { continue; }
    defer seen = seen + i; if (i == 3i32) { break; }
  }
  check(seen, 5i32); return "early continue";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "early continue\n"},
	{"iteration-cleanup-value-block", `function pilot(): string {
  var seen = 0i32; var before = 0i32;
  loop {
    var yielded = { var items = [2i32]; defer seen = items[0]; items = [9i32]; 1i32 };
    before = seen + yielded; break;
  }
  check(before * 10i32 + seen, 19i32); return "value block";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "value block\n"},
	{"iteration-cleanup-action-write", `function pilot(): string {
  var seen = 0i32;
  loop { var items = [2i32]; defer seen = items[0]; defer items = [9i32]; break; }
  check(seen, 9i32); return "action write";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "action write\n"},
	{"iteration-cleanup-condition-outer-exit", `function pilot(): string {
  var seen = 0i32;
  outer: loop { defer seen = 7i32; while ({ break outer; false }) {} }
  check(seen, 7i32); return "condition exit";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "condition exit\n"},
}

func TestBuildIterationCleanup(t *testing.T) {
	for _, tc := range iterationCleanupCases {
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

func BenchmarkTypedIterationCleanup(b *testing.B) {
	for _, count := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("loops-%d", count), func(b *testing.B) {
			var source strings.Builder
			source.WriteString(`function pilot(items: string[][]): string[][] { var result = items;`)
			for i := 0; i < count; i++ {
				source.WriteString(`loop { defer result = result.append(["cleanup"]); break; }`)
			}
			source.WriteString(`return result; }`)
			prog, info := checkedProgram(b, source.String())
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
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
