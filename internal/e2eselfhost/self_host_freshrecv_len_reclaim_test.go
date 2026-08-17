package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// freshRecvLenCases pin the fresh-or-RECEIVER method reclaim in `.len()`
// receiver position (#6544).
//
// A borrowing string method has one shape: return a fresh box, or return the
// receiver unchanged when there is nothing to do — `(s: string) tails(n)` is
// `if (n <= 0) { return s; }` and a slice otherwise. Freshness alone cannot
// admit that, and native's route is not open either: it reclaims the identity
// return through an is_unique gate resting on a return-transfer inc the
// self-host emits for array params only, and adding that inc UNPAIRED measures
// strictly worse (docs/RC-PERCEUS-SELF-HOST-PORT.md §9).
//
// The SFRRECV: key proves the callee returns fresh-or-receiver, and the
// POINTER decides which arrived: a fresh box is never the receiver's pointer,
// an identity return always is. So the read frees only when they differ — no
// inc anywhere.
//
// The third admitted shape is a SLICE of the receiver, which is what
// `(s: string) drop(n)` and std/string's take / trim actually return. On the
// register backends that is a 24-byte rc-IMMORTAL box over the receiver's own
// bytes, so the release goes through __fern_str_view_free: the box returns to
// the freelist, the shared data is never touched. On wasm the slice copies, so
// the same call is an ordinary block release.
//
// Both directions are pinned. The churn cases prove the fresh box and the view
// box ARE freed (heap-bump flat); the identity and alias cases prove the shared
// ones are NOT — the receiver survives being read afterwards, its BYTES are
// re-read after thousands of view releases, and __rc_underflow() stays 0.
var freshRecvLenCases = []struct {
	name     string
	src      string
	expected int
}{
	// FRESH path freed: 5000 rounds stay flat. On the parent this leaked one
	// box per evaluation (48 bytes a round, measured).
	{"freshrecv-len-churn", `function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen] + "";
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) { var b2s: string = "long-enough-payload-" + (i % 8).to_string(); acc = (acc + b2s.tails(4).len()) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// IDENTITY path NOT freed: `b.tails(0)` hands back `b` itself, which is
	// read again afterwards. Freeing it would be a use-after-free; the value
	// check and the underflow counter are both witnesses.
	{"freshrecv-len-identity-alias-safe", `function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen] + "";
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 3000) {
        var b: string = "long-enough-payload-" + (i % 8).to_string();
        if (b.tails(0).len() != b.len()) { return 96; }
        if (b.len() != 21) { return 95; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// Both paths in one body, alternating per iteration — the discriminator is
	// a runtime pointer compare, so it has to decide correctly each time rather
	// than once per call site.
	{"freshrecv-len-alternating", `function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen] + "";
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) {
        var b3: string = "long-enough-payload-" + (i % 8).to_string();
        acc = (acc + b3.tails(i % 2 * 4).len()) % 251;
        if (b3.len() != 21) { return 96; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// A SLICE-of-receiver return, read back through the receiver. On the
	// register backends the released box's data pointer is `b`'s buffer + 4,
	// which is not even an allocation boundary — freeing it would push a
	// mid-block address onto the freelist, so the byte re-read after 3000
	// releases is the direct witness that only the box goes.
	{"freshrecv-len-view-alias-safe", `function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen];
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 3000) {
        var b: string = "long-enough-payload-" + (i % 8).to_string();
        if (b.tails(4).len() != 17) { return 96; }
        if (b.len() != 21) { return 95; }
        if ((b[20] as i32) != 48 + (i % 8)) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// All three admitted shapes in one callee, chosen per iteration by a
	// runtime compare: the receiver, a fresh literal box, and a view.
	{"freshrecv-len-view-alternating", `function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) {
        var b3: string = "long-enough-payload-" + (i % 8).to_string();
        acc = (acc + b3.tails((i % 3) * 40 - 40).len()) % 251;
        if (b3.len() != 21) { return 96; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// A CHAIN whose links both take their identity path, so the result IS the
	// root's own box. The guard has to recognise that through two levels — a
	// root walked wrong here frees a live local, and the byte re-read plus the
	// detector are the witnesses.
	{"freshrecv-len-chain-identity", `function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen];
}
function (s: string) same(): string { return s; }
function main(): i32 {
    var i: i32 = 0;
    while (i < 3000) {
        var b: string = "long-enough-payload-" + (i % 8).to_string();
        if (b.tails(0).same().len() != 21) { return 96; }
        if (b.len() != 21) { return 95; }
        if ((b[20] as i32) != 48 + (i % 8)) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// A chain whose OUTER link allocates over a receiver that is itself a view:
	// the result is the chain's own box and dies at the read, while the view it
	// was built from is still the root's bytes.
	{"freshrecv-len-chain-alias-safe", `function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen];
}
function (s: string) owned(): string { return s + ""; }
function main(): i32 {
    var i: i32 = 0;
    while (i < 3000) {
        var b: string = "long-enough-payload-" + (i % 8).to_string();
        if (b.tails(4).owned().len() != 17) { return 96; }
        if (b.len() != 21) { return 95; }
        if ((b[20] as i32) != 48 + (i % 8)) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// The REFUSAL control. `pick` also returns a bare non-receiver param, which
	// is neither fresh nor the receiver nor a view of it, so the whole callee
	// earns no SFRRECV key and nothing here is released. Admitting it would
	// free `alt` — whose pointer differs from the receiver's — while the caller
	// still owns it.
	{"freshrecv-len-view-nonrecv-return-refused", `function (s: string) pick(n: i32, alt: string): str {
    if (n < 0) { return alt; }
    if (n == 0) { return s; }
    return s[n:s.len()];
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 2000) {
        var b: string = "long-enough-payload-" + (i % 8).to_string();
        var alt: string = "alternate-payload-" + (i % 8).to_string();
        if (b.pick(0 - 1, alt).len() != 19) { return 96; }
        if (alt.len() != 19) { return 95; }
        if (b.len() != 21) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// freshRecvLenLeakCases are the LEAK half, x86-64 only. Heap flatness is
// asserted here rather than in the shared table because the wasm leg runs the
// WAT driver (wasm_ir_run), and every wasm sibling in this package asserts the
// over-release detector rather than heap growth for that reason. Flatness on
// wasm was checked separately through the CLI pipeline (`-target wasm32-wasi`),
// where both churns move the bump pointer by under 128 bytes across 5000
// rounds; the shared cases carry wasm's half, which is that nothing is
// over-released and the aliased receiver survives.
var freshRecvLenLeakCases = []struct {
	name string
	src  string
}{
	// A FRESH box return.
	{"freshrecv-len-leak-flat", `function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen] + "";
}
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var b: string = "long-enough-payload-" + (w % 8).to_string(); acc = (acc + b.tails(4).len()) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var c: string = "long-enough-payload-" + (i % 8).to_string(); acc = (acc + c.tails(4).len()) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// A VIEW return — the 24-byte immortal box `s[n:sLen]` allocates per call.
	// This is std/string's `drop` shape and the conformance case's own `tail`;
	// it leaked one box a round before __fern_str_view_free existed. `drop`
	// itself cannot be a row here — this driver resolves no imports — and was
	// measured flat through the CLI instead (docs/RC-PERCEUS-SELF-HOST-PORT.md
	// §9).
	{"freshrecv-len-view-leak-flat", `function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen];
}
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var b: string = "long-enough-payload-" + (w % 8).to_string(); acc = (acc + b.tails(4).len()) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var c: string = "long-enough-payload-" + (i % 8).to_string(); acc = (acc + c.tails(4).len()) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
	// A CHAINED receiver, `base.tails(4).owned().len()`. This one is not flat
	// and is not supposed to be: the chain's RESULT is released, the
	// intermediate view its outer link was built from is not — freeing an
	// intermediate needs the outer callee proven both borrowing and
	// never-a-view, which is a separate slice. So the assertion is structural
	// rather than absolute: one 24-byte box a round survives instead of three
	// allocations (a view box, the outer box, and the outer's data buffer).
	// Measured 71 B/round before, 22 after; the bound sits between with ~2x
	// margin either way.
	{"freshrecv-len-chain-bounded", `function (s: string) tails(n: i32): str {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen];
}
function (s: string) owned(): string { return s + ""; }
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var b: string = "long-enough-payload-" + (w % 8).to_string(); acc = (acc + b.tails(4).owned().len()) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var c: string = "long-enough-payload-" + (i % 8).to_string(); acc = (acc + c.tails(4).owned().len()) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 240000) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`},
}

// TestSelfHostFreshRecvLenReclaimX86_64 runs each case through the self-hosted
// x86-64 driver, plus the leak case. Exit 0 = reclaimed and safe; 98 = the
// fresh box leaked, 99 = over-release, 95/96 = a value went wrong.
func TestSelfHostFreshRecvLenReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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

// TestSelfHostFreshRecvLenReclaimWasm runs the same cases on the wasm IR
// backend, where the release maps to $__fern_arr_dec and its over-release
// detector is the direct witness that the identity alias is never freed.
func TestSelfHostFreshRecvLenReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fresh-or-receiver len reclaim wasm e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range freshRecvLenCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("fresh-or-receiver len reclaim wasm %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
