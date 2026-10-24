package e2ecompiler

import (
	"os/exec"
	"testing"
)

var freshRecvLenCases = []struct {
	name     string
	src      string
	expected int
}{
	{"freshrecv-len-churn", `import "std/i32";
function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen) + "";
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5000) { let b2s: string = "long-enough-payload-" + (i % 8).to_string(); acc = (acc + b2s.tails(4).len()) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"freshrecv-len-identity-alias-safe", `import "std/i32";
function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen) + "";
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 3000) {
        let b: string = "long-enough-payload-" + (i % 8).to_string();
        if (b.tails(0).len() != b.len()) { return 96; }
        if (b.len() != 21) { return 95; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"freshrecv-len-alternating", `import "std/i32";
function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen) + "";
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5000) {
        let b3: string = "long-enough-payload-" + (i % 8).to_string();
        acc = (acc + b3.tails(i % 2 * 4).len()) % 251;
        if (b3.len() != 21) { return 96; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"freshrecv-len-view-alias-safe", `import "std/i32";
function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 3000) {
        let b: string = "long-enough-payload-" + (i % 8).to_string();
        if (b.tails(4).len() != 17) { return 96; }
        if (b.len() != 21) { return 95; }
        if ((b[20] as i32) != 48 + (i % 8)) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"freshrecv-len-view-alternating", `import "std/i32";
function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5000) {
        let b3: string = "long-enough-payload-" + (i % 8).to_string();
        acc = (acc + b3.tails((i % 3) * 40 - 40).len()) % 251;
        if (b3.len() != 21) { return 96; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"freshrecv-len-chain-identity", `import "std/i32";
function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function (s: string) same(): string { return s; }
function main(): i32 {
    let i: i32 = 0;
    while (i < 3000) {
        let b: string = "long-enough-payload-" + (i % 8).to_string();
        if (b.tails(0).same().len() != 21) { return 96; }
        if (b.len() != 21) { return 95; }
        if ((b[20] as i32) != 48 + (i % 8)) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"freshrecv-len-chain-alias-safe", `import "std/i32";
function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function (s: string) owned(): string { return s + ""; }
function main(): i32 {
    let i: i32 = 0;
    while (i < 3000) {
        let b: string = "long-enough-payload-" + (i % 8).to_string();
        if (b.tails(4).owned().len() != 17) { return 96; }
        if (b.len() != 21) { return 95; }
        if ((b[20] as i32) != 48 + (i % 8)) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"freshrecv-len-view-nonrecv-return-refused", `import "std/i32";
function (s: string) pick(n: i32, alt: string): str {
    if (n < 0) { return alt; }
    if (n == 0) { return s; }
    return slice_unchecked(s, n, s.len());
}
function main(): i32 {
    let i: i32 = 0;
    while (i < 2000) {
        let b: string = "long-enough-payload-" + (i % 8).to_string();
        let alt: string = "alternate-payload-" + (i % 8).to_string();
        if (b.pick(0 - 1, alt).len() != 19) { return 96; }
        if (alt.len() != 19) { return 95; }
        if (b.len() != 21) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

var freshRecvLenLeakCases = []struct {
	name string
	src  string
}{
	{"freshrecv-len-leak-flat", `import "std/i32";
function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen) + "";
}
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let b: string = "long-enough-payload-" + (w % 8).to_string(); acc = (acc + b.tails(4).len()) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let c: string = "long-enough-payload-" + (i % 8).to_string(); acc = (acc + c.tails(4).len()) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	{"freshrecv-len-view-leak-flat", `import "std/i32";
function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let b: string = "long-enough-payload-" + (w % 8).to_string(); acc = (acc + b.tails(4).len()) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let c: string = "long-enough-payload-" + (i % 8).to_string(); acc = (acc + c.tails(4).len()) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	{"freshrecv-len-chain-bounded", `import "std/i32";
function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    let sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return slice_unchecked(s, n, sLen);
}
function (s: string) owned(): string { return s + ""; }
function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { let b: string = "long-enough-payload-" + (w % 8).to_string(); acc = (acc + b.tails(4).owned().len()) % 251; w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { let c: string = "long-enough-payload-" + (i % 8).to_string(); acc = (acc + c.tails(4).owned().len()) % 251; i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 240000) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
}

func TestSelfHostFreshRecvLenReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	cases := append([]struct {
		name     string
		src      string
		expected int
	}{}, freshRecvLenCases...)
	for _, lc := range freshRecvLenLeakCases {
		cases = append(cases, struct {
			name     string
			src      string
			expected int
		}{lc.name, lc.src, 0})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := []byte(l.emit(t, tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d (98 = fresh box leaked; 99 = over-release; 95/96 = aliased receiver corrupted)", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostFreshRecvLenReclaimWasm(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range freshRecvLenCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.expected {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.expected, stderr)
				}
			}
		})
	}
}
