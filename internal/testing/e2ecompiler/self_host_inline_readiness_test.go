package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

func TestSelfHostInlineReadinessSharedCall(t *testing.T) {
	const steps = 30
	program := `struct Box3 { a: i32, b: i32, c: i32 }
function large(n: i32): Option[i32] {
    let value: i32 = n;
` + strings.Repeat("    value = (value * 3 + n + 1) % 9973;\n", steps) + `
    return Some(value);
}
function wrapper(n: i32): Box3 {
    let value: i32 = match (large(n)) { Some(x) => { x }, None => { 0 } };
    return Box3 { a: value, b: n, c: 1 };
}
@noinline function other(n: i32): i32 {
    return match (large(n)) { Some(x) => { x }, None => { 0 } };
}
@noinline function rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let a: Box3 = wrapper(i);
        let b: Box3 = wrapper(i + 1);
        total = total + a.a + a.b + a.c + b.a + b.b + b.c;
        i = i + 1;
    }
    return total * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    if (other(3) < 0) { return 1; }
    print_int(rounds()); print("");
    return 0;
}
`
	var total int32
	for i := int32(0); i < 100; i++ {
		for _, n := range []int32{i, i + 1} {
			value := n
			for j := 0; j < steps; j++ {
				value = (value*3 + n + 1) % 9973
			}
			total += value + n + 1
		}
	}
	runSemanticProgram(t, "inline-readiness", program, []string{"large", "wrapper", "other", "rounds"},
		semInlineWants(fmt.Sprintf("%d\n", total*1000)), "large")
}

// count passes a word read out of its parameter to a call, so no caller
// takes its body. low, mid and top are each called one time, and each must
// still go into its caller.
const inlineRefusedCalleeProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk {
  function drop(self: Blk): void { buf_push_byte(self.h, 9); }
}
@noinline function probe(n: i32): i32 { return n; }
function count(b: Blk): i32 { return probe(buf_len(b.h)); }
function low(b: Blk, set: i32[]): i32 {
  if (set[1] > 100) { return 0 - 1; }
  return count(b) + set[0];
}
function mid(b: Blk, set: i32[]): i32 {
  if (set[0] < 0) { return 0 - 2; }
  return low(b, set) + set[1];
}
function top(b: Blk, set: i32[]): i32 {
  if (set[0] > 100000) { return 0 - 3; }
  return mid(b, set) + 1;
}
@noinline function round(b: Blk, i: i32): i32 {
  let set = [i, 2];
  let sum = 0;
  for k in 0..3 {
    sum += top(b, set);
  }
  return sum;
}
@noinline function make(h: usize): Blk { return Blk { h: h }; }
function main(): i32 {
  let h: usize = buf_new(8);
  buf_push_byte(h, 1);
  let b: Blk = make(h);
  let sum = 0;
  for i in 0..1000 {
    sum += round(b, i);
  }
  buf_free(h);
  print(sum);
  return 0;
}
`

func TestSelfHostInlineReadinessRefusedCallee(t *testing.T) {
	runSemanticProgramSplicing(t, "inline-refused-callee", inlineRefusedCalleeProgram,
		[]string{"count", "low", "mid", "top", "round"}, []string{"low", "mid", "top"},
		semInlineWants("1510500\n"), "count", "round")
}
