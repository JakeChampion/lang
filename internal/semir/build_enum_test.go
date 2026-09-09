package semir

import (
	"fmt"
	"strings"
	"testing"
)

var enumValueCases = []struct{ name, source, want string }{
	{"enum-option-projection", `function pilot(): string { var x: Option[string[]] = Some(["enum"]); return read(x); }
function read(x: Option[string[]]): string { return match (x) { None => "absent", Some(values) => values[0] }; }`, "enum\n"},
	{"enum-nullary", `function pilot(): string { var x: Option[string[]] = Option.None; return read(x); }
function read(x: Option[string[]]): string { return match (x) { Some(values) => values[0], None => "empty" }; }`, "empty\n"},
	{"enum-whole-value-wildcard", `function pilot(): string {
  var x: Option[string[]] = Some(["whole"]);
  return match (x) { whole @ Some(_) when false => "wrong", whole @ Some(_) => read(whole), _ => "wrong" };
}
function read(x: Option[string[]]): string { return match (x) { None => "wrong", Some(a) => a[0] }; }`, "whole\n"},
	{"enum-active-layout", `enum Mixed { Small(i32), Wide(i64, string[]), Pair(string[], i32), Empty }
function pilot(): string {
  var x = Wide(9000000000i64, ["wide"]); var y = Pair(["pair"], 7i32);
  var discarded = read(y); return read(x);
}
function read(x: Mixed): string { return match (x) { Small(n) => "small", Wide(n, a) => a[0], Pair(a, n) => a[0], Empty => "empty" }; }`, "wide\n"},
	{"enum-guarded-saved-child", `function pilot(): string {
  var x: Result[string[], string[]] = Ok(["saved"]);
  var saved = consume(Ok(["saved"])); x = Err(["other"]);
  var i = 0i32; while (i < 32i32) { churn(); i = i + 1i32; }
  return saved[0];
}
function consume(own x: Result[string[], string[]]): string[] { return read(x, x); }
function read(reader: Result[string[], string[]], own taken: Result[string[], string[]]): string[] {
  var seed = match (reader) { Ok(a) when false => a, Err(a) => a, Ok(a) => a };
  return match (taken) { Err(a) => a, Ok(a) => a };
}
function churn(): void { var a: Result[string[], string[]] = Ok(["churn"]); }`, "saved\n"},
	{"enum-finite-permutation", `enum Flip[T, U] { Leaf(T), Next(Flip[U, T]) }
function pilot(): string {
  var inner: Flip[string, i32] = Leaf("flip");
  var outer: Flip[i32, string] = Next(inner);
  return match (outer) { Leaf(n) => "wrong", Next(other) => match (other) { Leaf(s) => s, Next(rest) => "wrong" } };
}`, "flip\n"},
	{"enum-record-recursion", `struct Node { value: string[], next: Link }
enum Link { Stop, Continue(Node) }
function pilot(): string {
  var tail = Node { value: ["tail"], next: Stop };
  var head = Node { value: ["head"], next: Continue(tail) };
  var saved = child(Node { ...head }); head = tail;
  var i = 0i32; while (i < 32i32) { churn(); i = i + 1i32; }
  return saved[0];
}
function child(own node: Node): string[] { return match (node.next) { Stop => node.value, Continue(next) => next.value }; }
function churn(): void { var node = Node { value: ["churn"], next: Stop }; }`, "tail\n"},
	{"enum-loop-conditional-cleanup", `function pilot(): string {
  var saved = produce(true); var other = produce(false);
  var i = 0i32; while (i < 32i32) { churn(i < 16i32); i = i + 1i32; }
  return saved[0];
}
function produce(flag: boolean): string[] {
  var item: Result[string[], string[]] = Ok(["snapshot"]);
  if (flag) { defer item = Err(["replacement"]); }
  return match (item) { Ok(a) => a, Err(b) => b };
}
function churn(flag: boolean): void {
  var i = 0i32; while (i < 4i32) {
    var item: Result[string[], string[]] = if (flag) { Ok(["left"]) } else { Err(["right"]) };
    defer sink(item);
    i = i + 1i32;
  }
}
function sink(item: Result[string[], string[]]): void { var value = match (item) { Ok(a) => a, Err(b) => b }; }`, "snapshot\n"},
	{"enum-loop-carried-alternatives", `function pilot(): string {
  var item: Result[string[], string[]] = Ok(["saved"]);
  var saved = match (item) { Ok(a) => a, Err(b) => b };
  var i = 0i32; while (i < 32i32) {
    item = if (i < 16i32) { Err(["error"]) } else { Ok(["replacement"]) };
    var value = match (item) { Ok(a) when i > 20i32 => a, Err(b) => b, Ok(a) => a };
    i = i + 1i32;
  }
  return saved[0];
}`, "saved\n"},
}

func TestEnumValuesBuild(t *testing.T) {
	for _, tc := range enumValueCases {
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

// Measures checked source through semantic construction, ownership proofs and
// ARM64 SSA lowering. Excludes parsing/checking and generated-program execution.
func BenchmarkEnumValuesBuild(b *testing.B) {
	for _, count := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("fields-%d", count), func(b *testing.B) {
			var source strings.Builder
			source.WriteString("enum E { Empty, Payload(")
			for i := range count {
				if i != 0 {
					source.WriteString(",")
				}
				source.WriteString("i32[]")
			}
			source.WriteString(")} function pilot(): i32[] { var x = Payload(")
			for i := range count {
				if i != 0 {
					source.WriteString(",")
				}
				source.WriteString("[1i32]")
			}
			source.WriteString("); return match (x) { Empty => [0i32], Payload(")
			for i := range count {
				if i != 0 {
					source.WriteString(",")
				}
				fmt.Fprintf(&source, "a%d", i)
			}
			fmt.Fprintf(&source, ") => a%d }; }", count-1)
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
