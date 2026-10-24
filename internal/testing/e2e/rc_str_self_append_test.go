package e2e

import (
	"strings"
	"testing"
)

const strSelfAppendLoopSrc = `function main(): i32 {
    let s: string = "";
    let i: i32 = 0;
    while (i < 2000) {
        s = s + "ab";
        i = i + 1;
    }
    if (s.len() != 4000) { return 1; }
    return 0;
}`

func TestX86_64StrSelfAppendAllocsBounded(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, strSelfAppendLoopSrc)
	if code != 0 {
		t.Fatalf("string self-append loop exited %d (want 0 — the accumulated length was wrong); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs > 400 {
		t.Errorf("allocs = %d for 2000 appends, want <= 400 (~one per 16-byte class step); the in-place append is not firing", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced after the append loop: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0", allocs, frees, live)
	}
}

const strSelfAppendCorrectnessSrc = `function build(n: i32, piece: string): string {
    let s: string = "";
    let i: i32 = 0;
    while (i < n) {
        s = s + piece;
        i = i + 1;
    }
    return s;
}

function main(): i32 {
    print(build(5, "ab"));
    print(build(40, "xyz"));
    print("[" + build(3, "") + "]");
    let d: string = "";
    let i: i32 = 0;
    while (i < 6) {
        let alias: string = d;
        d = d + "q";
        print(alias + "|" + d);
        i = i + 1;
    }
    let e: string = "ab";
    let k: i32 = 0;
    while (k < 5) {
        e = e + e;
        k = k + 1;
    }
    print(e);
    return 0;
}`

const strSelfAppendWant = `ababababab
xyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyz
[]
|q
q|qq
qq|qqq
qqq|qqqq
qqqq|qqqqq
qqqqq|qqqqqq
abababababababababababababababababababababababababababababababab`

func TestWASMStrSelfAppendCorrect(t *testing.T) {

	if got := runWasmCapturingStdout(t, strSelfAppendCorrectnessSrc); got != strSelfAppendWant {
		t.Errorf("wasm string self-append output =\n%q\nwant\n%q", got, strSelfAppendWant)
	}
}

func TestX86_64StrSelfAppendCorrect(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, strSelfAppendCorrectnessSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != strSelfAppendWant+"\n" {
		t.Errorf("x86-64 string self-append output =\n%q\nwant\n%q", stdout, strSelfAppendWant+"\n")
	}
	if allocs, frees, _ := parseLeakCheckLine(t, stderr); frees > allocs {
		t.Errorf("frees=%d > allocs=%d — the append over-released a buffer", frees, allocs)
	}
}

func TestArm64StrSelfAppendCorrect(t *testing.T) {

	stdout, stderr, code := runLeakCheckArm64(t, strSelfAppendCorrectnessSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != strSelfAppendWant+"\n" {
		t.Errorf("arm64 string self-append output =\n%q\nwant\n%q", stdout, strSelfAppendWant+"\n")
	}
	if allocs, frees, _ := parseLeakCheckLine(t, stderr); frees > allocs {
		t.Errorf("frees=%d > allocs=%d — the append over-released a buffer", frees, allocs)
	}
}

const strConcatChainSrc = `function main(): i32 {
    let out: string = "";
    let i: i32 = 0;
    while (i < 500) {
        out = out + "a" + "bb" + "ccc";
        i = i + 1;
    }
    if (out.len() != 3000) { return 1; }
    return 0;
}`

func TestX86_64StrConcatChainAllocsBounded(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, strConcatChainSrc)
	if code != 0 {
		t.Fatalf("chained concat loop exited %d (want 0 — the accumulated length was wrong); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs > 300 {
		t.Errorf("allocs = %d for 500 three-join iterations, want <= 300 (~one per size-class step); a join is allocating and copying instead of growing", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced after the chain loop: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0 (the accumulator's overwrite must FREE, not just decrement)", allocs, frees, live)
	}
}

const strConcatChainCorrectnessSrc = `function join3(a: string, b: string, c: string): string { return a + b + c; }

function main(): i32 {
    print(join3("aa", "bb", "cc"));
    print("<" + "x" + "|" + "yy" + ">");
    let out: string = "";
    let i: i32 = 0;
    while (i < 8) {
        out = out + "[" + "*" + "]";
        i = i + 1;
    }
    print(out);
    let d: string = "";
    let k: i32 = 0;
    while (k < 5) {
        let alias: string = d;
        d = d + "q" + "r";
        print(alias + "/" + d);
        k = k + 1;
    }
    let s: string = "abcdefghij";
    print(slice_unchecked(s, 0, 3) + slice_unchecked(s, 3, 6) + slice_unchecked(s, 6, 9) + "!");
    return 0;
}`

const strConcatChainWant = `aabbcc
<x|yy>
[*][*][*][*][*][*][*][*]
/qr
qr/qrqr
qrqr/qrqrqr
qrqrqr/qrqrqrqr
qrqrqrqr/qrqrqrqrqr
abcdefghi!`

func TestWASMStrConcatChainCorrect(t *testing.T) {

	if got := runWasmCapturingStdout(t, strConcatChainCorrectnessSrc); got != strConcatChainWant {
		t.Errorf("wasm chained concat output =\n%q\nwant\n%q", got, strConcatChainWant)
	}
}

func TestX86_64StrConcatChainCorrect(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, strConcatChainCorrectnessSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != strConcatChainWant+"\n" {
		t.Errorf("x86-64 chained concat output =\n%q\nwant\n%q", stdout, strConcatChainWant+"\n")
	}
	if allocs, frees, _ := parseLeakCheckLine(t, stderr); frees > allocs {
		t.Errorf("frees=%d > allocs=%d — the chain over-released an intermediate", frees, allocs)
	}
}

const strAppendClassBoundarySrc = `function main(): i32 {
    let s: string = "";
    let i: i32 = 0;
    while (i < 2100) {
        s = s + "ab";
        i = i + 1;
    }
    if (s.len() != 4200) { return 1; }
    let j: i32 = 0;
    while (j < 4200) {
        let want: i32 = 97;
        if (j % 2 == 1) { want = 98; }
        if ((s[j] as i32) != want) { return 2; }
        j = j + 1;
    }
    if (slice_unchecked(s, 2046, 2050) != "abab") { return 3; }
    if (slice_unchecked(s, 4196, 4200) != "abab") { return 4; }
    return 0;
}`

func TestX86_64StrAppendClassBoundary(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, strAppendClassBoundarySrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0 (1 = wrong length, 2 = a byte at the wrong position, 3/4 = a boundary slice); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 8 {
		t.Errorf("allocs = %d for 2100 appends across the 2048 tier change, want 8 — the grow takes an append its capacity does not hold, or sizes a fresh block other than by doubling", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

func TestArm64StrAppendClassBoundary(t *testing.T) {

	stdout, stderr, code := runLeakCheckArm64(t, strAppendClassBoundarySrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0 (1 = wrong length, 2 = a byte at the wrong position, 3/4 = a boundary slice); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 8 {
		t.Errorf("allocs = %d for 2100 appends across the 2048 tier change, want 8 — the grow takes an append its capacity does not hold, or sizes a fresh block other than by doubling", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

func TestWASMStrAppendClassBoundary(t *testing.T) {

	_, stderr, code := runLeakCheckWasm(t, strAppendClassBoundarySrc, false)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 11 {
		t.Errorf("allocs = %d for 2100 appends across the 2048 tier change, want 11 — the grow takes an append its capacity does not hold, or sizes a fresh block other than by doubling", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

const strChainAliasSrc = `function main(): i32 {
    let out: string = "ab";
    let i: i32 = 0;
    while (i < 5) {
        out = out + "-" + out;
        i = i + 1;
    }
    print(out);
    if (out.len() != 95) { return 1; }
    return 0;
}`

const strChainAliasWant = "ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab\n"

func TestX86_64StrSelfAppendChainAliasIsNotFused(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, strChainAliasSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0 (the accumulated length was wrong — the chain fused over a read of its own accumulator); stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != strChainAliasWant {
		t.Errorf("x86-64 aliased chain output =\n%q\nwant\n%q", stdout, strChainAliasWant)
	}
	if allocs, frees, live := parseLeakCheckLine(t, stderr); frees > allocs || live != 0 {
		t.Errorf("heap after the aliased chain: allocs=%d frees=%d live_bytes=%d, want frees <= allocs and live_bytes==0", allocs, frees, live)
	}
}

func TestArm64StrSelfAppendChainAliasIsNotFused(t *testing.T) {

	stdout, stderr, code := runLeakCheckArm64(t, strChainAliasSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != strChainAliasWant {
		t.Errorf("arm64 aliased chain output =\n%q\nwant\n%q", stdout, strChainAliasWant)
	}
	if allocs, frees, live := parseLeakCheckLine(t, stderr); frees > allocs || live != 0 {
		t.Errorf("heap after the aliased chain: allocs=%d frees=%d live_bytes=%d, want frees <= allocs and live_bytes==0", allocs, frees, live)
	}
}

func TestWASMStrSelfAppendChainAliasIsNotFused(t *testing.T) {

	if got := runWasmCapturingStdout(t, strChainAliasSrc); got != strings.TrimSuffix(strChainAliasWant, "\n") {
		t.Errorf("wasm aliased chain output =\n%q\nwant\n%q", got, strChainAliasWant)
	}
}
