package main

import "testing"

// core/cmp makes the checker rewrite method calls before it checks owned
// arguments. The receiver must not count as an argument there either (#14234).
func TestCheckIgnoresAMethodReceiverAfterTheRewrite(t *testing.T) {
	entry := writeFern(t, `import "core/cmp";
struct Keys { a: Cell[i32[]] }
function take(slot: Cell[i32[]]): i32[] {
    let got = slot.get();
    slot.set([]);
    return got;
}
function consume(own xs: i32[]): i32 { return xs.len(); }
function f(k: Keys): i32 { return consume(take(k.a)); }
function main(): i32 { return 0; }
`)
	if err := runCheck(entry, ""); err != nil {
		t.Fatalf("check: %v", err)
	}
}
