package e2e

import "testing"

// --- arm64 concat and the two-word inline form (#7446) ------------------
//
// arm64 runs the two-word string ABI with a 15-byte inline cap, and
// __fern_strcat is the first arm64 producer of non-empty inline strings:
// every literal is heap-form .rodata, so before it the only inline value in
// an arm64 program was the empty string. These tests pin both halves of
// that: the leak census is the instrument that says a short concat
// allocated nothing (an inline value has no block to leak), and the
// consumers that must decode the packed form (len, byte index, equality
// against a heap literal, slice, as_bytes, print) are driven with the
// bytes placed on both sides of the data/len word boundary.

// strcatSsoIssueReproSrc is #7446's repro: two concats of 2 and 3 bytes per
// round, rebound in one local. x86-64 measures allocs=0 through its
// single-word SSO; arm64 measured allocs=200 while its concat was heap-only.
const strcatSsoIssueReproSrc = `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var t: i32 = 0;
    var x: string = mkstr("x");
    x = mkstr("yz");
    t = (t + x.len()) % 101;
    return t;
}
function main(): i32 {
    var acc: i32 = 0; var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

func TestArm64StrcatShortResultIsInline(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, strcatSsoIssueReproSrc)
	if code != 51 {
		t.Fatalf("exit code %d, want 51", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 0 || frees != 0 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 0/0/0: a concat result of 15 bytes or fewer must stay inline on arm64 as it does on x86-64", allocs, frees, live)
	}
}

// strcatInlineFormSrc builds inline results of 8, 14 and 15 bytes from a
// runtime-selected operand (so nothing folds), then reads them back through
// every decoding consumer. 8 bytes fills the data word exactly; 14 and 15
// put bytes in the len word, 15 being the cap. The literals it compares
// against are heap-form, so equality crosses the two encodings.
const strcatInlineFormSrc = `function pick(i: i32): string { if (i % 2 == 0) { return "abcde"; } return "vwxyz"; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var a: string = pick(i) + "fgh";
        var b: string = a + "ijklmn";
        var c: string = b + "o";
        if (a.len() != 8) { return 1; }
        if (b.len() != 14) { return 2; }
        if (c.len() != 15) { return 3; }
        if (i % 2 == 0) {
            if (b != "abcdefghijklmn") { return 4; }
            if ((c[14] as i32) != 111) { return 5; }
            if ((c[9] as i32) != 106) { return 6; }
        } else {
            if (b != "vwxyzfghijklmn") { return 7; }
        }
        if (("" + a) != a) { return 8; }
        if ((a + "") != a) { return 9; }
        if (b == c) { return 10; }
        t = (t + c.len() + (c[0] as i32)) % 101;
        i = i + 1;
    }
    print(pick(0) + "fgh");
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`

func TestArm64StrcatInlineFormRoundTrips(t *testing.T) {
	want := 0
	for i := 0; i < 100; i++ {
		first := int('a')
		if i%2 != 0 {
			first = int('v')
		}
		want = (want + 15 + first) % 101
	}
	want %= 83
	stdout, stderr, code := runLeakCheckArm64(t, strcatInlineFormSrc)
	if code != want {
		t.Fatalf("exit code %d, want %d (a nonzero code below 11 names the failing check)", code, want)
	}
	if stdout != "abcdefgh\n" {
		t.Errorf("stdout = %q, want %q", stdout, "abcdefgh\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 0 || frees != 0 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 0/0/0: every string here fits the inline cap", allocs, frees, live)
	}
}

// strcatCapCrossingSrc makes a 16-byte result, one past the cap, once per
// round. It must heap-allocate, and the round's exit sweep must free it.
const strcatCapCrossingSrc = `function pick(i: i32): string { if (i % 2 == 0) { return "abcdefgh"; } return "ABCDEFGH"; }
function round(i: i32): i32 {
    var s: string = pick(i) + "ijklmnop";
    if (s.len() != 16) { return 1000; }
    return s[15] as i32;
}
function main(): i32 {
    var acc: i32 = 0; var i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

func TestArm64StrcatPastCapAllocatesAndFrees(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, strcatCapCrossingSrc)
	if want := (100 * int('p')) % 83; code != want {
		t.Fatalf("exit code %d, want %d", code, want)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 100 {
		t.Errorf("allocs=%d, want 100: a 16-byte concat result is one past the inline cap and must heap-allocate", allocs)
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want every round's buffer freed", allocs, frees, live)
	}
}

// An inline string has no address, so a slice header cannot alias it:
// as_bytes copies the bytes out first. 12 + 'a' + 'l' = 217.
func TestArm64StrcatInlineAsBytes(t *testing.T) {
	_, _, code := runLeakCheckArm64(t, `function main(): i32 {
    var e: string = "";
    if (e.as_bytes().len() != 0) { return 1; }
    var s: string = "abc" + "defghijkl";
    var b = s.as_bytes();
    return b.len() + (b[0] as i32) + (b[11] as i32);
}`)
	if code != 217 {
		t.Errorf("exit = %d, want 217 (12 + 'a' + 'l'; 1 means the empty string's as_bytes was not empty)", code)
	}
}

// __str_slice reads the base's length and then materialises its bytes; on
// an inline base both must see the packed len word.
func TestArm64StrcatInlineSlice(t *testing.T) {
	_, _, code := runLeakCheckArm64(t, `function main(): i32 {
    var s: string = "abcde" + "fghijklmn";
    match (s[8:12]) {
        Some(v) => { if (v != "ijkl") { return 1; } if (v.len() != 4) { return 2; } },
        None => { return 3; },
    }
    return 42;
}`)
	if code != 42 {
		t.Errorf("exit = %d, want 42 (a code below 4 names the failing check)", code)
	}
}

// __fern_strbuf_append reads the string's bytes; an inline argument must be
// spilled before its len word is untagged. 4 + 'd' = 104.
func TestArm64StrcatInlineStrbufAppend(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, `function pick(i: i32): string { if (i == 0) { return "ab"; } return "AB"; }
function main(): i32 {
    strbuf_reset();
    strbuf_append(pick(0) + "cd");
    var s: string = strbuf_take();
    if (s != "abcd") { return 1; }
    return s.len() + (s[3] as i32);
}`)
	if code != 104 {
		t.Errorf("exit = %d, want 104 (4 + 'd'; 1 means the appended bytes were wrong)", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 1 || frees != 1 || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want 1/1/0: only strbuf_take's buffer allocates", allocs, frees, live)
	}
}

// The path helpers NUL-terminate their argument from its len word; an
// inline path must be decoded, not sized by the tagged word. Each branch
// returns a distinct code so a failure names the helper.
func TestArm64StrcatInlinePathArgs(t *testing.T) {
	_, code, _ := compileArm64InDir(t, `function pick(i: i32): string { if (i == 0) { return "r"; } return "w"; }
function main(): i32 {
    match (read_file(pick(0) + ".txt")) {
        Ok(s) => { if (s != "hello") { return 1; } },
        Err(_) => { return 2; }
    }
    match (read_file_bytes(pick(0) + ".txt")) {
        Ok(b) => { if (b.len() != 5) { return 3; } },
        Err(_) => { return 4; }
    }
    match (write_file(pick(1) + ".txt", "wrote")) {
        Ok(_) => {},
        Err(_) => { return 5; }
    }
    match (read_file(pick(1) + ".txt")) {
        Ok(s) => { if (s != "wrote") { return 6; } },
        Err(_) => { return 7; }
    }
    match (open_reader(pick(0) + ".txt")) {
        Ok(_) => {},
        Err(_) => { return 8; }
    }
    return 42;
}`, map[string]string{"r.txt": "hello"})
	if code != 42 {
		t.Errorf("exit = %d, want 42 (a code below 9 names the failing helper)", code)
	}
}
