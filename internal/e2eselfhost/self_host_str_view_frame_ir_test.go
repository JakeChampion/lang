package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strViewFrameCases pin #6713: a string slice `s[a:b]` is a zero-copy VIEW, and on the
// register backends the self-host materialised its 24-byte box — [rc=-1, data, len] —
// on the HEAP. The box is born immortal (rc=-1) because its data pointer aims into the
// SOURCE string's buffer, so freeing it would attack the middle of someone else's
// allocation; immortal means every rc_dec skips it, so the box is unreclaimable by
// construction. One per EVALUATION, unbounded in a loop, where native allocates nothing
// at all for the same program.
//
// Measured with FERN_LEAKCHECK=1, self-host x86-64, on the issue's reproducer
// (`while (i < k) { var t: str = s[0:1]; n = n + t.len(); i = i + 1; }`):
//
//	k    before                              after
//	400  allocs=401 frees=0 live=9624        allocs=1 frees=0 live=24
//	800  allocs=801 frees=0 live=19224       allocs=1 frees=0 live=24
//
// The fix is placement, not reclamation: a view whose every use BORROWS it takes its box
// from three reserved frame slots instead of the arena, so it dies with the frame and the
// count stops growing. Layout, the rc=-1 sentinel and every consumer are unchanged.
//
// The borrow whitelist is narrow (`.len()`, a byte index, a comparison / concat operand,
// the source of a further slice) because there are two hazards, not one: a reference that
// outlives the FRAME dangles, and — since one frame slot serves a site across every
// iteration — a reference that outlives the next execution of the SITE silently reads the
// newer view. Copying the name into a second local is the second hazard's shape, so the
// escape cases below carry the value guards that turn a wrong admission into a wrong
// ANSWER rather than only a byte count.
//
// wasm is unaffected: its `str_slice` copies the bytes into a fresh inline block, so
// there is no box to place.
var strViewFrameCases = []struct {
	name string
	src  string
	want int
}{
	// The reproducer, heap-bump guarded: 5000 rounds of a borrow-only view must not
	// move the bump pointer.
	{"strview-loop-flat", `function main(): i32 {
    var s: string = "hello world";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var t: str = s[0:1]; acc = (acc + t.len()) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var t2: str = s[0:1]; acc = (acc + t2.len()) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// VALUE guard on the same shape: varying bounds, and the bytes read back. A frame
	// box the analysis placed wrongly shows up here as a wrong answer.
	{"strview-value-exact", `function main(): i32 {
    var s: string = "abcdefgh";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        var t: str = s[i:i + 2];
        if (t.len() != 2) { return 96; }
        if (t[0] != s[i]) { return 95; }
        if (t[1] != s[i + 1]) { return 94; }
        acc = acc + (t[0] as i32);
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc != 97 + 98 + 99 + 100 + 101 + 102) { return 97; }
    return 0;
}`, 0},
	// Concat and comparison operands are borrows too, and both read the bytes.
	{"strview-concat-compare", `function main(): i32 {
    var s: string = "hello world";
    var hits: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var t: str = s[1:4];
        if (t == "ell") { hits = hits + 1; }
        var wrapped: string = "<" + t + ">";
        if (wrapped.len() != 5) { return 96; }
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (hits != 200) { return 97; }
    if (b1 < 0) { return 95; }
    return 0;
}`, 0},
	// A slice OF a view: the inner view's data pointer aims at the source buffer, not at
	// the outer box, so it outlives it — both are eligible.
	{"strview-nested-slice", `function main(): i32 {
    var s: string = "abcdefgh";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var t: str = s[2:6];
        var u: str = t[1:3];
        if (u.len() != 2) { return 96; }
        if (u[0] != 100) { return 95; }
        acc = (acc + u.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc != (400) % 251) { return 97; }
    return 0;
}`, 0},
	// ESCAPE negative — the view is RETURNED, so it must keep its heap box: a frame box
	// would dangle the moment the callee's frame is gone. `churn` reuses that dead
	// frame's slots before the view is read, which is what turns the dangle from
	// "usually still there" into a wrong answer (it exits 96 with the borrow scan
	// disabled).
	{"strview-escape-return-safe", `function head(s: string): str { var t: str = s[0:4]; return t; }
function churn(n: i32): i32 {
    var a: i32 = n * 3;
    var b: i32 = a + 7;
    var c: i32 = b * 2;
    var d: i32 = c - a;
    var e: i32 = d + b;
    var f: i32 = e % 97;
    return a + b + c + d + e + f;
}
function main(): i32 {
    var s: string = "hello world";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var v: str = head(s);
        acc = (acc + churn(i)) % 251;
        if (v.len() != 4) { return 96; }
        if (v[0] != 104) { return 95; }
        if (v[3] != 108) { return 94; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ESCAPE negative — the view is copied into a second local that survives into the
	// NEXT iteration, where the site would have rewritten a shared frame box under it.
	// This is the case a frame-only lifetime rule would get wrong.
	{"strview-escape-alias-safe", `function main(): i32 {
    var s: string = "abcdefgh";
    var prev: str = s[0:2];
    var acc: i32 = 0;
    var i: i32 = 1;
    while (i < 6) {
        var cur: str = s[i:i + 2];
        if (prev[0] != s[i - 1]) { return 95; }
        acc = acc + (prev[0] as i32);
        prev = cur;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (prev[0] != 102) { return 96; }
    if (acc != 97 + 98 + 99 + 100 + 101) { return 97; }
    return 0;
}`, 0},
	// ESCAPE negative — the view is passed to a callee that KEEPS it, in an array
	// outliving the frame that made the view. This is why a call argument is refused
	// even though the caller's frame is still alive at the call itself.
	{"strview-escape-arg-safe", `function keep(v: str, xs: string[]): string[] { return xs.append(v); }
function build(s: string, k: i32): string[] {
    var xs: string[] = [];
    var i: i32 = 0;
    while (i < k) { var t: str = s[i:i + 2]; xs = keep(t, xs); i = i + 1; }
    return xs;
}
function churn(n: i32): i32 { var a: i32 = n * 3; var b: i32 = a + 7; var c: i32 = b * 2; return a + b + c; }
function main(): i32 {
    var s: string = "abcdefgh";
    var xs: string[] = build(s, 5);
    var acc: i32 = churn(5) % 251;
    var j: i32 = 0;
    while (j < xs.len()) {
        if (xs[j].len() != 2) { return 96; }
        if (xs[j][0] != s[j]) { return 95; }
        j = j + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// ESCAPE negative — the views are stored in a container the BUILDING frame returns,
	// so every one of them outlives the frame that would have held its box.
	{"strview-escape-store-safe", `function collect(s: string, k: i32): string[] {
    var xs: string[] = [];
    var i: i32 = 0;
    while (i < k) { var t: str = s[i:i + 2]; xs = xs.append(t); i = i + 1; }
    return xs;
}
function churn(n: i32): i32 {
    var a: i32 = n * 3;
    var b: i32 = a + 7;
    var c: i32 = b * 2;
    var d: i32 = c - a;
    var e: i32 = d + b;
    return a + b + c + d + e;
}
function main(): i32 {
    var s: string = "abcdefgh";
    var xs: string[] = collect(s, 5);
    var acc: i32 = churn(3) % 251;
    var j: i32 = 0;
    while (j < xs.len()) {
        if (xs[j].len() != 2) { return 96; }
        if (xs[j][0] != s[j]) { return 95; }
        acc = acc + (xs[j][0] as i32);
        j = j + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostStrViewFrameIRX86_64 drives the cases through the self-hosted x86-64
// compiler (asm_run), heap-bump + underflow + value guarded.
func TestSelfHostStrViewFrameIRX86_64(t *testing.T) {
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

	for _, tc := range strViewFrameCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
				t.Errorf("%s = %d, want %d (98 = view box still per-evaluation; 99 = over-release/underflow; 94-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrViewFrameIRArm64 is the arm64 leg — the other backend that boxes a
// slice view, and so the other one that allocated per evaluation.
func TestSelfHostStrViewFrameIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strViewFrameCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = view box still per-evaluation; 99 = over-release/underflow; 94-97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
