package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// shaCoreSrc is std/crypto's SHA-256 core (import-free), trimmed to what a
// digest probe needs. `main` returns one digest byte so the exit code is a
// stable, all-bytes-sensitive witness (via the chosen index).
const shaCoreSrc = `
function __crypto_k(): u32[] {
    return [
        0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
        0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
        0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
        0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
        0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
        0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
        0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
        0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
    ];
}
function __crypto_h0(): u32[] {
    return [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19];
}
function __rotr(x: u32, n: u32): u32 { return (x >> n) | (x << (32 - n)); }
function __zeros32(n: i32): u32[] {
    var a: u32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(0); i = i + 1; }
    return a;
}
function __sha256_compress(h: u32[], k: u32[], msg: u8[], off: i32): u32[] {
    var w: u32[] = __zeros32(64);
    var t: i32 = 0;
    while (t < 16) {
        var b: i32 = off + t * 4;
        var word: u32 = ((msg[b] as u32) << 24) | ((msg[b + 1] as u32) << 16) | ((msg[b + 2] as u32) << 8) | (msg[b + 3] as u32);
        w = w.with(t, word);
        t = t + 1;
    }
    t = 16;
    while (t < 64) {
        var w15: u32 = w[t - 15];
        var w2: u32 = w[t - 2];
        var s0: u32 = __rotr(w15, 7) ^ __rotr(w15, 18) ^ (w15 >> 3);
        var s1: u32 = __rotr(w2, 17) ^ __rotr(w2, 19) ^ (w2 >> 10);
        w = w.with(t, w[t - 16] + s0 + w[t - 7] + s1);
        t = t + 1;
    }
    var a: u32 = h[0]; var bb: u32 = h[1]; var c: u32 = h[2]; var d: u32 = h[3];
    var e: u32 = h[4]; var f: u32 = h[5]; var g: u32 = h[6]; var hh: u32 = h[7];
    t = 0;
    while (t < 64) {
        var bs1: u32 = __rotr(e, 6) ^ __rotr(e, 11) ^ __rotr(e, 25);
        var ch: u32 = (e & f) ^ ((e ^ 0xffffffff) & g);
        var t1: u32 = hh + bs1 + ch + k[t] + w[t];
        var bs0: u32 = __rotr(a, 2) ^ __rotr(a, 13) ^ __rotr(a, 22);
        var maj: u32 = (a & bb) ^ (a & c) ^ (bb & c);
        var t2: u32 = bs0 + maj;
        hh = g; g = f; f = e; e = d + t1; d = c; c = bb; bb = a; a = t1 + t2;
        t = t + 1;
    }
    var out: u32[] = __zeros32(8);
    out = out.with(0, h[0] + a); out = out.with(1, h[1] + bb); out = out.with(2, h[2] + c); out = out.with(3, h[3] + d);
    out = out.with(4, h[4] + e); out = out.with(5, h[5] + f); out = out.with(6, h[6] + g); out = out.with(7, h[7] + hh);
    return out;
}
function __sha256_core(msg: u8[]): u8[] {
    var ml: i32 = msg.len();
    var total: i32 = ml + 1 + 8;
    var plen: i32 = total;
    if (total % 64 != 0) { plen = total + (64 - (total % 64)); }
    var m: u8[] = __alloc_u8(plen);
    var z: i32 = 0;
    while (z < plen) { m = m.with(z, 0 as u8); z = z + 1; }
    var i: i32 = 0;
    while (i < ml) { m = m.with(i, msg[i]); i = i + 1; }
    m = m.with(ml, 0x80 as u8);
    var bits: i64 = (ml as i64) * 8;
    var j: i32 = 0;
    while (j < 8) {
        var shift: i64 = (j * 8) as i64;
        m = m.with(plen - 1 - j, ((bits >> shift) & 255) as u8);
        j = j + 1;
    }
    var k: u32[] = __crypto_k();
    var h: u32[] = __crypto_h0();
    var off: i32 = 0;
    while (off < plen) { h = __sha256_compress(h, k, m, off); off = off + 64; }
    var digest: u8[] = __alloc_u8(32);
    var wi: i32 = 0;
    while (wi < 8) {
        var word: u32 = h[wi];
        digest = digest.with(wi * 4, ((word >> 24) & 255) as u8);
        digest = digest.with(wi * 4 + 1, ((word >> 16) & 255) as u8);
        digest = digest.with(wi * 4 + 2, ((word >> 8) & 255) as u8);
        digest = digest.with(wi * 4 + 3, (word & 255) as u8);
        wi = wi + 1;
    }
    return digest;
}
function __str_to_bytes(s: string): u8[] {
    var n: i32 = s.len();
    var b: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < n) { b = b.with(i, s[i] as u8); i = i + 1; }
    return b;
}
`

