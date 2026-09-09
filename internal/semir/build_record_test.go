package semir

import (
	"fmt"
	"strings"
	"testing"
)

var recordValueCases = []struct{ name, source, want string }{
	{"record-nested-shared-lifetime", `struct Inner { tag: i32, values: i32[], flag: boolean }
struct Outer { left: Inner, right: Inner }
function pilot(): string {
  var child = Inner { tag: 7i32, values: [41i32], flag: true };
  var pair = Outer { left: child, right: child };
  var saved = pair.right.values;
  var next = Outer { ...pair, left: Inner { tag: 1i32, values: [9i32], flag: false } };
  if (next.right.tag != 7i32 || !next.right.flag) { fail(); }
  pair = Outer { left: next.left, right: next.left }; child = next.left;
  next = pair;
  var i = 0i32; while (i < 32i32) { churn(); i = i + 1i32; }
  if (saved[0] != 41i32) { fail(); }
  return "nested";
}
function churn(): void { var inner = Inner { tag: 0i32, values: [0i32], flag: false }; var pair = Outer { left: inner, right: inner }; }
function fail(): void { var bad: i32[] = []; var value = bad[0]; }`, "nested\n"},
	{"record-own-borrow-call", `struct Box { value: i32[] }
function pilot(): string {
  var saved = produce(Box { value: [41i32] });
  var i = 0i32; while (i < 32i32) { churn(); i = i + 1i32; }
  check(saved[0], 41i32); return "calls";
}
function produce(own box: Box): i32[] { return take(box, box); }
function take(reader: Box, own taken: Box): i32[] { check(reader.value[0], 41i32); return taken.value; }
function churn(): void { var box = Box { value: [9i32] }; }
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "calls\n"},
	{"record-update-base-snapshot-order", `struct R { a: i32[], b: i32[], c: i32[] }
function pilot(): string {
  var seen = 0i32; var base = R { a: [1i32], b: [2i32], c: [3i32] };
  var next = R { ...{ seen = seen * 10i32 + 1i32; base },
    b: { seen = seen * 10i32 + 2i32; base = R { a: [9i32], b: [9i32], c: [9i32] }; [22i32] },
    a: { seen = seen * 10i32 + 3i32; [11i32] } };
  check(seen, 123i32); check(next.a[0], 11i32); check(next.b[0], 22i32);
  check(next.c[0], 3i32); check(base.c[0], 9i32); return "base snapshot";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "base snapshot\n"},
	{"record-join-loop-saved-child", `struct Box { data: string[] }
function pilot(): string {
  var box = Box { data: ["saved"] }; var saved = box.data;
  var i = 0i32; while (i < 32i32) {
    box = if (i < 16i32) { Box { data: ["left"] } } else { Box { data: ["right"] } };
    i = i + 1i32;
  }
  return saved[0];
}`, "saved\n"},
	{"record-empty-and-duplicate-children", `struct Empty {} struct Pair { a: string[], b: string[] }
function pilot(): string {
  var items = ["twice"]; var pair = Pair { a: items, b: items };
  consume(Empty {}); consume(Empty {}); return read(pair);
}
function consume(own empty: Empty): void {}
function read(pair: Pair): string { return pair.b[0]; }`, "twice\n"},
	{"record-projection", `struct Box { value: string[] }
function pilot(): string { var box = Box { value: ["record"] }; return box.value[0]; }`, "record\n"},
	{"record-update-shared-child", `struct Pair { left: string[], right: string[] }
function pilot(): string {
  var seed = Pair { left: ["shared"], right: ["old"] };
  var next = Pair { ...seed, right: ["new"] };
  var saved = next.left; seed = Pair { left: ["discard"], right: ["discard"] };
  next = Pair { left: ["discard"], right: ["discard"] };
  var i = 0i32; while (i < 32i32) { churn(); i = i + 1i32; }
  return saved[0];
}
function churn(): void { var box = Pair { left: ["churn"], right: ["churn"] }; }`, "shared\n"},
	{"record-source-order", `struct Order { a: i32, b: i32 }
function pilot(): string {
  var seen = 0i32;
  var record = Order { b: { seen = seen * 10i32 + 1i32; 11i32 }, a: { seen = seen * 10i32 + 2i32; 22i32 } };
  check(seen, 12i32); check(record.a, 22i32); check(record.b, 11i32); return "order";
}
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var v = bad[0]; } }`, "order\n"},
	{"record-conditional-cleanup", `struct Box { values: string[] }
function pilot(): string { return work(true); }
function work(flag: boolean): string {
  var box = Box { values: ["snapshot"] };
  if (flag) { defer box = Box { values: ["replacement"] }; }
  return box.values[0];
}`, "snapshot\n"},
}

func TestRecordValuesBuild(t *testing.T) {
	for _, tc := range recordValueCases {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			p, err := BuildProgram(prog, info)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := solveReturnFlow(p); err != nil {
				t.Fatal(err)
			}
			if _, err := LowerARM64SSA(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func BenchmarkRecordValuesBuild(b *testing.B) {
	for _, count := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("fields-%d", count), func(b *testing.B) {
			var source strings.Builder
			source.WriteString("struct R {")
			for i := range count {
				fmt.Fprintf(&source, "field%d: i32[],", i)
			}
			source.WriteString("} function pilot(): i32 { var base = R {")
			for i := range count {
				fmt.Fprintf(&source, "field%d: [1i32],", i)
			}
			source.WriteString("}; var next = R { ...base, field0: [2i32] }; return next.field0[0]; }")
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
