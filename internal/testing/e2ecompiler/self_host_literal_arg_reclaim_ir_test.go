package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostLiteralArgReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := []byte(l.emit(t, prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = literal-arg box leaked; 99 = over-release; 88 = live value freed; 97 = value corrupted)", name, code, want)
		}
	}

	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + readit("ab"); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + readit("ab"); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 4400) { return 97; }
    return 0;
}`, "literal-arg-borrowable-flat", 0)

	run(t, `@noinline function keepit(nm: string): string { return nm; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let got: string = keepit("xy");
        if (got.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "literal-arg-retained-safe", 0)

	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    let base: string = "a";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + readit(base + "bc"); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + readit(base + "bc"); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (base.len() != 1) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 6600) { return 97; }
    return 0;
}`, "fresh-concat-arg-flat", 0)

	run(t, `import "std/string";
function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    let src: string = "AbC" + "d";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + readit(src.to_ascii_upper()); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + readit(src.to_ascii_upper()); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (src.len() != 4) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 8800) { return 97; }
    return 0;
}`, "fresh-producer-arg-flat", 0)

	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    let src: string = "aa" + "bb";
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        if (readit(src) != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (src.len() != 4) { return 88; }
    if (bad != 0) { return 87; }
    return 0;
}`, "bare-ident-arg-safe", 0)

	run(t, `function pick(a: string, b: string): i32 { return a.len() + b.len(); }
function main(): i32 {
    let live: string = "aa" + "bb";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + pick("x", live); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + pick("x", live); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (live.len() != 4) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 11000) { return 97; }
    return 0;
}`, "literal-arg-mixed-flat", 0)

	run(t, `function (s: string) readit(nm: string): i32 { return s.len() + nm.len(); }
function main(): i32 {
    let recv: string = "rr";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + recv.readit("ab"); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + recv.readit("ab"); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (recv.len() != 2) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 8800) { return 97; }
    return 0;
}`, "method-literal-arg-borrowable-flat", 0)

	run(t, `function (k: i32) tally(nm: string): i32 { return k + nm.len(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + (i % 4).tally("abc"); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + (j % 4).tally("abc"); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "method-literal-arg-prim-recv-flat", 0)

	run(t, `function (s: string) readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    let recv: string = "rr";
    let base: string = "a";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + recv.readit(base + "bc"); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + recv.readit(base + "bc"); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (base.len() != 1) { return 88; }
    if (recv.len() != 2) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 6600) { return 97; }
    return 0;
}`, "method-fresh-concat-arg-flat", 0)

	run(t, `function (s: string) keepit(nm: string): string { return nm; }
