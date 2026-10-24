package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var tostrScalarReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"tostr-scalar-loop-local", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let s: string = i.to_string(); acc = (acc + s.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let t: string = j.to_string(); acc = (acc + t.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 2048) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"tostr-scalar-param-receiver", `import "std/i32";
function fmt(n: i32): i32 {
    let s: string = n.to_string();
    return s.len();
}
function churn(k: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < k) { acc = (acc + fmt(1234567 + i)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let a: i32 = churn(400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 2048) { return 98; }
    return 0;
}`, 0},
	{"tostr-param-user-method-uncredited", `import "std/i32";
` + strProbeHelpers + `import "std/i32";
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
struct Holder { name: string, tag: string }
function (h: Holder) to_string(): string { return h.tag; }
function shown(h: Holder): i32 { let s: string = h.to_string(); return s.len() % 251; }
function churn(pre: string): i32 { let a: string = w(pre + "1"); let b: string = w(pre + "2"); return a.len() + b.len(); }
function main(): i32 {
    let keep: Holder = Holder { name: w("aaaa"), tag: w("bbbb") };
    let i: i32 = 0;
    while (i < 2000) {
        if (shown(keep) < 0) { return 96; }
        if (churn("QQQQQQQQ") < 0) { return 95; }
        if (!has_prefix(keep.name, "aaaa-")) { return 97; }
        if (!has_prefix(keep.tag, "bbbb-")) { return 97; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"tostr-freefn-control", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let s: string = i.to_string(); acc = (acc + s.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let t: string = j.to_string(); acc = (acc + t.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 2048) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"tostr-scalar-value-exact", `import "std/i32";
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let s: string = i.to_string();
        if (s.len() < 1) { return 97; }
        acc = (acc + s.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 239) { return 97; }
    return 0;
}`, 0},
	{"tostr-string-recv-uncredited", `import "std/i32";
import "std/string";
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let base: string = "ab";
        let s: string = base.to_string();
        if (base.len() != 2) { return 97; }
        if (s.len() != 2) { return 97; }
        acc = (acc + s.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 149) { return 97; }
    return 52;
}`, 52},
	{"tostr-scalar-struct-field", `import "std/i32";
import "std/string";
struct Rec { name: string }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    let b1: i32 = (__heap_bump_bytes() as i32);
    while (i < 5000) {
        let s: string = i.to_string();
        let r: Rec = Rec { name: s };
        if (r.name.len() != s.len()) { return 97; }
        acc = (acc + r.name.len()) % 251;
        i = i + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 2048) { return 98; }
    return 0;
}`, 0},
	{"tostr-scalar-alias-reassign", `import "std/i32";
import "std/string";
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    let b1: i32 = (__heap_bump_bytes() as i32);
    while (i < 5000) {
        let s: string = i.to_string();
        let t: string = "";
        t = s;
        if (t.len() != s.len()) { return 97; }
        acc = (acc + t.len()) % 251;
        i = i + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 2048) { return 98; }
    return 0;
}`, 0},
	{"tostr-scalar-field-read-after-loop", `import "std/i32";
import "std/string";
struct Rec { name: string }
function main(): i32 {
    let acc: i32 = 0;
    let first: string = (7 * 1000).to_string();
    let keep: Rec = Rec { name: first };
    let i: i32 = 0;
    while (i < 200) { let s: string = i.to_string(); let r: Rec = Rec { name: s }; acc = (acc + r.name.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let s: string = j.to_string(); let r: Rec = Rec { name: s }; acc = (acc + r.name.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 2048) { return 98; }
    if (keep.name != "7000") { return 97; }
    if (first.len() != 4) { return 97; }
    return 0;
}`, 0},
	{"tostr-string-recv-field-uncredited", `import "std/i32";
import "std/string";
struct Rec { name: string }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let base: string = "ab";
        let s: string = base.to_string();
        let r: Rec = Rec { name: s };
        let t: string = "";
        t = s;
        if (base != "ab" || r.name != "ab") { return 97; }
        acc = (acc + r.name.len() + t.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 47) { return 97; }
    return 52;
}`, 52},
	{"join-strarr-field-and-reassign", `import "std/array";
struct Rec { name: string }
function main(): i32 {
    let acc: i32 = 0;
    let b1: i32 = 0;
    let i: i32 = 0;
    while (i < 5200) {
        if (i == 200) { b1 = (__heap_bump_bytes() as i32); }
        let xs: string[] = ["ab", "cd"];
        let s: string = xs.join("-");
        let r: Rec = Rec { name: s };
        let t: string = "";
        t = s;
        if (r.name != "ab-cd" || t != "ab-cd") { return 97; }
        acc = (acc + r.name.len() + t.len()) % 251;
        i = i + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 2048) { return 98; }
    return 0;
}`, 0},
	{"trim-local-field-and-reassign", `import "std/string";
struct Rec { name: string }
function main(): i32 {
    let acc: i32 = 0;
    let b1: i32 = 0;
    let i: i32 = 0;
    while (i < 5200) {
        if (i == 200) { b1 = (__heap_bump_bytes() as i32); }
        let base: string = "  ab-cd  ";
        let s: string = base.trim();
        let r: Rec = Rec { name: s };
        let t: string = "";
        t = s;
        if (r.name != "ab-cd" || t != "ab-cd" || base.len() != 9) { return 97; }
        acc = (acc + r.name.len() + t.len()) % 251;
        i = i + 1;
    }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 2048) { return 98; }
    return 0;
}`, 0},
	{"tostr-scalar-escape-return-safe", `import "std/i32";
function mk(n: i32): string { let s: string = n.to_string(); return s; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let r: string = mk(i); acc = (acc + r.len()) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 239) { return 97; }
    return 0;
}`, 0},
	{"tostr-scalar-then-string-recv", `import "std/i32";
import "std/string";
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let s: string = i.to_string();
        let t: string = s.to_string();
        if (s.len() != t.len()) { return 97; }
        acc = (acc + s.len() + t.len()) % 251;
        i = i + 1;
    }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let u: string = j.to_string(); let v: string = u.to_string(); acc = (acc + v.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 2048) { return 98; }
    if (acc != 41) { return 97; }
    return 0;
}`, 0},
}

func TestSelfHostTostrScalarReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range tostrScalarReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = to_string box leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTostrScalarReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host to_string reclaim wasm IR e2e")
	}
	l := newWasmStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range tostrScalarReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := l.emit(t, tc.src+"\n")
			if len(wat) == 0 {
				t.Fatal("self-host wasm driver emitted 0 bytes")
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watFile)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s:\n%s", tc.name, wat)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = to_string box leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
