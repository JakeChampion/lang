package e2ecompiler

import "testing"

var mapCompositeKeyLitCases = []struct {
	name string
	src  string
}{
	{"struct-key-one-field-differs", `import "core/cmp";
import "core/map";

@derive(cmp.Eq, cmp.Hash)
struct Key { a: i32, b: string }

function main(): i32 {
    let s: Map[Key, i32] = Map { Key { a: 1, b: "x" }: 10, Key { a: 1, b: "y" }: 20 };
    let i: Map[Key, i32] = Map { Key { a: 1, b: "x" }: 10, Key { a: 2, b: "x" }: 20 };
    if (s.len() != 2) { return 90; }
    if (i.len() != 2) { return 91; }
    if (s.get_or(Key { a: 1, b: "y" }, 0) != 20) { return 92; }
    if (i.get_or(Key { a: 2, b: "x" }, 0) != 20) { return 93; }
    return 0;
}
`},
	{"struct-key-read-miss-overwrite", `import "core/cmp";
import "core/map";

@derive(cmp.Eq, cmp.Hash)
struct Key { a: i32, b: string }

function main(): i32 {
    let m: Map[Key, i32] = Map { Key { a: 1, b: "x" }: 10, Key { a: 2, b: "y" }: 20 };
    if (m.get_or(Key { a: 1, b: "x" }, 0) != 10) { return 90; }
    if (m.get_or(Key { a: 3, b: "z" }, 77) != 77) { return 91; }
    if (m.contains(Key { a: 9, b: "q" })) { return 92; }
    m = m.insert(Key { a: 1, b: "x" }, 11);
    if (m.len() != 2) { return 93; }
    if (m.get_or(Key { a: 1, b: "x" }, 0) != 11) { return 94; }
    return 0;
}
`},
	{"enum-key-literal", `import "core/cmp";
import "core/map";

@derive(cmp.Eq, cmp.Hash)
enum Tag { Red, Green, Blue }

function main(): i32 {
    let e: Map[Tag, i32] = Map { Red: 1, Green: 2, Blue: 3 };
    if (e.len() != 3) { return 90; }
    if (e.get_or(Green, 0) != 2) { return 91; }
    if (e.get_or(Blue, 0) != 3) { return 92; }
    return 0;
}
`},
	{"insert-form-control", `import "core/cmp";
import "core/map";

@derive(cmp.Eq, cmp.Hash)
struct Key { a: i32, b: string }

function main(): i32 {
    let m: Map[Key, i32] = Map {};
    m = m.insert(Key { a: 1, b: "x" }, 10);
    m = m.insert(Key { a: 1, b: "y" }, 20);
    if (m.len() != 2) { return 90; }
    if (m.get_or(Key { a: 1, b: "y" }, 0) != 20) { return 91; }
    return 0;
}
`},
	{"scalar-key-control", `import "core/map";

function main(): i32 {
    let m: Map[i32, i32] = Map { 1: 10, 2: 20 };
    let s: Map[string, i32] = Map { "a": 40, "b": 2 };
    if (m.len() != 2 || s.len() != 2) { return 90; }
    if (m.get_or(2, 0) != 20) { return 91; }
    if (s.get_or("a", 0) != 40) { return 92; }
    return 0;
}
`},
}

func TestSelfHostMapCompositeKeyLitIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapCompositeKeyLitCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the composite key column read back wrong", tc.name, code, 0)
			}
		})
	}
}

func TestSelfHostMapCompositeKeyLitIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapCompositeKeyLitCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the composite key column read back wrong", tc.name, code, 0)
			}
		})
	}
}

func TestSelfHostMapCompositeKeyLitIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapCompositeKeyLitCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the composite key column read back wrong", tc.name, code, 0)
			}
		})
	}
}
