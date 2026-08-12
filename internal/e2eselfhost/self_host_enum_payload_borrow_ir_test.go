package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostEnumPayloadBorrowIRX86_64 pins the two #6049 rc defects around an
// enum value that lives inside another value, both of which produced a
// use-after-free the moment the freed block was recycled:
//
//  1. A DIRECT ENUM struct field read stored into a CONTAINER (`[p.node]`,
//     `xs.append(p.node)`) took no Perceus dup, while __struct_drop_<T>'s k_enum
//     arm decs that field when the struct is reclaimed — so the container's
//     element was freed under it. enum_field_read_type now drives the alias-inc
//     at both the array-literal and the append sites.
//  2. A match arm's PAYLOAD ARRAY binding (`Seq(xs) => …`, and the same for an
//     Option/Result payload) was marked is_arr and therefore swept at every
//     function exit, decing a buffer the box owns — once per call, so the
//     payload died on the second entry. Both lowerings already described the
//     binding as borrowed and leak-only; is_arr alone put it in the sweep
//     anyway. LocalInfo.borrowed_arr keeps the sweep off a borrowed binding.
//
// In std/regex both fire in the same parse tree: `__rx_alt` builds its branches
// array out of `RParse.node` field reads and `__rx_match` re-binds `RSeq(xs)` /
// `RAlt(xs)` once per scan position. The freed RSeq box came back from the
// freelist as the enclosing RGroup, making the tree self-referential and
// `__rx_match` recurse until the stack ran out (six fixtures, x86-64 + arm64).
//
// Every program below allocates BETWEEN the reads so a freed block is actually
// recycled — without that churn the stale pointer still reads plausible data and
// the bug is invisible (a first cut of these probes passed on the broken
// compiler for exactly that reason). Values are checked in Fern and the
// over-release detector is asserted, so a wrong exit code is unambiguous.
func TestSelfHostEnumPayloadBorrowIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (97 = payload read back wrong → use-after-free; 99 = over-release detector)", name, code, want)
		}
	}

	// (1) array-LITERAL element: `[first.node]` over a struct whose enum field
	// the exit sweep deep-drops. The churn array below is the recycler — the
	// freed Seq box comes straight back and the element reads as a Leaf.
	run(t, `enum N { Leaf(i32), Seq(N[]) }
struct P { node: N, pos: i32 }
function mkp(): P { var kids: N[] = [Leaf(7), Leaf(8)]; return P { node: Seq(kids), pos: 1 }; }
function build(): N[] { var first: P = mkp(); var out: N[] = [first.node]; return out; }
function describe(n: N): i32 { match (n) { Leaf(v) => { return v; }, Seq(xs) => { return 100 + xs.len(); } } }
function main(): i32 {
    var xs: N[] = build();
    var churn: N[] = [Leaf(1), Leaf(2), Leaf(3)];
    var churn2: N = Leaf(55);
    if (describe(xs[0]) != 102) { return 97; }
    if (describe(churn[0]) + describe(churn2) != 56) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "enum-field-into-array-literal", 0)

	// (1) the .append sibling: `out = out.append(first.node)` takes the in-place
	// arr_push_owned path, which stored the borrowed pointer with no counted
	// reference exactly like the literal above.
	run(t, `enum N { Leaf(i32), Seq(N[]) }
struct P { node: N, pos: i32 }
function mkp(): P { var kids: N[] = [Leaf(7), Leaf(8)]; return P { node: Seq(kids), pos: 1 }; }
function build(): N[] { var first: P = mkp(); var out: N[] = []; out = out.append(first.node); return out; }
function describe(n: N): i32 { match (n) { Leaf(v) => { return v; }, Seq(xs) => { return 100 + xs.len(); } } }
function main(): i32 {
    var xs: N[] = build();
    var churn: N[] = [Leaf(1), Leaf(2), Leaf(3)];
    var churn2: N = Leaf(55);
    if (describe(xs[0]) != 102) { return 97; }
    if (describe(churn[0]) + describe(churn2) != 56) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "enum-field-into-append", 0)

	// (2) the arm-binding borrow: `total` is re-entered 8 times over the SAME
	// tree with a fresh array allocated between calls. One sweep dec per entry
	// took the payload buffer's count to 0 on the second call, and `churn`
	// recycled the block, so the third call summed whatever landed there.
	run(t, `enum N { Leaf(i32), Seq(N[]) }
function total(n: N): i32 {
    match (n) {
        Leaf(v) => { return v; },
        Seq(xs) => {
            var t: i32 = 0;
            var i: i32 = 0;
            while (i < xs.len()) { t = t + total(xs[i]); i = i + 1; }
            return t;
        }
    }
}
function main(): i32 {
    var kids: N[] = [Leaf(1), Leaf(2), Leaf(4)];
    var n: N = Seq(kids);
    var k: i32 = 0;
    while (k < 8) {
        if (total(n) != 7) { return 97; }
        var churn: i32[] = [k + 100, k + 200, k + 300];
        if (churn[0] != k + 100) { return 97; }
        k = k + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "enum-array-payload-arm-borrow", 0)

	// (2) the same borrow through a `string[]` payload — the other spelling the
	// arm binding marks is_arr, so it took the identical sweep dec.
	run(t, `enum Doc { Empty, Lines(string[]) }
function width(d: Doc): i32 {
    match (d) {
        Empty => { return 0; },
        Lines(ls) => {
            var w: i32 = 0;
            var i: i32 = 0;
            while (i < ls.len()) { w = w + ls[i].len(); i = i + 1; }
            return w;
        }
    }
}
function main(): i32 {
    var d: Doc = Lines(["ab", "cde"]);
    var k: i32 = 0;
    while (k < 8) {
        if (width(d) != 5) { return 97; }
        var churn: i32[] = [k, k + 1, k + 2];
        if (churn[2] != k + 2) { return 97; }
        k = k + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "enum-strarr-payload-arm-borrow", 0)

	// (2) the Option/Result arm binding had the identical defect — its own
	// lowering already calls the array payload "BORROWED … leak-only", but
	// is_arr alone put it in the sweep, so the box's buffer was released once
	// per entry with nothing balancing it. Same opt-out.
	run(t, `function width(d: Option[i32[]]): i32 {
    match (d) {
        Some(xs) => {
            var w: i32 = 0;
            var i: i32 = 0;
            while (i < xs.len()) { w = w + xs[i]; i = i + 1; }
            return w;
        },
        None => { return 0; }
    }
}
function main(): i32 {
    var base: i32[] = [1, 2, 4];
    var d: Option[i32[]] = Some(base);
    var k: i32 = 0;
    while (k < 8) {
        if (width(d) != 7) { return 97; }
        var churn: i32[] = [k + 9, k + 8, k + 7];
        if (churn[0] != k + 9) { return 97; }
        k = k + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "option-array-payload-arm-borrow", 0)
}

// #6121: routing the same enum field read through a LOCAL defeated #6049's
// retain. `var tmp: N = first.node` binds an uncounted alias of the source
// struct's enum box; the container store that follows is then a bare ident, and
// the ident arm only retained an rc-CONTAINER slot (array / string / tuple), of
// which an enum slot is none. So __struct_drop_P's k_enum arm freed the box the
// array still pointed at — the identical use-after-free #6049 fixed for the
// direct spelling, one indirection away, on all three backends.
//
// The bind takes no dup: an alias that never escapes must not outlive the
// struct. The mark travels with the name (mark_enum_field_alias) and the retain
// happens at the store, so the balance is exactly the direct read's — field
// rc 1 → store dup 2 → __struct_drop_<T> 1 → the container keeps it.
//
// Every case churns after the struct dies so a freed block is really recycled;
// without that the stale pointer still reads plausible data (97 = value read
// back wrong, 96 = wrong length, 99 = over-release detector).
var selfHostEnumFieldAliasCases = []struct {
	name string
	src  string
	exit int
}{
	// The issue's repro: the array LITERAL element is a bare ident.
	{"alias-into-array-literal", `enum N { Leaf(i32), Seq(N[]) }
struct P { node: N, pos: i32 }
function mkp(): P { var kids: N[] = [Leaf(7), Leaf(8)]; return P { node: Seq(kids), pos: 1 }; }
function build(): N[] { var first: P = mkp(); var tmp: N = first.node; var out: N[] = [tmp]; return out; }
function describe(n: N): i32 { match (n) { Leaf(v) => { return v; }, Seq(xs) => { return 100 + xs.len(); } } }
function main(): i32 {
    var xs: N[] = build();
    var churn: N[] = [Leaf(1), Leaf(2), Leaf(3)];
    var churn2: N = Leaf(55);
    if (describe(xs[0]) != 102) { return 97; }
    if (describe(churn[0]) + describe(churn2) != 56) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// The .append sibling — the in-place arr_push path, where the retain is an
	// inc bracket over the loaded slot rather than an inline inc.
	{"alias-into-append", `enum N { Leaf(i32), Seq(N[]) }
struct P { node: N, pos: i32 }
function mkp(): P { var kids: N[] = [Leaf(7), Leaf(8)]; return P { node: Seq(kids), pos: 1 }; }
function build(): N[] { var first: P = mkp(); var tmp: N = first.node; var out: N[] = []; out = out.append(tmp); return out; }
function describe(n: N): i32 { match (n) { Leaf(v) => { return v; }, Seq(xs) => { return 100 + xs.len(); } } }
function main(): i32 {
    var xs: N[] = build();
    var churn: N[] = [Leaf(1), Leaf(2), Leaf(3)];
    var churn2: N = Leaf(55);
    if (describe(xs[0]) != 102) { return 97; }
    if (describe(churn[0]) + describe(churn2) != 56) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// TWO containers off one alias: the retain is per-store, not per-bind, so
	// both references are counted and the struct's dec still leaves one live.
	{"alias-into-two-containers", `enum N { Leaf(i32), Seq(N[]) }
struct P { node: N, pos: i32 }
function mkp(): P { var kids: N[] = [Leaf(7), Leaf(8)]; return P { node: Seq(kids), pos: 1 }; }
function build(): N[] { var first: P = mkp(); var tmp: N = first.node; var a: N[] = [tmp]; var b: N[] = []; b = b.append(tmp); return a.append(b[0]); }
function describe(n: N): i32 { match (n) { Leaf(v) => { return v; }, Seq(xs) => { return 100 + xs.len(); } } }
function main(): i32 {
    var xs: N[] = build();
    var churn: N[] = [Leaf(1), Leaf(2), Leaf(3)];
    var churn2: N = Leaf(55);
    if (xs.len() != 2) { return 96; }
    if (describe(xs[0]) != 102) { return 97; }
    if (describe(xs[1]) != 102) { return 97; }
    if (describe(churn[0]) + describe(churn2) != 56) { return 97; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostEnumFieldAliasIRX86_64 — the #6121 cases through the production
// x86-64 IR path. All three exit 97 on the parent commit.
func TestSelfHostEnumFieldAliasIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostEnumFieldAliasCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d (97 = enum box read back wrong → use-after-free; 99 = over-release detector)", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostEnumFieldAliasIRArm64 — the same cases on arm64, which the shared
// irlower analysis makes a real second backend rather than a formality: the
// retain sites emit through a different instruction selector, and the parent
// commit fails all three here too.
func TestSelfHostEnumFieldAliasIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostEnumFieldAliasCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
