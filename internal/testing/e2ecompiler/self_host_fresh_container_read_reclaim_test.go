package e2ecompiler

import (
	"os/exec"
	"testing"
)

var freshContainerReadReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"fresh-arr-index", `function lit(n: i32): i32[] { return [n, n + 1, n + 2, n + 3]; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { acc = acc + lit(i)[2]; i = i + 1; }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 1325) { return 91; }
    if (y != 5150) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"fresh-struct-field-scalar", `struct Pair { j: i32, k: i32 }
function pair(n: i32): Pair { return Pair { j: n, k: n + 1 }; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { acc = acc + pair(i).k; i = i + 1; }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 1275) { return 91; }
    if (y != 5050) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"fresh-struct-field-deep", `struct Bag { xs: i32[], k: i32 }
function bag(n: i32): Bag { return Bag { xs: [n, n + 1, n + 2, n + 3], k: n }; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { acc = acc + bag(i).k; i = i + 1; }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 1225) { return 91; }
    if (y != 4950) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"borrowed-field-refused", `struct Bag { xs: i32[], k: i32 }
function borrowed(v: i32[], n: i32): Bag { return Bag { xs: v, k: n }; }
function main(): i32 {
    let live: i32[] = [1, 2, 3];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + borrowed(live, i).k; i = i + 1; }
    let churn1: i32[] = [9, 9, 9];
    let churn2: i32[] = [9, 9, 9];
    if (t != 4950) { return 91; }
    if (live[0] + live[1] + live[2] != 6) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"local-built-producer", `function nums(n: i32): i32[] {
    let out: i32[] = [];
    let i: i32 = 0;
    while (i < 4) { out = out.append(n + i); i = i + 1; }
    return out;
}
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { acc = acc + nums(i)[1]; i = i + 1; }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 1275) { return 91; }
    if (y != 5050) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"param-seeded-producer-refused", `function seeded(src: i32[], n: i32): i32[] {
    let out: i32[] = [];
    out = src;
    out = out.append(n);
    return out;
}
function main(): i32 {
    let live: i32[] = [1, 2, 3];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + seeded(live, i)[0]; i = i + 1; }
    let churn1: i32[] = [9, 9, 9];
    let churn2: i32[] = [9, 9, 9];
    if (t != 100) { return 91; }
    if (live[0] + live[1] + live[2] != 6) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"fresh-strarr-index-bound", `import "std/i32";
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function strs(n: i32): string[] {
    let out: string[] = [];
    let i: i32 = 0;
    while (i < 4) { out = out.append(wide(n + i)); i = i + 1; }
    return out;
}
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let b: string = strs(i)[1]; acc = acc + b.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 2091) { return 91; }
    if (y != 4192) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"fresh-strarr-index-borrowed", `import "std/i32";
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function strs(n: i32): string[] {
    let out: string[] = [];
    let i: i32 = 0;
    while (i < 4) { out = out.append(wide(n + i)); i = i + 1; }
    return out;
}
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { acc = acc + strs(i)[0].len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 2090) { return 91; }
    if (y != 4190) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"fresh-struct-field-string", `import "std/i32";
struct Box { name: string, k: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function boxed(n: i32): Box { return Box { name: wide(n), k: n }; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let f: string = boxed(i).name; acc = acc + f.len() + boxed(i).name.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 4180) { return 91; }
    if (y != 8380) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"fresh-struct-method-field-string", `import "std/i32";
struct Box { tag: string, n: i32 }
function (b: Box) bump(): Box { return Box { tag: b.tag + "!", n: b.n + 1 }; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: 1 }; acc = acc + b.bump().tag.len(); i = i + 1; }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 900) { return 91; }
    if (y != 1800) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"fresh-struct-method-field-scalar", `struct Pair { j: i32, k: i32 }
function (p: Pair) bump(): Pair { return Pair { j: p.j + 1, k: p.k + 1 }; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let p: Pair = Pair { j: i, k: i + 1 }; acc = acc + p.bump().k; i = i + 1; }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 1325) { return 91; }
    if (y != 5150) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"method-field-string-bound", `import "std/i32";
struct Box { tag: string, n: i32 }
function (b: Box) bump(): Box { return Box { tag: b.tag + "!", n: b.n + 1 }; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: 1 };
        let f: string = b.bump().tag;
        acc = acc + f.len();
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 900) { return 91; }
    if (y != 1800) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"freshself-mixed-method-flat", `struct Pair { j: i32, k: i32 }
function (p: Pair) relabel(t: i32): Pair {
    if (t == 0) { return p; }
    return Pair { j: t, k: p.k };
}
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let p: Pair = Pair { j: 1, k: 2 };
        acc = acc + p.relabel(3).k + p.relabel(0).k;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 200) { return 91; }
    if (y != 400) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"freshself-string-field-read-safe", `struct Box { tag: string, n: i32 }
