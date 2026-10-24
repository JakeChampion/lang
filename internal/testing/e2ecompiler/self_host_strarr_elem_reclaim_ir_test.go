package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostStrArrElemReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = heap bump grew → elements not reclaimed; 99 = over-release; 97 = value corrupted)", name, code, want)
		}
	}

	run(t, `function build(pre: string): i32 { let xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"strarr-elem-reclaim-flat", 0)

	run(t, `function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { let xs: string[] = ["lit", pre + "x", pre + "yy"]; acc = (acc + xs[0].len() + xs[2].len()) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"strarr-elem-reinit-loop", 0)

	run(t, `function pick(pre: string): i32 { let xs: string[] = [pre + "x", "qq"]; let t: string = xs[0]; return t.len() + xs[1].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (pick(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-elem-alias-excluded", 0)

	run(t, `function first(pre: string): string { let xs: string[] = [pre + "z"]; return xs[0]; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (first(pre).len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-elem-return-excluded", 0)

	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { let xs: string[] = [w(pre), "lit"]; xs = xs.append(w(pre)); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-elem-producer-store-flat", 0)

	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { let xs: string[] = mk(pre); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-local-from-producer-flat", 0)

	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function take(xs: string[]): i32 { return xs.len() + xs[0].len(); }
function build(pre: string): i32 { let xs: string[] = mk(pre); let k: i32 = take(xs); return k + xs[2].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-local-borrowed-arg-flat", 0)

	run(t, `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): i32 { let xs: string[] = mk(pre); let b: Box = keep(xs); return b.rows.len() + b.rows[0].len() + xs[2].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-local-stored-by-callee-counted", 0)

	run(t, `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): Box { let xs: string[] = mk(pre); let b: Box = keep(xs); return b; }
@noinline function ids(s: string): string { return s; }
function churnjunk(i: i32): i32 { let a: string[] = ["zzzz", "yyyy", ids("xxxx")]; return a[0].len() + a[2].len(); }
function round(i: i32): i32 {
    let pre: string = "ab";
    let b: Box = build(pre);
    let j: i32 = 0; let t: i32 = 0;
    while (j < 20) { t = t + churnjunk(j); j = j + 1; }
    let s: i32 = 0; let k: i32 = 0;
    while (k < b.rows.len()) { s = s + b.rows[k].len(); k = k + 1; }
    if (s != 129) { return 0 - 1; }
    return (t + s) % 101;
}
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 500) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
		"strarr-local-callee-holder-escapes", 8)

	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fwd(pre: string): string[] { let xs: string[] = mk(pre); return xs; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { let r: string[] = fwd(pre); if (r.len() + r[1].len() != 46) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); let before = __heap_bump_bytes(); let v2: i32 = churn(2000); let after = __heap_bump_bytes(); if (after != before) { return 98; } if (__rc_underflow_count() != 0) { return 99; } return v + v2; }`,
		"strarr-local-forwarded-return-owned", 0)

	run(t, `import "std/i32";
function mks(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function build(pre: string): i32 { let a: string[] = mks(pre); a = a.with(3, a[5]); return a.len() + a[3].len() + a[5].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-with-rebind-flat", 0)

	run(t, `import "std/i32";
function mks(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function churn(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function build(pre: string): i32 { let xs: string[] = mks(pre); let nm: string = pre + "-a-distinct-live-local-string-value"; xs = xs.with(1, nm); let junk: string = churn(20); if (junk.len() < 0) { return 0; } return nm.len() + xs[1].len() + xs[0].len(); }
function run2(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 97) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = run2(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-with-live-local-value-safe", 0)

	run(t, `import "std/i32";
function mks(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function churn(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function build(pre: string): i32 { let xs: string[] = mks(pre); xs = xs.with(3, xs[3]); let junk: string = churn(20); if (junk.len() < 0) { return 0; } return xs[3].len(); }
function run2(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 23) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = run2(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-with-self-store-safe", 0)

}
