package e2ecompiler

import (
	"os/exec"
	"testing"
)

var optStrBlockReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"optstr-loop-local-churn", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let o: Option[string] = Some("v" + w.to_string()); match (o) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } } w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let o2: Option[string] = Some("v" + i.to_string()); match (o2) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } } i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"resulterr-loop-local-churn", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let r: Result[i32, string] = Err("e" + w.to_string()); match (r) { Ok(v) => { acc = (acc + v) % 251; }, Err(m) => { acc = (acc + m.len()) % 251; } } w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let r2: Result[i32, string] = Err("e" + i.to_string()); match (r2) { Ok(v) => { acc = (acc + v) % 251; }, Err(m) => { acc = (acc + m.len()) % 251; } } i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optarr-loop-local-churn", `function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let o: Option[i32[]] = Some([w, w + 1]); match (o) { Some(xs) => { acc = (acc + xs[0]) % 251; }, None => { } } w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let o2: Option[i32[]] = Some([i, i + 1]); match (o2) { Some(xs) => { acc = (acc + xs[0]) % 251; }, None => { } } i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optstr-if-body-churn", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { if (w >= 0) { let o: Option[string] = Some("v" + w.to_string()); match (o) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } } } w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { if (i >= 0) { let o2: Option[string] = Some("v" + i.to_string()); match (o2) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } } } i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"optstr-binding-escape-safe", `import "std/i32";
function main(): i32 {
    let keep: string = "";
    let w: i32 = 0;
    while (w < 100) {
        let o: Option[string] = Some("k" + w.to_string());
        match (o) { Some(s) => { keep = s; }, None => { } }
        w = w + 1;
    }
    if (keep.len() < 2) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"optstr-alias-after-safe", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 100) {
        let o: Option[string] = Some("k" + w.to_string());
        let al: Option[string] = o;
        match (o) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } }
        match (al) { Some(s2) => { acc = (acc + s2.len()) % 251; }, None => { } }
        w = w + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"optstr-double-match-safe", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 100) {
        let o: Option[string] = Some("k" + w.to_string());
        match (o) { Some(s) => { acc = (acc + s.len()) % 251; }, None => { } }
        match (o) { Some(s2) => { acc = (acc + s2.len()) % 251; }, None => { } }
        w = w + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"optstr-return-arms-churn", `import "std/i32";
function probe(k: i32): i32 {
    let o: Option[string] = Some("v" + k.to_string());
    match (o) {
        Some(s) => { return s.len(); },
        None => { return 0; }
    }
    return 0;
}
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { acc = (acc + probe(w)) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { acc = (acc + probe(i)) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
	{"resulterr-return-arms-churn", `import "std/i32";
function probe(k: i32): i32 {
    let r: Result[i32, string] = Err("e" + k.to_string());
    match (r) {
        Ok(v) => { return v; },
        Err(m) => { return m.len(); }
    }
    return 0;
}
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { acc = (acc + probe(w)) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { acc = (acc + probe(i)) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
	{"optstr-mixed-arms-churn", `import "std/i32";
function probe(k: i32): i32 {
    let o: Option[string] = Some("v" + k.to_string());
    let acc: i32 = 0;
    match (o) {
        Some(s) => { if (s.len() > 100) { return 1; } acc = s.len(); },
        None => { return 0; }
    }
    return acc;
}
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { acc = (acc + probe(w)) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { acc = (acc + probe(i)) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
	{"optarr-return-arms-churn", `function probe(k: i32): i32 {
    let o: Option[i32[]] = Some([k, k + 1]);
    match (o) {
        Some(xs) => { return xs[0]; },
        None => { return 0; }
    }
    return 0;
}
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { acc = (acc + probe(w)) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { acc = (acc + probe(i)) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
	{"optstr-return-payload-safe", `import "std/i32";
function pick(k: i32): string {
    let o: Option[string] = Some("k" + k.to_string());
    match (o) {
        Some(s) => { return s; },
        None => { }
    }
    return "";
}
function main(): i32 {
    let s = pick(7);
    if (s.len() < 2) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"optstr-nested-return-arm-churn", `import "std/i32";
function probe(k: i32): i32 {
    if (k >= 0) {
        let o: Option[string] = Some("v" + k.to_string());
        match (o) {
            Some(s) => { return s.len(); },
            None => { return 0; }
        }
    }
    return 0;
}
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { acc = (acc + probe(w)) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { acc = (acc + probe(i)) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
}

func TestSelfHostOptStrBlockReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range optStrBlockReclaimCases {
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
