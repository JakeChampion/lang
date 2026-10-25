package e2ecompiler

import (
	"testing"
)

// TestSelfHostMapIterMethodIR compiles Map iterators with strict IR.
func TestSelfHostMapIterMethodIR(t *testing.T) {
	cli := newStrictCLI(t)

	src := `import "core/map"; import "core/iter";
function f(): i32 {
    let m: Map[string, i32] = map_new(0);
    m = m.insert("a", 7);
    m = m.insert("b", 8);
    let sum: i32 = 0;
    let it: iter.ArrayIter[(string, i32)] = m.iter();
    for (__mapkey_it, __mapvalue_it) in it {
        sum = sum + __mapvalue_it;

    }
    return sum;
}
function main(): i32 { return f(); }`

	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", src)); code != 15 {
		t.Errorf("map-iter program exited %d, want 15 (7+8)", code)
	}
}
