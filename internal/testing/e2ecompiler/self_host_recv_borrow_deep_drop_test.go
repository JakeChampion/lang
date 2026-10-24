package e2ecompiler

import "testing"

var recvBorrowDeepDropCases = []struct {
	name     string
	src      string
	expected int
}{
	{"recvborrow-identity-return-safe", `struct Box { tag: string, n: i32 }
function (b: Box) keep(): Box { return b; }
function main(): i32 {
    let acc: i32 = 0;
    let held: Box = Box { tag: "start-tag-value", n: 1 };
    let alias: Box = held.keep();
    let i: i32 = 0;
    while (i < 4000) { let b: Box = Box { tag: "start-tag-value", n: i % 8 }; acc = (acc + b.keep().n) % 251; i = i + 1; }
    if (alias.tag.len() != 15) { return 95; }
    if (held.tag.len() != 15) { return 96; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"recvborrow-field-move-safe", `struct Box { tag: string, n: i32 }
function (b: Box) label(): string { return b.tag; }
function main(): i32 {
    let acc: i32 = 0;
    let src: Box = Box { tag: "start-tag-value", n: 1 };
    let moved: string = src.label();
    let i: i32 = 0;
    while (i < 4000) { let b: Box = Box { tag: "start-tag-value", n: i % 8 }; acc = (acc + b.label().len()) % 251; i = i + 1; }
    if (moved.len() != 15) { return 95; }
    if (slice_unchecked(moved, 0, 5) != "start") { return 96; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"recvborrow-self-method-safe", `struct Box { tag: string, n: i32 }
function (b: Box) inner(): i32 { return b.n; }
function (b: Box) via(): i32 { return b.inner() + 1; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4000) { let b: Box = Box { tag: "start-tag-value", n: i % 8 }; acc = (acc + b.via()) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"recvborrow-aliased-field-balanced", `struct Box { tag: string, n: i32 }
function (b: Box) score(): i32 { return b.n * 2; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4000) {
        let nm: string = "start-tag-value";
        let b: Box = Box { tag: nm, n: i % 8 };
        acc = (acc + b.score()) % 251;
        if (nm.len() != 15) { return 95; }
        if (b.tag.len() != 15) { return 96; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"recvret-rebound-outer-no-over-release", `struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function rounds(n: i32): i32 {
    let t: i32 = 0;
    let keep: Box = Box { tag: "outer-tag-value", n: 0 };
    for i in 0..n { let b: Box = Box { tag: "start-tag-value", n: i % 8 }; keep = b.me(); t = (t + b.n) % 251; }
    return t + keep.tag.len();
}
function main(): i32 {
    let acc: i32 = rounds(4000);
    if (acc < 15) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"recvret-returned-result-safe", `struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function mk(k: i32): Box { let b: Box = Box { tag: "start-tag-value", n: k }; return b.me(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4000) { let r: Box = mk(i % 8); if (r.tag.len() != 15) { return 95; } acc = (acc + r.n) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"recvret-container-result-safe", `struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4000) {
        let b: Box = Box { tag: "start-tag-value", n: i % 8 };
        let xs: Box[] = [b.me()];
        if (xs[0].tag.len() != 15) { return 95; }
        acc = (acc + xs[0].n) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"recvborrow-array-field-safe", `struct Box { tag: string, items: i32[] }
function (b: Box) total(): i32 { return b.items.len() + b.tag.len(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4000) { let b: Box = Box { tag: "start-tag-value", items: [1, 2, 3] }; if (b.total() != 18) { return 95; } acc = (acc + b.items[i % 3]) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

var recvBorrowDeepDropLeakCases = []struct {
	name string
	src  string
}{
	{"recvborrow-deep-drop-flat", `import "std/i32";
struct Box { tag: string, n: i32 }
function (b: Box) score(): i32 { return b.n * 2; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; acc = (acc + b.score()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    let acc: i32 = rounds(200);
    let b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	{"recvident-inline-result-flat", `import "std/i32";
struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; acc = (acc + b.me().tag.len()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    let acc: i32 = rounds(200);
    let b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	{"recvident-borrowable-arg-flat", `import "std/i32";
struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function take(x: Box): i32 { return x.n; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; acc = (acc + take(b.me())) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    let acc: i32 = rounds(200);
    let b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	{"recvborrow-array-field-flat", `import "std/i32";
struct Box { tag: string, items: i32[] }
function (b: Box) total(): i32 { return b.items.len(); }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), items: [i, i + 1, i + 2] }; acc = (acc + b.total()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    let acc: i32 = rounds(200);
    let b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	{"recvident-chain-recv-flat", `import "std/i32";
struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function (b: Box) val(): i32 { return b.n; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; acc = (acc + b.me().val()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    let acc: i32 = rounds(200);
    let b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	{"recvident-chain-recv-array", `import "std/i32";
struct Box { tag: string, items: i32[] }
function (b: Box) me(): Box { return b; }
function (b: Box) total(): i32 { return b.items.len(); }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), items: [i, i + 1, i + 2] }; acc = (acc + b.me().total()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    let acc: i32 = rounds(200);
    let b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	{"recvident-chain-recv-double", `import "std/i32";
struct Box { tag: string, n: i32 }
function (b: Box) me(): Box { return b; }
function (b: Box) val(): i32 { return b.n; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let b: Box = Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; acc = (acc + b.me().me().val()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    let acc: i32 = rounds(200);
    let b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	{"recvident-chain-recv-freecall", `import "std/i32";
struct Box { tag: string, n: i32 }
function mk(i: i32): Box { return Box { tag: "start-tag-value-" + (i % 8).to_string(), n: i % 8 }; }
function (b: Box) me(): Box { return b; }
function (b: Box) val(): i32 { return b.n; }
function rounds(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) { let b: Box = mk(i); acc = (acc + b.me().val()) % 251; i = i + 1; }
    return acc;
}
function main(): i32 {
    let acc: i32 = rounds(200);
    let b1: i32 = (__heap_bump_bytes() as i32);
    acc = acc + rounds(5000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
}

func recvBorrowAllCases() []struct {
	name     string
	src      string
	expected int
} {
	out := append([]struct {
		name     string
		src      string
		expected int
	}{}, recvBorrowDeepDropCases...)
	for _, lc := range recvBorrowDeepDropLeakCases {
		out = append(out, struct {
			name     string
			src      string
			expected int
		}{lc.name, lc.src, 0})
	}
	return out
}

const recvBorrowFailFmt = "%s exited %d, want %d (98 = receiver fields leaked; 99 = over-release; 95/96 = a refused move was freed anyway)"

func TestSelfHostRecvBorrowDeepDropX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range recvBorrowAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.expected {
				t.Errorf(recvBorrowFailFmt, tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostRecvBorrowDeepDropArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range recvBorrowAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.expected {
				t.Errorf(recvBorrowFailFmt, tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostRecvBorrowDeepDropWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range recvBorrowAllCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.expected {
				t.Errorf(recvBorrowFailFmt, tc.name, code, tc.expected)
			}
		})
	}
}
