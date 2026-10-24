package e2ecompiler

import "testing"

const borrowedAliasMain = `function main(): i32 {
    let pending: string[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    return pending.len() % 256;
}
`

var borrowedAliasAppendRows = []leakRow{
	{"issue", `function walk(n: i32, acc: string[]): string[] {
    let out: string[] = acc;
    if (n % 4 == 0) { out = out.append("w" + ""); }
    return out;
}
` + borrowedAliasMain, true},
	{"return_append_in_loop", `function walk(n: i32, acc: string[]): string[] {
    let out: string[] = acc;
    let i: i32 = 0;
    while (i < 3) {
        if ((n + i) % 5 == 0) { return out.append("w" + ""); }
        i = i + 1;
    }
    return out;
}
` + borrowedAliasMain, true},
	{"return_append_in_branch", `function walk(n: i32, acc: string[]): string[] {
    let out: string[] = acc;
    if (n % 4 == 0) { return out.append("w" + ""); }
    return out;
}
` + borrowedAliasMain, true},
	{"alias_of_alias", `function walk(n: i32, acc: string[]): string[] {
    let a: string[] = acc;
    let out: string[] = a;
    if (n % 4 == 0) { out = out.append("w" + ""); }
    if (n % 3 == 0) { out = out.append("v" + ""); }
    return out;
}
` + borrowedAliasMain, true},
	{"scalar_elems", `function walk(n: i32, acc: i32[]): i32[] {
    let out: i32[] = acc;
    if (n % 4 == 0) { out = out.append(n); }
    return out;
}
function main(): i32 {
    let pending: i32[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    return pending.len() + pending[2];
}
`, true},
	{"owned_return_append", `function mk(n: i32): i32[] {
    let a: i32[] = [n, 2, 3, 4];
    return a.append(5);
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 5) { let x: i32[] = mk(i); t = t + x.len() + x[0]; i = i + 1; }
    return t;
}
`, true},
	{"struct_elems", `struct Inst { name: string, depth: i32 }
function walk(n: i32, acc: Inst[]): Inst[] {
    let out: Inst[] = acc;
    if (n % 4 == 0) { out = out.append(Inst { name: "w" + "", depth: n }); }
    return out;
}
function main(): i32 {
    let pending: Inst[] = [];
    let fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    return pending.len() + pending[1].depth;
}
`, false},
}

func TestSelfHostBorrowedAliasAppendX86_64(t *testing.T) {
	runLeakRowsX86_64(t, borrowedAliasAppendRows)
}

func TestSelfHostBorrowedAliasAppendArm64(t *testing.T) {
	runLeakRowsArm64(t, borrowedAliasAppendRows)
}

func TestSelfHostBorrowedAliasAppendWasm(t *testing.T) {
	runLeakRowsWasm(t, borrowedAliasAppendRows)
}