function main(): i32 {
    let recv: string = "rr";
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let got: string = recv.keepit("xy");
        if (got.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "method-literal-arg-retained-safe", 0)

	run(t, `struct Box { tag: string, n: i32 }
function (b: Box) relabel(t: string): Box {
    if (t.len() == 0) { return b; }
    return Box { tag: t, n: b.n };
}
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let b: Box = Box { tag: "start", n: i % 8 };
        let r: Box = b.relabel("fresh-tag-value");
        if (r.tag.len() != 15) { bad = 1; }
        if (b.tag.len() != 5) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "method-literal-arg-consumed-safe", 0)

	run(t, `import "std/i32";
function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function size(s: string): i32 { return s.len(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = (acc + size(mks(i))) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 3000) { acc = (acc + size(mks(j))) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "producer-call-arg-borrowable-flat", 0)

	run(t, `import "std/i32";
function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
@noinline function pick(s: string): string { return s; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) {
        let r: string = pick(mks(i));
        if (r.len() < 41) { bad = 1; }
        if (slice_unchecked(r, 0, 1) != "a") { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arg-returned-safe", 0)

	run(t, `import "std/i32";
struct Box { name: string, k: i32 }
function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function keep(s: string, k: i32): Box { return Box { name: s, k: k }; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) {
        let b: Box = keep(mks(i), i);
        if (b.name.len() < 41) { bad = 1; }
        if (b.k != i) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arg-stored-safe", 0)

	run(t, `import "std/i32";
function mks(n: i32): string { return "a-string-well-past-the-inline-threshold-" + n.to_string(); }
function size(s: string): i32 { return s.len(); }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) {
        let nm: string = mks(i);
        if (size(nm) < 41) { bad = 1; }
        if (nm.len() < 41) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arg-bound-local-safe", 0)

	run(t, `function mk(n: i32): i32[] { let out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function size(d: i32[]): i32 { return d.len(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = (acc + size(mk(i))) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 3000) { acc = (acc + size(mk(j))) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "producer-call-arr-arg-borrowable-flat", 0)

	run(t, `function mk(n: i32): i32[] { let out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
@noinline function pick(d: i32[]): i32[] { return d; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) {
        let r: i32[] = pick(mk(i));
        if (r.len() != 3) { bad = 1; }
        if (r[2] != i + 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arr-arg-returned-safe", 0)

	run(t, `struct Node { deps: i32[], k: i32 }
function mk(n: i32): i32[] { let out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function node(deps: i32[], k: i32): Node { return Node { deps: deps, k: k }; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) {
        let b: Node = node(mk(i), i);
        if (b.deps.len() != 3) { bad = 1; }
        if (b.deps[1] != i + 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arr-arg-stored-safe", 0)

	run(t, `function mk(n: i32): i32[] { let out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function size(d: i32[]): i32 { return d.len(); }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) {
        let live: i32[] = mk(i);
        if (size(live) != 3) { bad = 1; }
        if (live[0] != i) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "producer-call-arr-arg-bound-local-safe", 0)

	run(t, `struct Node { deps: i32[], k: i32 }
function mk(n: i32): i32[] { let out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function keep(deps: i32[], k: i32): i32 { let n: Node = Node { deps: deps, k: k }; return n.k + n.deps.len(); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = (acc + keep(mk(i), i)) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 3000) { acc = (acc + keep(mk(j), j)) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "counted-retain-arr-arg-flat", 0)

	run(t, `struct Node { deps: i32[], k: i32 }
function mk(n: i32): i32[] { let out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function both(deps: i32[], k: i32): i32[] { let n: Node = Node { deps: deps, k: k }; return n.deps; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) {
        let r: i32[] = both(mk(i), i);
        if (r.len() != 3) { bad = 1; }
        if (r[0] != i || r[2] != i + 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "counted-retain-arr-arg-pointer-result-safe", 0)

	run(t, `function mk(n: i32): i32[] { let out: i32[] = []; for i in 0..3 { out = out.append(n + i); } return out; }
function hand(deps: i32[]): i32 { let keepit: i32[] = deps; return keepit.len() + deps[0]; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 3000) { if (hand(mk(i)) != 3 + i) { bad = 1; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "counted-retain-arr-arg-aliased-param-safe", 0)

	run(t, `struct Q { tag: string, k: i32 }
function mkq(t: string, k: i32): Q { return Q { tag: t, k: k }; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let c: Q = mkq("a-string-well-past-the-inline-threshold", i); acc = (acc + c.tag.len() + c.k) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 3000) { let d: Q = mkq("a-string-well-past-the-inline-threshold", j); acc = (acc + d.tag.len() + d.k) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "counted-retain-str-arg-flat", 0)

	run(t, `struct Q { tag: string, k: i32 }
function both(t: string, k: i32): string { let n: Q = Q { tag: t, k: k }; return n.tag; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 400) {
        let r: string = both("a-string-well-past-the-inline-threshold", i);
        if (r.len() != 39) { bad = 1; }
        let churn: string = "another-long-string-to-recycle-the-block" + "";
        if (churn.len() != 40) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "counted-retain-str-arg-result-aliases-safe", 0)

	run(t, `struct Q { tag: string, k: i32 }
function mkq(t: string, k: i32): Q { return Q { tag: t, k: k }; }
function main(): i32 {
    let bad: i32 = 0;
    let live: string = "a-string-well-past-the-inline-threshold";
    let i: i32 = 0;
    while (i < 400) {
        let c: Q = mkq(live, i);
        if (c.tag.len() != 39) { bad = 1; }
        let churn: string = "another-long-string-to-recycle-the-block" + "";
        if (churn.len() != 40) { bad = 1; }
        i = i + 1;
    }
    if (live.len() != 39) { bad = 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "counted-retain-str-arg-live-local-safe", 0)

	run(t, `struct Q { tag: string, k: i32 }
function mkq2(t: string, k: i32): Q { return Q { tag: t, k: k + (t[0] as i32) }; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let c: Q = mkq2("tag", i); acc = (acc + c.k + c.tag.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { let d: Q = mkq2("tag", j); acc = (acc + d.k + d.tag.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "counted-retain-str-arg-index-read-flat", 0)

	run(t, `struct C { name: string, args: string }
function mk(name: string, args: string): C { return C { name: name, args: args }; }
function esc(c: C): string[] { let o: string[] = []; return o.append(c.name); }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let a: C = mk("fetch", "GET /a");
        let e: string[] = esc(a);
        if (e[0].len() != 5) { bad = 1; }
        if (a.args.len() != 6) { bad = 1; }
        if (a.args[0] != 71) { bad = 1; }
        if (a.name[0] != 102) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "counted-retain-str-arg-uncounted-store-safe", 0)
}
