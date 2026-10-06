package e2e

import "testing"

// --- arm64 concat across the old inline cap (#7446) ---------------------
//
// The native arm64 backend packed a string of 15 bytes or fewer inline in its
// two words, and these programs drove every consumer that had to decode that
// form (len, byte index, equality against a heap literal, slice, as_bytes,
// print) with the bytes on both sides of the data/len word boundary. A
// self-host string is never inline, so the lengths are now ordinary heap
// strings at the same boundaries, and each program is held to its answer and
// a balanced census.

// strcatBoundarySrc builds results of 8, 14 and 15 bytes from a
// runtime-selected operand (so nothing folds), then reads them back through
// every consumer. Those were the native inline form's boundaries: a full data
// word, bytes in the len word, and the cap. The literals it compares against
// are read-only, so equality crosses a built string and a literal.
const strcatBoundarySrc = `function pick(i: i32): string { if (i % 2 == 0) { return "abcde"; } return "vwxyz"; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let a: string = pick(i) + "fgh";
        let b: string = a + "ijklmn";
        let c: string = b + "o";
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

func TestArm64StrcatBoundariesRoundTrip(t *testing.T) {
	want := 0
	for i := 0; i < 100; i++ {
		first := int('a')
		if i%2 != 0 {
			first = int('v')
		}
		want = (want + 15 + first) % 101
	}
	want %= 83
	stdout, stderr, code := runLeakCheckArm64(t, strcatBoundarySrc)
	if code != want {
		t.Fatalf("exit code %d, want %d (a nonzero code below 11 names the failing check)", code, want)
	}
	if stdout != "abcdefgh\n" {
		t.Errorf("stdout = %q, want %q", stdout, "abcdefgh\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want at least one allocation, balanced / 0", allocs, frees, live)
	}
}

// strcatCapCrossingSrc makes a 16-byte result, one past the old cap, once per
// round. Each concat heap-allocates, so 100 rounds are 100 allocations, and
// each round's exit sweep frees its own.
const strcatCapCrossingSrc = `function pick(i: i32): string { if (i % 2 == 0) { return "abcdefgh"; } return "ABCDEFGH"; }
function round(i: i32): i32 {
    let s: string = pick(i) + "ijklmnop";
    if (s.len() != 16) { return 1000; }
    return s[15] as i32;
}
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

func TestArm64StrcatAllocatesOncePerRound(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, strcatCapCrossingSrc)
	if want := (100 * int('p')) % 83; code != want {
		t.Fatalf("exit code %d, want %d", code, want)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 100 {
		t.Errorf("allocs=%d, want 100: one concat result per round", allocs)
	}
	if frees != allocs || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want every round's buffer freed", allocs, frees, live)
	}
}

// as_bytes over a built string and over the empty one. 12 + 'a' + 'l' = 217.
func TestArm64StrcatAsBytes(t *testing.T) {
	_, _, code := runLeakCheckArm64(t, `function main(): i32 {
    let e: string = "";
    if (e.as_bytes().len() != 0) { return 1; }
    let s: string = "abc" + "defghijkl";
    let b = s.as_bytes();
    return b.len() + (b[0] as i32) + (b[11] as i32);
}`)
	if code != 217 {
		t.Errorf("exit = %d, want 217 (12 + 'a' + 'l'; 1 means the empty string's as_bytes was not empty)", code)
	}
}

// __str_slice reads a built base's length and then materialises its bytes.
func TestArm64StrcatSlice(t *testing.T) {
	_, _, code := runLeakCheckArm64(t, `function main(): i32 {
    let s: string = "abcde" + "fghijklmn";
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

// __fern_strbuf_append reads a built argument's bytes. 4 + 'd' = 104.
func TestArm64StrcatStrbufAppend(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, `function pick(i: i32): string { if (i == 0) { return "ab"; } return "AB"; }
function main(): i32 {
    strbuf_reset();
    strbuf_append(pick(0) + "cd");
    let s: string = strbuf_take();
    if (s != "abcd") { return 1; }
    return s.len() + (s[3] as i32);
}`)
	if code != 104 {
		t.Errorf("exit = %d, want 104 (4 + 'd'; 1 means the appended bytes were wrong)", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("got allocs=%d frees=%d live=%d, want at least one allocation, balanced / 0", allocs, frees, live)
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
