package e2eselfhost

import "testing"

// A local that holds a counted reference to a buffer another name still
// reaches, grown by `.append` (#10367). `var out = acc` over a borrowed
// parameter retains acc's buffer. The self-append took the sole-owner push,
// which leaves a shared old buffer alone on a grow, so out's reference to it
// leaked. `return out.append(v)` kept `out` from the exit sweep without giving
// its reference to the old buffer back when the push grew. Answers are the
// interpreter's.

const borrowedAliasMain = `function main(): i32 {
    var pending: string[] = [];
    var fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    return pending.len() % 256;
}
`

var borrowedAliasAppendRows = []leakRow{
	{"issue", `function walk(n: i32, acc: string[]): string[] {
    var out: string[] = acc;
    if (n % 4 == 0) { out = out.append("w" + ""); }
    return out;
}
` + borrowedAliasMain, true, [2]int64{}},
	{"return_append_in_loop", `function walk(n: i32, acc: string[]): string[] {
    var out: string[] = acc;
    var i: i32 = 0;
    while (i < 3) {
        if ((n + i) % 5 == 0) { return out.append("w" + ""); }
        i = i + 1;
    }
    return out;
}
` + borrowedAliasMain, true, [2]int64{}},
	{"return_append_in_branch", `function walk(n: i32, acc: string[]): string[] {
    var out: string[] = acc;
    if (n % 4 == 0) { return out.append("w" + ""); }
    return out;
}
` + borrowedAliasMain, true, [2]int64{}},
	{"alias_of_alias", `function walk(n: i32, acc: string[]): string[] {
    var a: string[] = acc;
    var out: string[] = a;
    if (n % 4 == 0) { out = out.append("w" + ""); }
    if (n % 3 == 0) { out = out.append("v" + ""); }
    return out;
}
` + borrowedAliasMain, true, [2]int64{}},
	{"scalar_elems", `function walk(n: i32, acc: i32[]): i32[] {
    var out: i32[] = acc;
    if (n % 4 == 0) { out = out.append(n); }
    return out;
}
function main(): i32 {
    var pending: i32[] = [];
    var fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    return pending.len() + pending[2];
}
`, true, [2]int64{}},
	// A sole-owner local returned grown: the superseded buffer is this
	// frame's alone.
	{"owned_return_append", `function mk(n: i32): i32[] {
    var a: i32[] = [n, 2, 3, 4];
    return a.append(5);
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 5) { var x: i32[] = mk(i); t = t + x.len() + x[0]; i = i + 1; }
    return t;
}
`, true, [2]int64{}},
	// Counted elements: the buffers balance, and the elements stay with the
	// threaded accumulator's shallow release (#10420).
	{"struct_elems", `struct Inst { name: string, depth: i32 }
function walk(n: i32, acc: Inst[]): Inst[] {
    var out: Inst[] = acc;
    if (n % 4 == 0) { out = out.append(Inst { name: "w" + "", depth: n }); }
    return out;
}
function main(): i32 {
    var pending: Inst[] = [];
    var fd: i32 = 0;
    while (fd < 12) { pending = walk(fd, pending); fd = fd + 1; }
    return pending.len() + pending[1].depth;
}
`, false, [2]int64{}},
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
