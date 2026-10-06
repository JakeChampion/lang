package e2ecompiler

import (
	"testing"
)

// TestSelfHostMapIterMethodIR pins the Map.iter() builtin cluster
// (iter/has_next/key/value/advance) on the self-host x86-64 path. The four
// MapIter methods are compiler builtins with no function body; a program that
// walks a map through them must compile and answer.
func TestSelfHostMapIterMethodIR(t *testing.T) {
	cli := newStrictCLI(t)

	// Sum the values via the iterator: 7 + 8 = 15.
	src := `import "core/map";
function f(): i32 {
    let m: Map[string, i32] = map_new(0);
    m = m.insert("a", 7);
    m = m.insert("b", 8);
    let sum: i32 = 0;
    let it: MapIter[string, i32] = m.iter();
    while (it.has_next()) {
        sum = sum + it.value();
        it.advance();
    }
    return sum;
}
function main(): i32 { return f(); }`

	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", src)); code != 15 {
		t.Errorf("map-iter program exited %d, want 15 (7+8)", code)
	}
}
