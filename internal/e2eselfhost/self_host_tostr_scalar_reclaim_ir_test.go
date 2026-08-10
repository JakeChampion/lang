package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tostrScalarReclaimCases pin #6599: `var s: string = i.to_string()` never freed its
// box on the self-host, unbounded in a loop, while native was flat.
//
// `str_free_producer_ident` admits the free-function spelling `i32_to_string(n)` by
// name and excludes the method form; `str_local_binding_is_fresh` lists `.to_string()`
// under "receiver-identity fast-paths". That is right for a STRING receiver, where the
// call returns the receiver itself so freeing the result would release a box the source
// still owns — and wrong for a SCALAR receiver, where it is the decimal-text builtin
// returning a fresh sole-owned box: the same value, by the same allocation, as the
// free-function spelling already credited.
//
// Measured with FERN_LEAKCHECK=1 (allocs/frees/live_bytes), 200 iterations, self-host
// x86-64 — `__heap_bump_bytes()` deltas cannot see this, see #5474's retraction:
//
//	var s = i32_to_string(i)   400/398/32     bounded, before and after (control)
//	var s = i.to_string()      400/0/6400  -> 400/398/32
//
// Identical allocation counts in both spellings, which is what proves int_to_string's
// own `__alloc_u8` buffer and string_from_bytes_unchecked are not involved: the whole
// difference was this one credit. It matters out of proportion to the shape because
// every `f"{x}"` desugars to `x.to_string()`.
//
// WHY THE TEST LIVES AT THE CREDIT SITE. `str_local_binding_is_fresh` is deliberately
// PURELY SYNTACTIC — no LowerState, no types — with the type gate applied separately
// through the slot's is_str. is_str is true for BOTH receivers here, because the RESULT
// is a string either way; what has to be tested is the RECEIVER's type, which that
// predicate cannot see. Its ~20 other callers drive the accumulator and concat-temp
// analyses, where widening it broke two over-release contracts in #6590. So the
// receiver-type test is a separate collector in reclaimable_names_of, and an UNKNOWN
// receiver type is refused rather than assumed scalar — the wrong answer on this side
// is an over-release, not a leak.
var tostrScalarReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// The reproducer: a scalar receiver, re-declared per iteration, borrow-only use.
	{"tostr-scalar-loop-local", `import "std/i32";
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var s: string = i.to_string(); acc = (acc + s.len()) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var t: string = j.to_string(); acc = (acc + t.len()) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 2048) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// CONTROL: the free-function spelling was already credited and must stay bounded,
	// so a regression here means the shared gates moved rather than this class.
	{"tostr-freefn-control", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var s: string = i32_to_string(i); acc = (acc + s.len()) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var t: string = i32_to_string(j); acc = (acc + t.len()) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 2048) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// VALUE guard: the credited box must not be freed while still readable. 200 rounds
	// of len("0".."199") = 10*1 + 90*2 + 100*3 = 490, %251 = 239.
	{"tostr-scalar-value-exact", `import "std/i32";
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var s: string = i.to_string();
        if (s.len() < 1) { return 97; }
        acc = (acc + s.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc != 239) { return 97; }
    return 0;
}`, 0},
	// STRING-RECEIVER negative: the identity case must stay UNCREDITED. It leaks by
	// design (both the literal source — excluded by #6590's litstr_tostring_receiver —
	// and the aliasing result), and the point of the case is that the source is still
	// readable afterwards with the detector at zero. Crediting either would double-
	// release one box. 200 rounds of 2 = 400, %251 = 149, %97 = 52.
	{"tostr-string-recv-uncredited", `import "std/string";
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var base: string = "ab";
        var s: string = base.to_string();
        if (base.len() != 2) { return 97; }
        if (s.len() != 2) { return 97; }
        acc = (acc + s.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc != 149) { return 97; }
    return 52;
}`, 52},
	// ESCAPE negative: the credited local is returned, so it must NOT be freed.
	{"tostr-scalar-escape-return-safe", `import "std/i32";
function mk(n: i32): string { var s: string = n.to_string(); return s; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var r: string = mk(i); acc = (acc + r.len()) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc != 239) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostTostrScalarReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostTostrScalarReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range tostrScalarReclaimCases {
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
				t.Errorf("%s = %d, want %d (98 = to_string box leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
