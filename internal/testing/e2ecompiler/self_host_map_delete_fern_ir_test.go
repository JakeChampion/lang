package e2ecompiler

import (
	"testing"
)

// `m.without(k)` removes a key and reports whether it was there.
//
// Each case deletes the middle key of three and then reads all three back, so
// a botched shift shows up as a wrong digit rather than a crash:
//
//	173 = a:1 kept * 100 + b: MISSING so the 7 default * 10 + c:3 kept
//
// 80 and 81 are the `existed` flag in each direction — a delete that reports
// nothing removed, and a miss that claims it removed something.
const (
	mapDelStrSrc = `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = Map {};
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    let r: (Map[string, i32], boolean) = m.without("b");
    if (!r.1) { return 80; }
    let n: Map[string, i32] = r.0;
    if (n.without("zz").1) { return 81; }
    return n.get_or("a", 0) * 100 + n.get_or("b", 7) * 10 + n.get_or("c", 0);
}
`
	mapDelI32Src = `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map {};
    m = m.insert(10, 1);
    m = m.insert(20, 2);
    m = m.insert(30, 3);
    let r: (Map[i32, i32], boolean) = m.without(20);
    if (!r.1) { return 80; }
    let n: Map[i32, i32] = r.0;
    if (n.without(99).1) { return 81; }
    return n.get_or(10, 0) * 100 + n.get_or(20, 7) * 10 + n.get_or(30, 0);
}
`
	// The struct key is the case that matters most: it is the only one that
	// reaches the derived `__fn_K__eq` through a runtime code address, which
	// is what the `eqfn: i32` parameter exists for.
	mapDelStructSrc = `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct K { a: i32, b: i32 }
function main(): i32 {
    let m: Map[K, i32] = Map {};
    m = m.insert(K { a: 1, b: 1 }, 1);
    m = m.insert(K { a: 2, b: 2 }, 2);
    m = m.insert(K { a: 3, b: 3 }, 3);
    let r: (Map[K, i32], boolean) = m.without(K { a: 2, b: 2 });
    if (!r.1) { return 80; }
    let n: Map[K, i32] = r.0;
    if (n.without(K { a: 9, b: 9 }).1) { return 81; }
    return n.get_or(K { a: 1, b: 1 }, 0) * 100 + n.get_or(K { a: 2, b: 2 }, 7) * 10 + n.get_or(K { a: 3, b: 3 }, 0);
}
`
)

func mapDelCases() []struct{ name, src string } {
	return []struct{ name, src string }{
		{"string_key", mapDelStrSrc},
		{"i32_key", mapDelI32Src},
		{"struct_key", mapDelStructSrc},
	}
}

const mapDelFailFmt = "%s exited %d, want 173 (80=delete reported nothing removed, 81=a miss claimed a removal, other=the shift moved the wrong element)"

// TestSelfHostMapDeleteFernIRX86_64 runs each key kind on x86-64. 173 is the
// interpreter's answer for all three, taken as the oracle.
func TestSelfHostMapDeleteFernIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapDelCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 173 {
				t.Errorf(mapDelFailFmt, tc.name, code)
			}
		})
	}
}

// TestSelfHostMapDeleteFernIRArm64 is the same three programs under qemu.
func TestSelfHostMapDeleteFernIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapDelCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 173 {
				t.Errorf(mapDelFailFmt, tc.name, code)
			}
		})
	}
}