// TestSelfHostU32WrapIR proves the self-hosted x86-64 IR path computes u32
// arithmetic with 2^32 wrapping (matching the native compiler) — the bit the
// IR path lacked, which silently miscompiled std/crypto's SHA-256 (#2861). The
// register backends keep values in 64-bit registers, so a u32 op (add / sub /
// mul / shl, plus __rotr's `x << (32-n)`) must mask back to 32 bits and a u32
// `>>` must be a LOGICAL shift; this is what local_is_u32 / op_u32_wrap drive.
//
// The oracle is the NATIVE compiler (compileAndRunX86_64), NOT the AST path:
// the legacy self-host AST backend has the SAME u32-overflow bug, so the IR
// path now intentionally diverges from (is more correct than) it. For an
// overflow program the AST path's answer differs, so IR == native also proves
// the program took the IR path rather than the AST fallback.
func TestSelfHostU32WrapIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
	}{
		// u32 add overflow: 0x80000000 + 0x80000000 wraps to 0; the >>28 brings
		// the would-be carry bit (bit 32) into the low byte if unmasked.
		{"add-wrap", `function main(): i32 { var x: u32 = 0x80000000; var s: u32 = x + x; return ((s >> 28) & 255) as i32; }`},
		// A u32 var initialised with a DECIMAL literal > i32-max (`4000000000`,
		// width-infers 64) must stay a 32-bit u32, not get i64-backed — otherwise
		// its arithmetic runs at full 64-bit width and skips the 2^32 wrap. The
		// >>28 reveals the unmasked carry bit if the add didn't wrap. (Forms:
		// reassign-to-self, fresh `var z`, and a function-local that's compared.)
		{"large-lit-reassign", `function main(): i32 { var x: u32 = 4000000000; x = x + 1000000000; return ((x >> 28) & 255) as i32; }`},
		{"large-lit-var-z", `function main(): i32 { var x: u32 = 4000000000; var y: u32 = 1000000000; var z = x + y; return ((z >> 28) & 255) as i32; }`},
		// Unsigned COMPARE of a wrapped large-literal u32: after wrap x is
		// 705032704 (< 1e9); i64-backed it would be 5e9 (> 1e9). Signed-compare on
		// the bit-31-set value would also flip the answer.
		{"large-lit-compare", `function main(): i32 { var x: u32 = 4000000000; x = x + 1000000000; if (x < 1000000000) { return 7; } return 0; }`},
		// Division of a wrapped large-literal u32 (truncation-on-store can't mask
		// this — the quotient differs by the unwrapped high bits).
		{"large-lit-div", `function main(): i32 { var x: u32 = 4000000000; x = x + 1000000000; return ((x / 1000000) % 100) as i32; }`},
		// 5-term wrapping add (the SHA round shape).
		{"add5-wrap", `function main(): i32 { var a: u32 = 0xffffffff; var s: u32 = a + a + a + a + a; return ((s >> 24) & 255) as i32; }`},
		// u32 mul overflow.
		{"mul-wrap", `function main(): i32 { var a: u32 = 0x10001; var s: u32 = a * a * a; return ((s >> 16) & 255) as i32; }`},
		// u32 left-shift past bit 31 must drop the high bits.
		{"shl-wrap", `function main(): i32 { var a: u32 = 0xff; var s: u32 = a << 28; return ((s >> 24) & 255) as i32; }`},
		// Logical right shift of a bit-31-set u32 (must NOT sign-fill).
		{"shr-logical", `function main(): i32 { var a: u32 = 0x80000000; return ((a >> 24) & 255) as i32; }`},
		// __rotr: x << (32-n) overflows; the rotate must wrap. Inline use (no u32
		// local) is the form that miscompiled in the SHA schedule.
		{"rotr-inline", `function __rotr(x: u32, n: u32): u32 { return (x >> n) | (x << (32 - n)); } function main(): i32 { var x: u32 = 0x7da86405; var r: u32 = __rotr(x, 17) ^ __rotr(x, 19) ^ (x >> 10); return ((r >> 24) & 255) as i32; }`},
		// SHA-256("abc") — byte 0 (0xba) of ba7816bf… The whole schedule +
		// compression depend on u32 wrapping; native is the known-correct vector.
		{"sha256-abc-b0", shaCoreSrc + `function main(): i32 { var d: u8[] = __sha256_core(__str_to_bytes("abc")); return d[0] as i32; }`},
		// SHA-256("abc") byte 31 (0xad) — exercises the last state word.
		{"sha256-abc-b31", shaCoreSrc + `function main(): i32 { var d: u8[] = __sha256_core(__str_to_bytes("abc")); return d[31] as i32; }`},
		// SHA-256("") byte 0 (0xe3) — the single-block padding path.
		{"sha256-empty-b0", shaCoreSrc + `function main(): i32 { var d: u8[] = __sha256_core(__str_to_bytes("")); return d[0] as i32; }`},
		// __alloc_u8 + .with + u8-element read, and string_from_bytes_unchecked. (Not in the
		// IR≡AST differential test: the legacy asm_ir_run AST fallback references
		// __fern_alloc_u8 without emitting it, so its link fails there; the IR
		// path compiles them, validated here against native.)
		{"alloc-u8", `function main(): i32 { var m: u8[] = __alloc_u8(3); m = m.with(0, 65); m = m.with(2, 67); return (m[0] as i32) + (m[2] as i32); }`},
		{"str-from-bytes", `function main(): i32 { var m: u8[] = __alloc_u8(2); m = m.with(0, 72); m = m.with(1, 73); var s: string = string_from_bytes_unchecked(m); return s.len() * 100 + (s[0] as i32); }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, want := compileAndRunX86_64(t, tc.src) // native = the correct oracle
			if got := emitAndRunIR(t, tc.src); got != want {
				t.Errorf("self-host IR %q: exit = %d, want %d (native)", tc.name, got, want)
			}
		})
	}
}
