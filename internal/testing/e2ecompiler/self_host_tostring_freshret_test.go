package e2ecompiler

import "testing"

var tostringFreshRetCases = []struct {
	name string
	src  string
	want int
}{
	{"freshret-tostring-direct-bind", `function util_num(i: i32): string {
    return i.to_string();
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let sv: string = util_num(i);
        acc = (acc + sv.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(1000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	{"freshret-tostring-concat-operand", `function util_num(i: i32): string {
    return i.to_string();
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let sv: string = "v" + util_num(i);
        acc = (acc + sv.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(1000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	{"freshret-tostring-via-local", `function util_num(i: i32): string {
    let t: string = i.to_string();
    return t;
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let sv: string = util_num(i);
        acc = (acc + sv.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(1000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	{"freshret-concat-return-unchanged", `function util_num(i: i32): string {
    return "x" + i.to_string();
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let sv: string = util_num(i);
        acc = (acc + sv.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(1000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	{"freshret-struct-tostring-refused", `struct Tag { s: string }
function (t: Tag) to_string(): string {
    return t.s;
}
function util_tag(t: Tag): string {
    return t.to_string();
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let tg: Tag = Tag { s: "abc" };
        let sv: string = util_tag(tg);
        acc = (acc + sv.len() + tg.s.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 85},
	{"freshret-string-tostring-refused", `function util_s(s: string): string {
    return s.to_string();
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let base: string = "v" + i.to_string();
        let sv: string = util_s(base);
        acc = (acc + sv.len() + base.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 45},
}

const tostringFreshRetImports = "import \"std/i32\";\nimport \"std/string\";\n"

const tostringFreshRetFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostTostringFreshRet(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range tostringFreshRetCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tostringFreshRetImports+tc.src+"\n", target); code != tc.want {
					t.Errorf(tostringFreshRetFailFmt+"\n%s", tc.name, code, tc.want, stderr)
				}
			})
		}
	}
}
