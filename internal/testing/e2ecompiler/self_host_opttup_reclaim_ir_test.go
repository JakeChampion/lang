package e2ecompiler

import (
	"os/exec"
	"testing"
)

var optTupReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"opttup-churn", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[(i32, i32[])] = Some((i, [i, i + 1])); match (o) { Some(p) => { acc = (acc + p.0) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[(i32, i32[])] = Some((j, [j, j + 1])); match (o2) { Some(p) => { acc = (acc + p.0) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"opttup-borrow-full", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[(i32, i32[])] = Some((i, [i, i + 1])); match (o) { Some(p) => { acc = (acc + p.0 + p.1[0] + p.1[1] + p.1.len()) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[(i32, i32[])] = Some((j, [j, j + 1])); match (o2) { Some(p) => { acc = (acc + p.1[1]) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"opttup-escape-store-safe", `function main(): i32 {
    let keep: i32[] = [0, 0];
    let i: i32 = 0;
    while (i < 50) {
        let o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
        match (o) { Some(p) => { keep = p.1; }, None => {} }
        i = i + 1;
    }
    let acc: i32 = keep[0] + keep[1];
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"opttup-escape-call-safe", `function take(xs: i32[]): i32 { return xs[0]; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
        match (o) { Some(p) => { acc = (acc + take(p.1)) % 251; }, None => {} }
        i = i + 1;
    }
    if (acc < 0) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"opttup-escape-fn-safe", `function pick(o: Option[(i32, i32[])]): i32[] {
    match (o) { Some(p) => { return p.1; }, None => {} }
    return [0];
}
function main(): i32 {
    let o: Option[(i32, i32[])] = Some((5, [6, 7]));
    let a = pick(o);
    if (__rc_underflow_count() != 0) { return 99; }
    return a[0] + a[1];
}`, 13},
	{"opttup-string-elem-churn", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[(i32, string)] = Some((i, "v" + i.to_string())); match (o) { Some(p) => { acc = (acc + p.0 + p.1.len()) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[(i32, string)] = Some((j, "v" + j.to_string())); match (o2) { Some(p) => { acc = (acc + p.1.len()) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"opttup-string-arr-mixed-churn", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let o: Option[(string, i32[])] = Some(("tag", [i, i + 1])); match (o) { Some(p) => { acc = (acc + p.0.len() + p.1[0]) % 251; }, None => {} } i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let o2: Option[(string, i32[])] = Some(("tag", [j, j + 1])); match (o2) { Some(p) => { acc = (acc + p.1[1]) % 251; }, None => {} } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"opttup-string-ident-elem-safe", `function main(): i32 {
    let s: string = "seven";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { let o: Option[(i32, string)] = Some((i, s)); match (o) { Some(p) => { acc = (acc + p.1.len()) % 251; }, None => {} } i = i + 1; }
    if (s.len() != 5) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"opttup-string-extract-safe", `import "std/i32";
function main(): i32 {
    let keep: string = "";
    let i: i32 = 0;
    while (i < 100) {
        let o: Option[(i32, string)] = Some((i, "k" + i.to_string()));
        match (o) { Some(p) => { keep = p.1; }, None => {} }
        i = i + 1;
    }
    if (keep.len() < 2) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

func TestSelfHostOptTupReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range optTupReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = option leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
