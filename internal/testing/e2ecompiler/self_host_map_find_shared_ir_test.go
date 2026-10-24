package e2ecompiler

import (
	"testing"
)

const (
	mapFindStrSrc = `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = Map {};
    m = m.insert("a", 1);
    m = m.insert("b", 9);
    if (!m.contains("a")) { return 90; }
    if (m.contains("zz")) { return 91; }
    m = m.insert("b", 2);
    let d_had: boolean = m.contains("q");
    let d: Map[string, i32] = m.remove("q");
    if (d_had) { return 91; }
    return m.get_or("a", 0) * 100 + m.get_or("zz", 5) * 10 + m.get_or("b", 0);
}
`
	mapFindI32Src = `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map {};
    m = m.insert(10, 1);
    m = m.insert(20, 9);
    if (!m.contains(10)) { return 90; }
    if (m.contains(99)) { return 91; }
    m = m.insert(20, 2);
    let d_had: boolean = m.contains(77);
    let d: Map[i32, i32] = m.remove(77);
    if (d_had) { return 91; }
    return m.get_or(10, 0) * 100 + m.get_or(99, 5) * 10 + m.get_or(20, 0);
}
`
	mapFindStructSrc = `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct K { a: i32, b: i32 }
function main(): i32 {
    let m: Map[K, i32] = Map {};
    m = m.insert(K { a: 1, b: 1 }, 1);
    m = m.insert(K { a: 2, b: 2 }, 9);
    if (!m.contains(K { a: 1, b: 1 })) { return 90; }
    if (m.contains(K { a: 9, b: 9 })) { return 91; }
    m = m.insert(K { a: 2, b: 2 }, 2);
    let d_had: boolean = m.contains(K { a: 7, b: 7 });
    let d: Map[K, i32] = m.remove(K { a: 7, b: 7 });
    if (d_had) { return 91; }
    return m.get_or(K { a: 1, b: 1 }, 0) * 100 + m.get_or(K { a: 9, b: 9 }, 5) * 10 + m.get_or(K { a: 2, b: 2 }, 0);
}
`
)

func mapFindCases() []struct{ name, src string } {
	return []struct{ name, src string }{
		{"string_key", mapFindStrSrc},
		{"i32_key", mapFindI32Src},
		{"struct_key", mapFindStructSrc},
	}
}

const mapFindFailFmt = "%s exited %d, want 152 (90=the search missed a present key, 91=it hit a key that was never inserted)"

func TestSelfHostMapFindSharedIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapFindCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 152 {
				t.Errorf(mapFindFailFmt, tc.name, code)
			}
		})
	}
}

func TestSelfHostMapFindSharedIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapFindCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 152 {
				t.Errorf(mapFindFailFmt, tc.name, code)
			}
		})
	}
}
