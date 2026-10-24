package e2ecompiler

import "testing"

var mapValueAliasRetainIRCases = []struct {
	name string
	src  string
}{
	{"helper-insert", `import "core/map";
function put(m: Map[string, i32[]], k: string, a: i32, b: i32): Map[string, i32[]] {
    let arr: i32[] = [a, b];
    return m.insert(k, arr);
}

function main(): i32 {
    let m: Map[string, i32[]] = Map {};
    m = put(m, "k", 3, 4);
    let junk: i32[] = [7, 9];
    match (m.get("k")) {
        Some(v) => {
            if (v.len() != 2) { return 2; }
            if (v[0] != 3) { return 3; }
            if (v[1] != 4) { return 4; }
        },
        None => { return 1; }
    }
    if (junk[0] != 7) { return 5; }
    return 0;
}
`},
	{"loop-scoped-value", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32[]] = Map {};
    let i: i32 = 0;
    while (i < 6) {
        let arr: i32[] = [i, i + 100];
        m = m.insert(i, arr);
        i = i + 1;
    }
    let j: i32 = 0;
    while (j < 6) {
        match (m.get(j)) {
            Some(v) => {
                if (v.len() != 2) { return 2; }
                if (v[0] != j) { return 3; }
                if (v[1] != j + 100) { return 4; }
            },
            None => { return 1; }
        }
        j = j + 1;
    }
    return 0;
}
`},
	{"string-array-value", `import "core/map";
function put(m: Map[string, string[]], k: string, v: string): Map[string, string[]] {
    let a: string[] = [v];
    return m.insert(k, a);
}

function main(): i32 {
    let m: Map[string, string[]] = Map {};
    m = put(m, "k", "hello");
    let junk: string[] = ["zz"];
    match (m.get("k")) {
        Some(v) => {
            if (v.len() != 1) { return 2; }
            if (v[0] != "hello") { return 3; }
        },
        None => { return 1; }
    }
    if (junk[0] != "zz") { return 4; }
    return 0;
}
`},
	{"fresh-value-control", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32[]] = Map {};
    m = m.insert(1, [3, 4]);
    let junk: i32[] = [7, 9];
    match (m.get(1)) {
        Some(v) => {
            if (v[0] != 3) { return 2; }
            if (v[1] != 4) { return 3; }
        },
        None => { return 1; }
    }
    if (junk[0] != 7) { return 4; }
    return 0;
}
`},
}

func TestSelfHostMapValueAliasRetainIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapValueAliasRetainIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the map's value column read back wrong", tc.name, code, 0)
			}
		})
	}
}

func TestSelfHostMapValueAliasRetainIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapValueAliasRetainIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the map's value column read back wrong", tc.name, code, 0)
			}
		})
	}
}

func TestSelfHostMapValueAliasRetainIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapValueAliasRetainIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the map's value column read back wrong", tc.name, code, 0)
			}
		})
	}
}
