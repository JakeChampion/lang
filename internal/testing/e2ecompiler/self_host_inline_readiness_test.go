package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

// A large shared callee stays out of line when its result is inspected,
// rather than replacing a dying box. Its caller must still become ready:
// waiting for a splice that cannot happen prevents the caller's own box
// from being eliminated (#12482).
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
