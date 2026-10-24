package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var optStructReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"optstruct-churn", `struct P { xs: i32[] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[P] = Some(P { xs: [i, i + 1] }); match (o) { Some(p) => { acc = (acc + p.xs[0]) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[P] = Some(P { xs: [j, j + 1] }); match (o2) { Some(p) => { acc = (acc + p.xs[0]) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optstruct-borrow-full", `struct P { n: i32, xs: i32[] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[P] = Some(P { n: i, xs: [i, i + 1] }); match (o) { Some(p) => { acc = (acc + p.n + p.xs[0] + p.xs.len()) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[P] = Some(P { n: j, xs: [j, j + 1] }); match (o2) { Some(p) => { acc = (acc + p.xs[1]) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optstruct-escape-store-safe", `struct P { xs: i32[] }
function main(): i32 {
    let keep: i32[] = [0, 0];
    let i: i32 = 0;
    while (i < 50) {
        let o: Option[P] = Some(P { xs: [i, i + 1] });
        match (o) { Some(p) => { keep = p.xs; }, None => {} }
        i = i + 1;
    }
    let acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"optstruct-escape-call-safe", `struct P { xs: i32[] }
function take(xs: i32[]): i32 { return xs[0]; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let o: Option[P] = Some(P { xs: [i, i + 1] });
        match (o) { Some(p) => { acc = (acc + take(p.xs)) % 251; }, None => {} }
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"optstruct-string-field-churn", `struct P { name: string, n: i32 }
function main(): i32 {
    let pre: string = "n";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[P] = Some(P { name: pre + "x", n: i }); match (o) { Some(p) => { acc = (acc + p.n + p.name.len()) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[P] = Some(P { name: pre + "x", n: j }); match (o2) { Some(p) => { acc = (acc + p.n + p.name.len()) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optstruct-string-field-alias-safe", `struct P { name: string, n: i32 }
function main(): i32 {
    let shared: string = "ab" + "cd";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[P] = Some(P { name: shared, n: i }); match (o) { Some(p) => { acc = (acc + p.n + p.name.len()) % 251; }, None => {} } i = i + 1; }
    let junk: string = "";
    let c: i32 = 0;
    while (c < 20) { junk = "zz" + "zz"; c = c + 1; }
    let sum: i32 = 0;
    let k: i32 = 0;
    while (k < shared.len()) { sum = sum + (shared[k] as i32); k = k + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0 || junk.len() != 4) { return 97; }
    if (sum != 394) { return 96; }
    return 0;
}`, 0},
	{"optstruct-scalar-only-payload", `struct P { a: i32, b: i32 }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[P] = Some(P { a: i, b: i + 1 }); match (o) { Some(p) => { acc = (acc + p.a + p.b) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[P] = Some(P { a: j, b: j + 1 }); match (o2) { Some(p) => { acc = (acc + p.a + p.b) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optstruct-string-escape-store-safe", `struct P { name: string, n: i32 }
function main(): i32 {
    let held: string = "";
    let i: i32 = 0;
    while (i < 50) {
        let o: Option[P] = Some(P { name: "a" + "b", n: i });
        match (o) { Some(p) => { held = p.name; }, None => {} }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (held.len() != 2) { return 97; }
    return 0;
}`, 0},
	{"optstruct-escape-fn-safe", `struct P { xs: i32[] }
function pick(o: Option[P]): i32[] {
    match (o) { Some(p) => { return p.xs; }, None => {} }
    return [0];
}
function main(): i32 {
    let o: Option[P] = Some(P { xs: [6, 7] });
    let a = pick(o);
    if (__rc_underflow_count() != 0) { return 99; }
    return a[0] + a[1];
}`, 13},
	{"optstrarr-churn", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[string[]] = Some(["a" + "b", "c"]); match (o) { Some(xs) => { acc = (acc + xs.len()) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[string[]] = Some(["a" + "b", "c"]); match (o2) { Some(xs) => { acc = (acc + xs.len()) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optstrarr-result-read", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Result[string[], string] = Ok(["ab", "cde"]); match (o) { Ok(xs) => { acc = (acc + xs[0].len() + xs[1].len()) % 251; }, Err(e) => { acc = acc + e.len(); } } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Result[string[], string] = Ok(["ab", "cde"]); match (o2) { Ok(xs) => { acc = (acc + xs[0].len()) % 251; }, Err(e) => { acc = acc + e.len(); } } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optstrarr-alias-safe", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let xs: string[] = ["a" + "b", "c"];
        let o: Option[string[]] = Some(xs);
        match (o) { Some(ys) => { acc = acc + ys.len(); }, None => {} }
        acc = acc + xs[0].len();
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 200) { return 97; }
    return 0;
}`, 0},
}

func TestSelfHostOptStructReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range optStructReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
				t.Errorf("%s = %d, want %d (98 = option leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
