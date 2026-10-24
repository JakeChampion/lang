package e2ecompiler

import "testing"

const jsonArrayPrelude = `trait Json { function to_json(self: Self): string; }
impl Json for i32 { function to_json(self: Self): string { return self.to_string(); } }
impl Json for string { function to_json(self: Self): string { return "\"" + self + "\""; } }
pub function (xs: T[]) to_json[T: Json](): string {
    let out: string = "[";
    let i: i32 = 0;
    while (i < xs.len()) { if (i > 0) { out = out + ","; } out = out + xs[i].to_json(); i = i + 1; }
    return out + "]";
}
`

var jsonArrayIRCases = []struct {
	name string
	prog string
	exit int
}{
	{"i32-array", `function main(): i32 { let a: i32[] = [1, 2, 3]; return a.to_json().len(); }`, 7},
	{"string-array", `function main(): i32 { let a: string[] = ["x", "y"]; return a.to_json().len(); }`, 9},
	{"empty-array", `function main(): i32 { let a: i32[] = []; return a.to_json().len(); }`, 2},
	{"struct-array",
		`@derive(Json) struct Item { id: i32, tag: string }
function main(): i32 { let items: Item[] = [Item { id: 1, tag: "x" }, Item { id: 2, tag: "y" }]; return items.to_json().len(); }`, 39},
}

func jsonArraySrc(prog string) string {
	return "import \"std/i32\";\nimport \"std/string\";\n" + jsonArrayPrelude + "\n" + prog + "\n"
}

func TestSelfHostJsonArrayIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range jsonArrayIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, jsonArraySrc(tc.prog), target); code != tc.exit {
					t.Errorf("exited %d, want %d\n%s", code, tc.exit, stderr)
				}
			})
		}
	}
}
