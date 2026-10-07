package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// litStrLocalReclaimCases pin #6582: a literal-initialised string local declared INSIDE
// a loop body must free its box every iteration on the asm backends. A string literal
// allocates a fresh box per evaluation — the DATA is .rodata but the box is not, and
// __fern_str_free's heap-base guard skips the data and reclaims the box — so a named
// literal binding is as fresh as any other string producer.
//
// The census (FERN_LEAKCHECK=1) is what sees this; `__heap_bump_bytes()` deltas cannot
// (#5474). wasm is unaffected: the whole literal is data-section there and arr_dec is a
// guarded no-op.
//
// A literal local that is the receiver of `.to_string()` must NOT be freed: on a string
// receiver that call is the IDENTITY, so its result aliases the receiver's box and is
// freed as an inline-consumed concat temp; freeing the local too would release the same
// box twice. TestSelfHostStrConcatTempIRX86_64's `tostring-string-recv-alias-safe` case
// pins that.
var litStrLocalReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// The reproducer: a literal string local re-declared per iteration, used borrow-only.
	{"litstr-loop-local", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let pre: string = "ab"; acc = (acc + pre.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let p2: string = "cd"; acc = (acc + p2.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// #6606: a local bound to a USER function's string result, not just a
	// builtin's: `let t = suffix(i)` is freed every round.
	{"strfresh-ret-call-loop-local", `function suffix(n: i32): string { if (n % 2 == 0) { return "even"; } return "odd"; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let t: string = suffix(i); acc = (acc + t.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let t2: string = suffix(j); acc = (acc + t2.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// The OVER-RELEASE guard on that credit, and the reason the freshness half is a
	// whole-program proof rather than "it is a call". `ident` returns its PARAMETER, so
	// its result ALIASES the caller's box — releasing it at the binding as if it were
	// fresh would free a string the caller still holds. Reading every value back
	// afterwards turns a wrong admission into a wrong ANSWER rather than only a byte count.
	{"strfresh-identity-ret-safe", `@noinline function ident(s: string): string { return s; }
function main(): i32 {
    let keep: string = "abcd";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let t: string = ident(keep); acc = (acc + t.len()) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (keep.len() != 4) { return 96; }
    if (acc != (200 * 4) % 251) { return 95; }
    return 0;
}`, 0},
	// The shape #5474's gate tripped on: the same local feeding a scalar array build.
	{"litstr-loop-local-with-array", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let pre: string = "ab"; let xs: i32[] = [pre.len(), 2]; acc = (acc + xs.len()) % 251; i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 5000) { let p2: string = "cd"; let ys: i32[] = [p2.len(), 2]; acc = (acc + ys.len()) % 251; j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// VALUE guard: the freed box must not be read back. A literal local re-declared per
	// iteration and compared, so a premature free or a shared/interned box shows up as a
	// wrong answer rather than only as a byte count. 200 rounds x 2 = 400, %251 = 149.
	{"litstr-value-exact", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let pre: string = "ab";
        if (pre != "ab") { return 97; }
        acc = (acc + pre.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 149) { return 97; }
    return 0;
}`, 0},
	// ESCAPE negative: the literal local is returned, so it must NOT be freed.
	{"litstr-escape-return-safe", `function mk(): string { let pre: string = "abcd"; return pre; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { let s: string = mk(); acc = (acc + s.len()) % 251; i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (acc != 47) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostLitStrLocalReclaimIRX86_64 drives the cases through the self-hosted x86-64
// compiler (asm_run), heap-bump, census and underflow guarded.
func TestSelfHostLitStrLocalReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	t.Setenv("FERN_LEAKCHECK", "1")
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range litStrLocalReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			stderr, code := hevRun(t, runner, bin)
			if code != tc.want {
				t.Errorf("%s = %d, want %d (98 = literal string local leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", tc.name, allocs, frees, live)
			}
		})
	}
}

// TestSelfHostLitStrLocalReclaimIRArm64 is the arm64 leg — the other backend that
// allocates a box for a string literal, and so the other one that leaked.
func TestSelfHostLitStrLocalReclaimIRArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range litStrLocalReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d (98 = literal string local leaked; 99 = over-release/underflow; 97 = value corrupted)\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
