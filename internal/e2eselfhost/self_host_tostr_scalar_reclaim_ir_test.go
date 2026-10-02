package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tostrScalarReclaimCases pin #6599: `let s: string = i.to_string()` never freed its
// box on the self-host, unbounded in a loop, while native was flat.
//
// `str_free_producer_ident` admits the free-function spelling `n.to_string()` by
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
//	let s = i.to_string()   400/398/32     bounded, before and after (control)
//	let s = i.to_string()      400/0/6400  -> 400/398/32
//
// Identical allocation counts in both spellings, which is what proves int_to_string's
// own `__alloc_u8` buffer and string_from_bytes_unchecked are not involved: the whole
// difference was this one credit. It matters out of proportion to the shape because
// every `f"{x}"` desugars to `x.to_string()`.
//
// WHY THE TEST LIVES AT THE CREDIT SITE. `str_local_binding_is_fresh` is deliberately
// PURELY SYNTACTIC — no lowering state, no types — with the type gate applied separately
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
	// A scalar PARAM receiver. The receiver-type test reads declared types, and a
	// parameter is a declaration that never appears as a `let` in the body — so
	// until the harvesters were seeded with the function's ParamDecl[] this shape
	// was refused and leaked (12800 over 400 rounds on x86-64, 9600 on wasm).
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
	// PARAM negative: seeding the harvesters with parameters must not widen the
	// credit past the type test. A struct param whose user `to_string` returns an
	// ALIAS of a field the receiver still owns is refused for the same reason the
	// local-receiver case above is — the declared type is not a scalar.
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
	// CONTROL: the free-function spelling was already credited and must stay bounded,
	// so a regression here means the shared gates moved rather than this class.
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
	// VALUE guard: the credited box must not be freed while still readable. 200 rounds
	// of len("0".."199") = 10*1 + 90*2 + 100*3 = 490, %251 = 239.
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
	// STRING-RECEIVER negative: the identity case must stay UNCREDITED. It leaks by
	// design (both the literal source — excluded by #6590's litstr_tostring_receiver —
	// and the aliasing result), and the point of the case is that the source is still
	// readable afterwards with the detector at zero. Crediting either would double-
	// release one box. 200 rounds of 2 = 400, %251 = 149, %97 = 52.
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
	// #10369: a scalar `.to_string()` local stored in a struct FIELD. The
	// construction retains the box and the struct's field drop gives it back, so the
	// local keeps its own credit only if it earns the field credit every other fresh
	// string local gets; without it the store read as an escape and every box leaked.
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
	// #10369: the same local reassigned into an alias (`t = s`), which retains and
	// shares the credit only when the source is a credited fresh string local.
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
	// VALUE guard for the field credit: a record built from a `.to_string()` local
	// before the loop is read after it, so a release of the source that took the
	// field's share with it shows as a wrong value or an underflow.
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
	// STRING-RECEIVER negative for the field and reassign credits: the result is the
	// receiver's own box, so neither credit may reach it. 200 rounds of 2+2 = 800,
	// %251 = 47.
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
	// The receiver-typed `join` and `trim` locals are fresh string locals on the same
	// terms, so they earn the same field and alias-reassign credits.
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
	// ESCAPE negative: the credited local is returned, so it must NOT be freed.
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
	// A scalar `to_string` local that is itself a `to_string` receiver: the
	// string receiver's result is the receiver itself, so the pair shares one box
	// and neither release may double-free it. The row pins the shape (bounded
	// heap, no underflow, exact value), not which reclaim gate decided it;
	// TestSelfHostStrarrFieldToString witnesses the scalar `.to_string()`
	// admission. 980 from the first loop plus 18890 from the
	// second, %251 = 41.
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

// TestSelfHostTostrScalarReclaimIRX86_64 drives the cases through the self-hosted
// x86-64 compiler (asm_load_run), heap-bump + underflow guarded.
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

// TestSelfHostTostrScalarReclaimWasmIR drives the same cases through the self-hosted
// wasm backend (asm_load_run -target wasm32-wasi), where a string box carries an rc header and an over-release ticks
// the underflow counter (exit 99) instead of passing silently.
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
