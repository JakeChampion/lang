package semir

import (
	"fmt"
	"strings"
	"testing"
)

var recursiveRecordCases = []struct{ name, source, want string }{
	{"recursive-record-call-paths", `struct Node { data: i32[], left: Node[], right: Node[] }
function pilot(): string {
  var target = leaf(41i32);
  var left2 = Node { data: [2i32], left: [], right: [Node { data: [3i32], left: [], right: [target] }] };
  var root = Node { data: [0i32], left: [Node { data: [1i32], left: [left2], right: [] }], right: [] };
  var saved = pick(root, 2i32);
  root = leaf(9i32); left2 = root; target = root;
  var i = 0i32; while (i < 32i32) { churn(); i = i + 1i32; }
  check(saved.data[0], 41i32); return "recursive calls";
}
function pick(root: Node, n: i32): Node {
  if (n == 0i32) { return root; }
  return pick(root.left[0], n - 1i32).right[0];
}
function leaf(value: i32): Node { return Node { data: [value], left: [], right: [] }; }
function churn(): void { var node = leaf(7i32); }
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var value = bad[0]; } }`, "recursive calls\n"},
	{"recursive-record-escaped-child", `struct Node { data: i32[], children: Node[] }
function pilot(): string {
  var root = Node { data: [1i32], children: [Node { data: [41i32], children: [] }] };
  var saved = root.children[0].data;
  root = Node { data: [9i32], children: [] };
  var i = 0i32; while (i < 32i32) { churn(); i = i + 1i32; }
  check(saved[0], 41i32); return "recursive child";
}
function churn(): void { var root = Node { data: [7i32], children: [Node { data: [8i32], children: [] }] }; }
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var value = bad[0]; } }`, "recursive child\n"},
	{"recursive-record-loop-shared-update", `struct Node { data: i32[], children: Node[] }
function pilot(): string {
  var tree = Node { data: [-1i32], children: [] };
  var i = 0i32;
  while (i < 64i32) { tree = Node { data: [i], children: [tree] }; i = i + 1i32; }
  var other = Node { ...tree, data: [99i32] };
  var saved = other.children[0].data;
  tree = Node { data: [0i32], children: [] }; other = tree;
  i = 0i32; while (i < 32i32) { churn(); i = i + 1i32; }
  check(saved[0], 62i32); return "recursive loop";
}
function churn(): void { var root = Node { data: [7i32], children: [] }; }
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var value = bad[0]; } }`, "recursive loop\n"},
	{"recursive-record-mutual-call-cleanup", `struct A { data: i32[], children: B[] }
struct B { children: A[] }
function pilot(): string {
  var saved = work(true);
  var i = 0i32; while (i < 32i32) { churn(); i = i + 1i32; }
  check(saved[0], 41i32); return "mutual";
}
function work(flag: boolean): i32[] {
  var root = A { data: [0i32], children: [B { children: [A { data: [41i32], children: [] }] }] };
  if (flag) { defer root = A { data: [9i32], children: [] }; }
  return take(root, A { ...root });
}
function take(reader: A, own root: A): i32[] { check(reader.data[0], 0i32); return root.children[0].children[0].data; }
function churn(): void { var root = A { data: [7i32], children: [B { children: [] }] }; }
function check(a: i32, b: i32): void { if (a != b) { var bad: i32[] = []; var value = bad[0]; } }`, "mutual\n"},
}

func TestRecursiveRecordValuesBuild(t *testing.T) {
	for _, tc := range recursiveRecordCases {
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

func BenchmarkRecursiveRecordBuild(b *testing.B) {
	for _, count := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("types-%d", count), func(b *testing.B) {
			var source strings.Builder
			for i := range count {
				fmt.Fprintf(&source, "struct Level%d { children: Level%d[], data: i32[] }", i, (i+1)%count)
			}
			source.WriteString("function pilot(own root: Level0): void {}")
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
