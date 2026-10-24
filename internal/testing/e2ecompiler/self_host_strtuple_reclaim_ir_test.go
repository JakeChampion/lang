package e2ecompiler

import (
	"os/exec"
	"testing"
)

var strTupleReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"str-tuple-concat-churn", `function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let t: (i32, string) = (w, "n" + "x"); acc = (acc + t.1.len()) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let t2: (i32, string) = (i, "n" + "x"); acc = (acc + t2.1.len()) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"str-tuple-tostring-churn", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let t: (i32, string) = (w, w.to_string()); acc = (acc + t.1.len()) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let t2: (i32, string) = (i, i.to_string()); acc = (acc + t2.1.len()) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"str-tuple-lit-mixed", `function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let t: (string, i32[]) = ("tag", [w, w + 1]); acc = (acc + t.1[0]) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let t2: (string, i32[]) = ("tag", [i, i + 1]); acc = (acc + t2.1[0]) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"str-tuple-lit-shallow", `function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let t: (i32, string) = (w, "abc"); acc = (acc + t.0 + t.1.len()) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let t2: (i32, string) = (i, "abc"); acc = (acc + t2.0 + t2.1.len()) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"str-tuple-ident-elem-safe", `function main(): i32 {
    let s: string = "seven";
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 100) { let t: (i32, string) = (w, s); acc = (acc + t.1.len()) % 251; w = w + 1; }
    if (s.len() != 5) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"str-tuple-escape-safe", `import "std/i32";
function mk(i: i32): (i32, string) {
    let t: (i32, string) = (i, "v" + i.to_string());
    return t;
}
function main(): i32 {
    let t = mk(5);
    if (t.0 != 5) { return 97; }
    if (t.1.len() != 2) { return 96; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"str-tuple-extract-escape-safe", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let keep: string = "";
    let w: i32 = 0;
    while (w < 100) {
        let t: (i32, string) = (w, "k" + w.to_string());
        keep = t.1;
        acc = (acc + keep.len()) % 251;
        w = w + 1;
    }
    if (keep.len() < 2) { return 97; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"str-tuple-discarded", `function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { (w, "x" + "y"); w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { (i, "x" + "y"); i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"str-tuple-field-discarded", `function main(): i32 {
    let w: i32 = 0;
    while (w < 200) { let t: (i32, string) = (w, "x" + "y"); t.1; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let t2: (i32, string) = (i, "x" + "y"); t2.1; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
	{"str-struct-literal-discarded", `struct S { tag: string }
function main(): i32 {
    let w: i32 = 0;
    while (w < 200) { S { tag: "v" + "x" }; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { S { tag: "v" + "x" }; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    return 0;
}`, 0},
}

func TestSelfHostStrTupleReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range strTupleReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = string-tuple leaked; 99 = over-release/underflow; 97/96 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
