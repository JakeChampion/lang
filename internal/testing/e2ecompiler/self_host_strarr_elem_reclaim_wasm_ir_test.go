package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStrArrElemReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host string[] element reclaim wasm IR e2e")
	}
	dir := t.TempDir()
	l := newWasmStdlibLoader(t)

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		{"strarr-elem-reclaim-churn-wasm", `function build(pre: string): i32 { let xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 10) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(20000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		{"strarr-elem-reclaim-flat-wasm", `function build(pre: string): i32 { let xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"strarr-elem-reinit-loop-wasm", `function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { let xs: string[] = ["lit", pre + "x", pre + "yy"]; acc = (acc + xs[0].len() + xs[2].len()) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`, 0},
		{"strarr-elem-alias-excluded-wasm", `function pick(pre: string): i32 { let xs: string[] = [pre + "x", "qq"]; let t: string = xs[0]; return t.len() + xs[1].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (pick(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		{"strarr-elem-producer-store-wasm", `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { let xs: string[] = [w(pre), "lit"]; xs = xs.append(w(pre)); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(5000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		{"strarr-local-from-producer-wasm", `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { let xs: string[] = mk(pre); return xs.len() + xs[1].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 46) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(5000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		{"strarr-local-stored-by-callee-counted-wasm", `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): i32 { let xs: string[] = mk(pre); let b: Box = keep(xs); return b.rows.len() + b.rows[0].len() + xs[2].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
		{"strarr-local-callee-holder-escapes-wasm", `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): Box { let xs: string[] = mk(pre); let b: Box = keep(xs); return b; }
@noinline function ids(s: string): string { return s; }
function churnjunk(i: i32): i32 { let a: string[] = ["zzzz", "yyyy", ids("xxxx")]; return a[0].len() + a[2].len(); }
function round(i: i32): i32 { let pre: string = "ab"; let b: Box = build(pre); let j: i32 = 0; let t: i32 = 0; while (j < 20) { t = t + churnjunk(j); j = j + 1; } let s: i32 = 0; let k: i32 = 0; while (k < b.rows.len()) { s = s + b.rows[k].len(); k = k + 1; } if (s != 129) { return 0 - 1; } return (t + s) % 101; }
function main(): i32 { let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0; while (i < 500) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; } if (bad > 0) { return 100; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`, 8},
		{"strarr-with-rebind-wasm", `import "std/i32";
function mks(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function build(pre: string): i32 { let a: string[] = mks(pre); a = a.with(3, a[5]); return a.len() + a[3].len() + a[5].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 54) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(5000); if (__rc_underflow_count() != 0) { return 99; } return v; }`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := l.emit(t, tc.src)
			if len(wat) == 0 {
				t.Fatalf("no WAT for %q", tc.src)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("string[] element reclaim wasm IR %q = %d, want %d (99 = double-free detected)", tc.name, got, tc.expected)
			}
		})
	}
}