function (b: Box) relabel(t: string): Box {
    if (t.len() == 0) { return b; }
    return Box { tag: t, n: b.n };
}
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let b: Box = Box { tag: "start-tag-value", n: i % 8 };
        acc = acc + b.relabel("fresh-tag-value").tag.len();
        acc = acc + b.relabel("").tag.len() + b.tag.len();
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let x: i32 = rounds(50);
    let y: i32 = rounds(100);
    let churn1: string = "0123456789" + "0123456789";
    let churn2: string = "0123456789" + "0123456789";
    if (x != 2250) { return 91; }
    if (y != 4500) { return 93; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"freshself-identity-path-not-freed", `import "std/i32";
struct Box { tag: string, n: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function (b: Box) relabel(t: string): Box {
    if (t.len() == 0) { return b; }
    return Box { tag: t, n: b.n };
}
function main(): i32 {
    let keep: Box = Box { tag: wide(7), n: 7 };
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + keep.relabel("").tag.len(); i = i + 1; }
    let churn1: string = "0123456789" + "0123456789";
    let churn2: string = "0123456789" + "0123456789";
    if (t != 4100) { return 91; }
    if (keep.n != 7) { return 90; }
    if (keep.tag.len() != 41) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"method-identity-return-not-released", `import "std/i32";
struct Box { tag: string, n: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function (b: Box) me(): Box { return b; }
function main(): i32 {
    let keep: Box = Box { tag: wide(7), n: 7 };
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let f: string = keep.me().tag; t = t + f.len(); i = i + 1; }
    let churn1: string = "0123456789" + "0123456789";
    let churn2: string = "0123456789" + "0123456789";
    if (t != 4100) { return 91; }
    if (keep.tag.len() != 41) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"method-receiver-field-value-refused", `import "std/i32";
struct Box { tag: string, n: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function (b: Box) same(): Box { return Box { tag: b.tag, n: b.n }; }
function main(): i32 {
    let keep: Box = Box { tag: wide(7), n: 7 };
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let f: string = keep.same().tag; t = t + f.len(); i = i + 1; }
    let churn1: string = "0123456789" + "0123456789";
    let churn2: string = "0123456789" + "0123456789";
    if (t != 4100) { return 91; }
    if (keep.tag.len() != 41) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"strarr-aliased-element-refused", `import "std/i32";
struct Box { name: string, k: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function pair(b: Box): string[] { return [b.name, b.name]; }
function main(): i32 {
    let keep: Box = Box { name: wide(7), k: 7 };
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + pair(keep)[0].len(); i = i + 1; }
    let churn1: string = "0123456789" + "0123456789";
    let churn2: string = "0123456789" + "0123456789";
    if (t != 4100) { return 91; }
    if (keep.name.len() != 41) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"strfld-param-value-refused", `import "std/i32";
struct Box { name: string, k: i32 }
function wide(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function wrap(s: string, n: i32): Box { return Box { name: s, k: n }; }
function main(): i32 {
    let live: string = wide(3);
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let f: string = wrap(live, i).name; t = t + f.len(); i = i + 1; }
    let churn1: string = "0123456789" + "0123456789";
    let churn2: string = "0123456789" + "0123456789";
    if (t != 4100) { return 91; }
    if (live.len() != 41) { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"strfld-param-value-box-reclaimed", `struct Box { name: string, k: i32 }
function wrap(s: string, n: i32): Box { return Box { name: s, k: n }; }
function rounds(live: string, n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + wrap(live, i).name.len(); i = i + 1; }
    return t;
}
function main(): i32 {
    let live: string = "abcd" + "efgh";
    let b0: i64 = __heap_bump_bytes();
    let x: i32 = rounds(live, 50);
    let b1: i64 = __heap_bump_bytes();
    let y: i32 = rounds(live, 100);
    let b2: i64 = __heap_bump_bytes();
    if (x != 400) { return 91; }
    if (y != 800) { return 93; }
    if ((b2 - b1) > (b1 - b0)) { return 92; }
    if (live != "abcdefgh") { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},

	{"strfld-method-param-value-box-reclaimed", `struct Box { tag: string, n: i32 }
function (b: Box) relabel(t: string): Box { return Box { tag: t, n: b.n }; }
function churn(n: i32): i32 {
    let junk: string = "";
    let i: i32 = 0;
    while (i < n) { junk = junk + "z"; i = i + 1; }
    return junk.len();
}
function main(): i32 {
    let seed: Box = Box { tag: "seed", n: 1 };
    let owned: string = "hello" + "!";
    let got: string = seed.relabel(owned).tag;
    if (churn(200) != 200) { return 91; }
    if (got != "hello!") { return 90; }
    if (owned != "hello!") { return 90; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

func TestSelfHostFreshContainerReadReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range freshContainerReadReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := []byte(l.emit(t, tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (90 = a live buffer was freed and reused; "+
					"91/93 = wrong value; 92 = fresh bytes grew where the shape must be flat; "+
					"94 = flat where a known-open shape still grows — see the case comment; "+
					"99 = over-release/underflow)", tc.name, code, tc.want)
			}
		})
	}
}
